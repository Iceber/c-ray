package docker

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	"github.com/icebergu/c-ray/pkg/runtime"
	containerdrt "github.com/icebergu/c-ray/pkg/runtime/containerd"
)

// containerHandle implements runtime.Container backed by Docker Engine API.
type containerHandle struct {
	rt *Runtime
	id string

	// summary is set when created from ContainerList.
	summary *dockertypes.Container

	// inspectOnce lazily loads the full ContainerInspect result.
	inspectOnce sync.Once
	inspect     *dockertypes.ContainerJSON
	inspectErr  error
}

func (r *Runtime) newContainerHandle(summary *dockertypes.Container) *containerHandle {
	return &containerHandle{
		rt:      r,
		id:      summary.ID,
		summary: summary,
	}
}

func (r *Runtime) newContainerHandleFromInspect(inspection *dockertypes.ContainerJSON) *containerHandle {
	h := &containerHandle{
		rt:      r,
		id:      inspection.ID,
		inspect: inspection,
	}
	h.inspectOnce.Do(func() {}) // mark as loaded
	return h
}

func (h *containerHandle) ensureInspect(ctx context.Context) {
	h.inspectOnce.Do(func() {
		h.inspect, h.inspectErr = h.loadInspect(ctx)
	})
}

func (h *containerHandle) loadInspect(ctx context.Context) (*dockertypes.ContainerJSON, error) {
	resp, err := h.rt.dockerClient.ContainerInspect(ctx, h.id)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — identity
// ---------------------------------------------------------------------------

func (h *containerHandle) ID() string { return h.id }

// ---------------------------------------------------------------------------
// runtime.Container — Info
// ---------------------------------------------------------------------------

func (h *containerHandle) Info(ctx context.Context) (*runtime.ContainerInfo, error) {
	// Fast path: use summary if available (avoids inspect for list views).
	if h.summary != nil {
		ci := h.infoFromSummary()
		// Supplement PID from inspect data so that detail views can read
		// extended procfs info (threads, open FDs, listening ports).
		h.ensureInspect(ctx)
		if h.inspect != nil && h.inspect.State != nil {
			ci.PID = uint32(h.inspect.State.Pid)
		}
		return ci, nil
	}

	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}
	return h.infoFromInspect(), nil
}

func (h *containerHandle) infoFromSummary() *runtime.ContainerInfo {
	s := h.summary
	ci := &runtime.ContainerInfo{
		ID:        s.ID,
		Name:      dockerContainerName(s.Names),
		ImageRef:  s.Image,
		ImageID:   s.ImageID,
		Status:    convertDockerStatus(s.State),
		CreatedAt: time.Unix(s.Created, 0),
	}
	if labels := s.Labels; labels != nil {
		ci.PodName = labels["io.kubernetes.pod.name"]
		ci.PodNamespace = labels["io.kubernetes.pod.namespace"]
		ci.PodUID = labels["io.kubernetes.pod.uid"]
	}
	return ci
}

func (h *containerHandle) infoFromInspect() *runtime.ContainerInfo {
	i := h.inspect
	ci := &runtime.ContainerInfo{
		ID:       i.ID,
		Name:     strings.TrimPrefix(i.Name, "/"),
		ImageRef: i.Config.Image,
		ImageID:  i.Image,
	}
	if i.State != nil {
		ci.Status = convertDockerStatus(i.State.Status)
		ci.PID = uint32(i.State.Pid)
	}
	if ct, err := time.Parse(time.RFC3339Nano, i.Created); err == nil {
		ci.CreatedAt = ct
	}
	if i.Config != nil && i.Config.Labels != nil {
		ci.PodName = i.Config.Labels["io.kubernetes.pod.name"]
		ci.PodNamespace = i.Config.Labels["io.kubernetes.pod.namespace"]
		ci.PodUID = i.Config.Labels["io.kubernetes.pod.uid"]
	}
	return ci
}

// ---------------------------------------------------------------------------
// runtime.Container — Config
// ---------------------------------------------------------------------------

func (h *containerHandle) Config(ctx context.Context) (*runtime.ContainerConfig, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}
	i := h.inspect

	cfg := &runtime.ContainerConfig{}

	// Environment.
	if i.Config != nil && len(i.Config.Env) > 0 {
		cfg.Environment = runtime.ParseEnvVars(i.Config.Env)
	}

	// Image.
	if i.Config != nil {
		cfg.ImageRef = i.Config.Image
	}
	if i.Image != "" {
		cfg.ImageID = i.Image
	}

	// CGroup path — try HostConfig.CgroupParent first, fall back to live
	// /proc/<pid>/cgroup for standalone Docker containers where CgroupParent
	// is not explicitly set.
	if i.HostConfig != nil && i.HostConfig.CgroupParent != "" {
		cfg.CGroupPath = i.HostConfig.CgroupParent
		cfg.CGroupDriver = runtime.InferCGroupDriver(i.HostConfig.CgroupParent)
	}
	if cfg.CGroupPath == "" {
		cfg.CGroupPath = runtime.ResolveCGroupPath("", 0, h.liveContainerPID(), h.rt.procReader)
	}
	if cfg.CGroupDriver == "" && cfg.CGroupPath != "" {
		cfg.CGroupDriver = runtime.InferCGroupDriver(cfg.CGroupPath)
	}

	// CGroup version and root from reader.
	if h.rt.cgroupReader != nil {
		cfg.CGroupVersion = int(h.rt.cgroupReader.GetVersion())
	}
	if cfg.CGroupPath != "" {
		cfg.CGroupRootPath = runtime.CGroupRootPath()
	}

	// Namespaces from Docker HostConfig modes.
	cfg.Namespaces = dockerNamespaceMap(i)

	if h.rt != nil && h.rt.imageStoreMode == ImageStoreModeContainerd {
		if i.Driver != "" {
			cfg.Backend = &runtime.LayerBackend{
				Kind: runtime.LayerBackendContainerdSnapshotter,
				Name: i.Driver,
			}
		}
	} else {
		backendName := h.inspect.GraphDriver.Name
		if backendName == "" {
			backendName = i.Driver
		}
		if backendName != "" {
			cfg.Backend = &runtime.LayerBackend{
				Kind: runtime.LayerBackendDockerGraphDriver,
				Name: backendName,
			}
		}
	}

	// RW layer path from GraphDriver data.
	if gd := i.GraphDriver; len(gd.Data) > 0 {
		if upper, ok := gd.Data["UpperDir"]; ok {
			cfg.WritableLayerPath = upper
			if cfg.SnapshotKey == "" {
				cfg.SnapshotKey = dockerSnapshotKeyFromPath(upper)
			}
		}
		if lower, ok := gd.Data["LowerDir"]; ok {
			parts := strings.Split(lower, ":")
			if len(parts) > 0 {
				cfg.ReadOnlyLayerPath = parts[len(parts)-1]
			}
		}
	}
	if cfg.WritableLayerPath == "" || cfg.ReadOnlyLayerPath == "" {
		if live := h.resolveLiveRootPaths(); live != nil {
			if cfg.SnapshotKey == "" {
				cfg.SnapshotKey = live.rwSnapshotKey
			}
			if cfg.WritableLayerPath == "" {
				cfg.WritableLayerPath = live.upperdir
			}
			if cfg.ReadOnlyLayerPath == "" {
				cfg.ReadOnlyLayerPath = live.baseLayer
			}
		}
	}

	return cfg, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — State
// ---------------------------------------------------------------------------

func (h *containerHandle) State(ctx context.Context) (*runtime.ContainerState, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}

	state := &runtime.ContainerState{}
	ds := h.inspect.State
	if ds == nil {
		state.Status = runtime.ContainerStatusUnknown
		return state, nil
	}

	state.Status = convertDockerStatus(ds.Status)
	state.PID = uint32(ds.Pid)

	if ds.StartedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, ds.StartedAt); err == nil {
			state.StartedAt = t
		}
	}
	if ds.FinishedAt != "" {
		if t, err := time.Parse(time.RFC3339Nano, ds.FinishedAt); err == nil && !t.IsZero() {
			state.ExitedAt = t
		}
	}
	if !ds.Running && ds.ExitCode != 0 {
		code := int32(ds.ExitCode)
		state.ExitCode = &code
	}

	// PPID from procfs.
	if ds.Pid > 0 && h.rt.procReader != nil {
		if ppid, err := h.rt.procReader.GetProcessPPID(ds.Pid); err == nil {
			state.PPID = uint32(ppid)
		}
	}

	// Process count.
	if ds.Pid > 0 && h.rt.processCollector != nil {
		if procs, err := h.rt.processCollector.CollectContainerProcesses(uint32(ds.Pid)); err == nil {
			state.ProcessCount = len(procs)
		}
	}

	// RestartCount.
	if h.inspect.RestartCount > 0 {
		rc := uint32(h.inspect.RestartCount)
		state.RestartCount = &rc
	}

	return state, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — CGroup
// ---------------------------------------------------------------------------

func (h *containerHandle) CGroup(ctx context.Context) (*runtime.ContainerCGroupInfo, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}

	var rawPath string
	var pid uint32
	if h.inspect != nil {
		if h.inspect.HostConfig != nil {
			rawPath = h.inspect.HostConfig.CgroupParent
		}
		if h.inspect.State != nil && h.inspect.State.Pid > 0 {
			pid = uint32(h.inspect.State.Pid)
		}
	}

	info := &runtime.ContainerCGroupInfo{
		RootPath: runtime.CGroupRootPath(),
	}

	if h.rt.cgroupReader != nil {
		info.Version = int(h.rt.cgroupReader.GetVersion())
	}
	if rawPath != "" {
		info.Driver = runtime.InferCGroupDriver(rawPath)
	}
	info.Path = runtime.ResolveCGroupPath(rawPath, info.Version, pid, h.rt.procReader)
	if info.Driver == "" && info.Path != "" {
		info.Driver = runtime.InferCGroupDriver(info.Path)
	}

	runtime.PopulateCGroupStatsFromReader(info, h.rt.cgroupReader)

	info.SpecResources = extractDockerSpecResources(h.inspect)

	return info, nil
}

// extractDockerSpecResources builds CGroupSpecResources from Docker inspect data.
func extractDockerSpecResources(inspect *dockertypes.ContainerJSON) *runtime.CGroupSpecResources {
	if inspect == nil || inspect.HostConfig == nil {
		return nil
	}
	hc := inspect.HostConfig
	sr := &runtime.CGroupSpecResources{}
	hasValue := false

	if hc.CPUShares > 0 {
		shares := uint64(hc.CPUShares)
		sr.CPUShares = &shares
		hasValue = true
	}
	if hc.CPUQuota > 0 {
		sr.CPUQuota = &hc.CPUQuota
		hasValue = true
	}
	if hc.CPUPeriod > 0 {
		period := uint64(hc.CPUPeriod)
		sr.CPUPeriod = &period
		hasValue = true
	}
	if hc.NanoCPUs > 0 {
		quota := hc.NanoCPUs / 1000
		period := uint64(100000)
		sr.CPUQuota = &quota
		sr.CPUPeriod = &period
		hasValue = true
	}
	if hc.CpusetCpus != "" {
		sr.CPUSetCPUs = hc.CpusetCpus
		hasValue = true
	}
	if hc.CpusetMems != "" {
		sr.CPUSetMems = hc.CpusetMems
		hasValue = true
	}

	if hc.Memory > 0 {
		sr.MemoryLimit = &hc.Memory
		hasValue = true
	}
	if hc.MemoryReservation > 0 {
		sr.MemoryReservation = &hc.MemoryReservation
		hasValue = true
	}
	if hc.MemorySwap != 0 {
		sr.MemorySwap = &hc.MemorySwap
		hasValue = true
	}
	if hc.MemorySwappiness != nil {
		swappiness := uint64(*hc.MemorySwappiness)
		sr.MemorySwappiness = &swappiness
		hasValue = true
	}
	if hc.OomKillDisable != nil {
		sr.OOMKillDisable = hc.OomKillDisable
		hasValue = true
	}

	if hc.PidsLimit != nil && *hc.PidsLimit != 0 {
		sr.PidsLimit = hc.PidsLimit
		hasValue = true
	}

	if !hasValue {
		return nil
	}
	return sr
}

// ---------------------------------------------------------------------------
// runtime.Container — Network
// ---------------------------------------------------------------------------

func (h *containerHandle) Network(ctx context.Context) (*runtime.ContainerNetworkState, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}
	ns := h.inspect.NetworkSettings
	if ns == nil {
		return nil, nil
	}

	podNet := &runtime.PodNetworkInfo{
		NetNSPath: ns.SandboxKey,
		SandboxID: ns.SandboxID,
	}
	if h.inspect.Config != nil {
		podNet.Hostname = h.inspect.Config.Hostname
	}

	// Extract IPs and interfaces from per-network endpoint settings.
	// ns.IPAddress is only populated for the default bridge; the canonical
	// source for all networks is ns.Networks.
	if len(ns.Networks) > 0 {
		var interfaces []*runtime.CNIInterface
		first := true
		for netName, ep := range ns.Networks {
			if ep == nil {
				continue
			}
			ip := ep.IPAddress
			if first && ip != "" {
				podNet.PrimaryIP = ip
				first = false
			} else if ip != "" {
				podNet.AdditionalIPs = append(podNet.AdditionalIPs, ip)
			}
			iface := &runtime.CNIInterface{
				Name: netName,
				MAC:  ep.MacAddress,
			}
			if ip != "" {
				mask := "/16"
				if ep.IPPrefixLen > 0 {
					mask = fmt.Sprintf("/%d", ep.IPPrefixLen)
				}
				iface.Addresses = append(iface.Addresses, &runtime.CNIInterfaceAddress{
					CIDR:    ip + mask,
					Gateway: ep.Gateway,
					Family:  "inet",
				})
			}
			if ep.GlobalIPv6Address != "" {
				mask6 := "/64"
				if ep.GlobalIPv6PrefixLen > 0 {
					mask6 = fmt.Sprintf("/%d", ep.GlobalIPv6PrefixLen)
				}
				iface.Addresses = append(iface.Addresses, &runtime.CNIInterfaceAddress{
					CIDR:    ep.GlobalIPv6Address + mask6,
					Gateway: ep.IPv6Gateway,
					Family:  "inet6",
				})
			}
			interfaces = append(interfaces, iface)
		}
		if len(interfaces) > 0 {
			podNet.CNI = &runtime.CNIResultInfo{
				Interfaces: interfaces,
			}
		}
	} else if ns.IPAddress != "" {
		// Fallback: legacy top-level IP (bridge-only).
		podNet.PrimaryIP = ns.IPAddress
	}

	// Observed network interface traffic from /proc/<pid>/net/dev.
	if h.inspect.State != nil && h.inspect.State.Pid > 0 && h.rt.procReader != nil {
		if stats, err := h.rt.procReader.ReadNetDev(h.inspect.State.Pid); err == nil {
			podNet.ObservedInterfaces = runtime.ConvertNetworkStats(stats)
		}
	}

	// Port mappings.
	for portProto, bindings := range ns.Ports {
		parts := strings.SplitN(string(portProto), "/", 2)
		if len(parts) < 1 {
			continue
		}
		cp, _ := strconv.ParseUint(parts[0], 10, 16)
		proto := "tcp"
		if len(parts) > 1 {
			proto = parts[1]
		}
		for _, b := range bindings {
			hp, _ := strconv.ParseUint(b.HostPort, 10, 16)
			podNet.PortMappings = append(podNet.PortMappings, &runtime.PortMapping{
				HostIP:        b.HostIP,
				HostPort:      uint16(hp),
				ContainerPort: uint16(cp),
				Protocol:      proto,
			})
		}
	}

	// DNS from HostConfig.
	if hc := h.inspect.HostConfig; hc != nil {
		if len(hc.DNS) > 0 || len(hc.DNSSearch) > 0 || len(hc.DNSOptions) > 0 {
			podNet.DNS = &runtime.DNSConfig{
				Servers:  append([]string(nil), hc.DNS...),
				Searches: append([]string(nil), hc.DNSSearch...),
				Options:  append([]string(nil), hc.DNSOptions...),
			}
		}
	}

	// Network mode.
	if hc := h.inspect.HostConfig; hc != nil {
		mode := string(hc.NetworkMode)
		podNet.NamespaceMode = mode
		if mode == "host" {
			podNet.HostNetwork = true
		}
	}

	return &runtime.ContainerNetworkState{PodNetwork: podNet}, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Storage
// ---------------------------------------------------------------------------

func (h *containerHandle) Storage(ctx context.Context) (*runtime.ContainerStorage, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}

	storage := &runtime.ContainerStorage{}

	// ReadOnly rootfs from HostConfig.
	if h.inspect.HostConfig != nil && h.inspect.HostConfig.ReadonlyRootfs {
		storage.ReadOnly = true
	}

	live := h.resolveLiveRootPaths()

	gd := h.inspect.GraphDriver

	// RW layer path.
	if upper, ok := gd.Data["UpperDir"]; ok {
		storage.RWLayerPath = upper
	}
	if storage.RWLayerPath == "" && live != nil {
		storage.RWLayerPath = live.upperdir
	}

	dockerStorage := &runtime.DockerContainerStorage{}
	if h.rt != nil && h.rt.imageStoreMode == ImageStoreModeContainerd {
		dockerStorage.Snapshotter = h.inspect.Driver
		if dockerStorage.Snapshotter != "" {
			storage.Backend = &runtime.LayerBackend{
				Kind: runtime.LayerBackendContainerdSnapshotter,
				Name: dockerStorage.Snapshotter,
			}
		}
		if upper, ok := gd.Data["UpperDir"]; ok {
			dockerStorage.RWSnapshotKey = dockerSnapshotKeyFromPath(upper)
		}
		if dockerStorage.RWSnapshotKey == "" && live != nil {
			dockerStorage.RWSnapshotKey = live.rwSnapshotKey
		}
	} else {
		dockerStorage.GraphDriver = gd.Name
		if dockerStorage.GraphDriver == "" {
			dockerStorage.GraphDriver = h.inspect.Driver
		}
		if dockerStorage.GraphDriver != "" {
			storage.Backend = &runtime.LayerBackend{
				Kind: runtime.LayerBackendDockerGraphDriver,
				Name: dockerStorage.GraphDriver,
			}
		}
		if upper, ok := gd.Data["UpperDir"]; ok {
			dockerStorage.RWLayerID = dockerRWIDFromPath(upper)
		}
		if dockerStorage.RWLayerID == "" && live != nil {
			dockerStorage.RWLayerID = dockerRWIDFromPath(live.upperdir)
		}
	}
	if dockerStorage.GraphDriver != "" || dockerStorage.Snapshotter != "" || dockerStorage.RWSnapshotKey != "" || dockerStorage.RWLayerID != "" {
		storage.Docker = dockerStorage
	}

	// Resolve read-only image layers.
	img, err := h.Image(ctx)
	if err == nil {
		layers, err := img.Layers(ctx, h.readOnlyLayerQuery(storage))
		if err == nil {
			storage.ReadOnlyLayers = layers
		}
	}

	return storage, nil
}

func (h *containerHandle) readOnlyLayerQuery(storage *runtime.ContainerStorage) runtime.LayerQuery {
	query := runtime.LayerQuery{}
	if h.rt == nil || h.rt.imageStoreMode != ImageStoreModeContainerd || storage == nil {
		return query
	}
	if storage.Backend != nil && storage.Backend.Kind == runtime.LayerBackendContainerdSnapshotter {
		query.Snapshotter = storage.Backend.Name
	}
	if storage.Docker != nil {
		query.RWSnapshotKey = storage.Docker.RWSnapshotKey
	}
	return query
}

// ---------------------------------------------------------------------------
// runtime.Container — Mounts
// ---------------------------------------------------------------------------

func (h *containerHandle) Mounts(ctx context.Context) ([]*runtime.Mount, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}
	return convertDockerMounts(h.inspect.Mounts), nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Runtime
// ---------------------------------------------------------------------------

func (h *containerHandle) Runtime(ctx context.Context) (*runtime.RuntimeProfile, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}

	i := h.inspect
	profile := &runtime.RuntimeProfile{
		OCI:  &runtime.OCIInfo{},
		Shim: &runtime.ContainerdShimInfo{},
	}

	// -----------------------------------------------------------------------
	// OCI runtime name and binary
	// -----------------------------------------------------------------------

	// Determine the OCI runtime name for this container.
	runtimeName := "runc"
	if hc := i.HostConfig; hc != nil && hc.Runtime != "" {
		runtimeName = hc.Runtime
	} else if h.rt.daemonInfo != nil && h.rt.daemonInfo.DefaultRuntime != "" {
		runtimeName = h.rt.daemonInfo.DefaultRuntime
	}
	profile.OCI.RuntimeName = runtimeName

	// Resolve the OCI runtime binary path from daemon Runtimes map.
	if h.rt.daemonInfo != nil && h.rt.daemonInfo.Runtimes != nil {
		if rt, ok := h.rt.daemonInfo.Runtimes[runtimeName]; ok && rt.Path != "" {
			profile.OCI.RuntimeBinary = rt.Path
		}
	}

	// -----------------------------------------------------------------------
	// containerd shim / bundle paths
	// -----------------------------------------------------------------------

	// Docker uses containerd under the hood; derive the containerd state dir
	// from the containerd socket address (e.g. /run/containerd/containerd.sock → /run/containerd).
	containerdStateDir := dockerContainerdStateDir(h.rt.daemonInfo)
	namespace := dockerContainerdNamespace(h.rt.daemonInfo)

	if containerdStateDir != "" {
		bundleDir := containerdrt.ShimBundleDir(containerdStateDir, namespace, i.ID)
		profile.OCI.BundleDir = bundleDir
		profile.OCI.ConfigPath = bundleDir + "/config.json"
	}

	// Resolve the OCI runtime state directory.
	//
	// Docker does NOT use runc's CLI default (/run/runc). Instead it always
	// invokes the OCI runtime with an explicit --root pointing at
	//   <ExecRoot>/runtime-<runtimeName>
	// so the per-container state lives at
	//   <ExecRoot>/runtime-<runtimeName>/<namespace>/<id>
	// A non-default runtime may override this via daemon.json runtimeArgs
	// containing "--root <path>"; in that case the override wins.
	runtimeStateDir := dockerRuntimeStateDir(h.rt.daemonInfo, runtimeName, namespace, i.ID)
	profile.OCI.StateDir = runtimeStateDir
	profile.OCI.StatePath = runtimeStateDir + "/state.json"

	// -----------------------------------------------------------------------
	// Shim process detection via procfs
	// -----------------------------------------------------------------------

	pid := h.liveContainerPID()
	if pid > 0 && h.rt.procReader != nil {
		if shim := containerdrt.GetShimProcessInfo(h.rt.procReader, pid); shim != nil {
			profile.Shim.BinaryPath = shim.BinaryPath
			profile.Shim.Cmdline = append([]string(nil), shim.Cmdline...)

			if containerdStateDir != "" {
				bundleDir := containerdrt.ShimBundleDir(containerdStateDir, namespace, i.ID)
				profile.Shim.SocketAddress = containerdrt.ResolveShimSocketAddress(
					containerdStateDir, bundleDir, i.ID, "", namespace,
				)
			}
		}
	}

	// -----------------------------------------------------------------------
	// RootFS path
	// -----------------------------------------------------------------------

	if merged, ok := i.GraphDriver.Data["MergedDir"]; ok {
		profile.RootFSPath = merged
	}
	if profile.RootFSPath == "" {
		if live := h.resolveLiveRootPaths(); live != nil {
			profile.RootFSPath = live.rootfs
		}
	}

	// Trim empty Shim if nothing was populated.
	if profile.Shim.BinaryPath == "" && profile.Shim.SocketAddress == "" && len(profile.Shim.Cmdline) == 0 {
		profile.Shim = nil
	}

	return profile, nil
}

// dockerContainerdStateDir derives the containerd state directory from
// daemon info. For Docker's bundled containerd this is typically
// <ExecRoot>/containerd (e.g. /var/run/docker/containerd); when Docker is
// configured to use the system containerd it is /run/containerd.
func dockerContainerdStateDir(di *daemonInfo) string {
	if di == nil || di.ContainerdAddr == "" {
		return ""
	}
	return filepath.Dir(di.ContainerdAddr)
}

// dockerContainerdNamespace returns the containerd namespace Docker uses for
// containers (typically "moby").
func dockerContainerdNamespace(di *daemonInfo) string {
	if di != nil && di.ContainerdNS != nil {
		if ns, ok := di.ContainerdNS["containers"]; ok && ns != "" {
			return ns
		}
	}
	return "moby"
}

// dockerExecRoot infers Docker's --exec-root.
//
// The Engine's /info endpoint does not expose ExecRoot, so we reconstruct it
// from signals that are exposed:
//
//  1. Docker's bundled containerd places its socket at
//     <ExecRoot>/containerd/containerd.sock, and ExecRoot itself is
//     conventionally a "docker" directory (default /var/run/docker, or a
//     user-set path that still terminates in "docker"). When ContainerdAddr
//     matches that shape, the parent of its directory is ExecRoot.
//  2. Otherwise — including when Docker is configured to talk to the system
//     containerd at /run/containerd/containerd.sock — we fall back to
//     Docker's compiled-in default, /var/run/docker (which on systemd-based
//     distros resolves to /run/docker via the /var/run → /run symlink).
func dockerExecRoot(di *daemonInfo) string {
	if di != nil && di.ContainerdAddr != "" {
		sockDir := filepath.Dir(di.ContainerdAddr)
		if filepath.Base(sockDir) == "containerd" {
			parent := filepath.Dir(sockDir)
			if filepath.Base(parent) == "docker" {
				return parent
			}
		}
	}
	return "/var/run/docker"
}

// dockerRuntimeStateDir resolves the OCI runtime state directory for a single
// container managed by Docker. See call site for the layout rationale.
func dockerRuntimeStateDir(di *daemonInfo, runtimeName, namespace, id string) string {
	if root := dockerRuntimeArgsRoot(di, runtimeName); root != "" {
		return filepath.Join(root, namespace, id)
	}
	return filepath.Join(dockerExecRoot(di), "runtime-"+runtimeName, namespace, id)
}

// dockerRuntimeArgsRoot extracts an explicit "--root <path>" override from
// the runtimeArgs configured in daemon.json for the given runtime, if any.
func dockerRuntimeArgsRoot(di *daemonInfo, runtimeName string) string {
	if di == nil || runtimeName == "" {
		return ""
	}
	rt, ok := di.Runtimes[runtimeName]
	if !ok {
		return ""
	}
	args := rt.Args
	for i, a := range args {
		switch {
		case a == "--root" && i+1 < len(args):
			return args[i+1]
		case strings.HasPrefix(a, "--root="):
			return strings.TrimPrefix(a, "--root=")
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// runtime.Container — RWLayerStats
// ---------------------------------------------------------------------------

func (h *containerHandle) RWLayerStats(ctx context.Context) (runtime.ContainerRWLayerStats, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return runtime.ContainerRWLayerStats{}, h.inspectErr
	}

	gd := h.inspect.GraphDriver
	if upper, ok := gd.Data["UpperDir"]; ok && upper != "" {
		usage, inodes := runtime.DirDiskUsage(upper)
		return runtime.ContainerRWLayerStats{
			RWLayerUsage:  usage,
			RWLayerInodes: inodes,
		}, nil
	}
	if live := h.resolveLiveRootPaths(); live != nil && live.upperdir != "" {
		usage, inodes := runtime.DirDiskUsage(live.upperdir)
		return runtime.ContainerRWLayerStats{
			RWLayerUsage:  usage,
			RWLayerInodes: inodes,
		}, nil
	}

	return runtime.ContainerRWLayerStats{}, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Processes / ProcessStats
// ---------------------------------------------------------------------------

func (h *containerHandle) Processes(ctx context.Context) ([]*runtime.Process, error) {
	pid, err := h.containerPID(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.CollectProcesses(h.rt.processCollector, pid)
}

func (h *containerHandle) ProcessStats(ctx context.Context) (*runtime.ProcessStats, error) {
	pid, err := h.containerPID(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.CollectProcessStats(h.rt.processCollector, pid, h.cgroupPath(ctx))
}

func (h *containerHandle) GetProcessStats(ctx context.Context, pid string) (*runtime.ProcessStats, error) {
	containerPID, err := h.containerPID(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.CollectSingleProcessStats(h.rt.processCollector, containerPID, h.cgroupPath(ctx), pid)
}

// ---------------------------------------------------------------------------
// runtime.Container — Image
// ---------------------------------------------------------------------------

func (h *containerHandle) Image(ctx context.Context) (runtime.Image, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}
	imageRef := ""
	if h.rt.imageStoreMode == ImageStoreModeContainerd && h.inspect.Config != nil {
		imageRef = h.inspect.Config.Image
	}
	if imageRef == "" {
		imageRef = h.inspect.Image
	}
	if imageRef == "" && h.inspect.Config != nil {
		imageRef = h.inspect.Config.Image
	}
	if imageRef == "" {
		return nil, fmt.Errorf("container has no image reference")
	}
	return h.rt.GetImage(ctx, imageRef)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func (h *containerHandle) containerPID(ctx context.Context) (uint32, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return 0, h.inspectErr
	}
	if h.inspect.State == nil || h.inspect.State.Pid <= 0 {
		return 0, fmt.Errorf("container is not running")
	}
	return uint32(h.inspect.State.Pid), nil
}

func (h *containerHandle) cgroupPath(ctx context.Context) string {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return ""
	}

	var rawPath string
	var pid uint32
	if h.inspect != nil {
		if h.inspect.HostConfig != nil {
			rawPath = h.inspect.HostConfig.CgroupParent
		}
		if h.inspect.State != nil && h.inspect.State.Pid > 0 {
			pid = uint32(h.inspect.State.Pid)
		}
	}

	version := 0
	if h.rt.cgroupReader != nil {
		version = int(h.rt.cgroupReader.GetVersion())
	}
	return runtime.ResolveCGroupPath(rawPath, version, pid, h.rt.procReader)
}

type liveRootPaths struct {
	rootfs        string
	upperdir      string
	baseLayer     string
	rwSnapshotKey string
}

func (h *containerHandle) resolveLiveRootPaths() *liveRootPaths {
	pid := h.liveContainerPID()
	if pid == 0 || h.rt.mountReader == nil {
		return nil
	}

	mounts, err := h.rt.mountReader.ReadMounts(int(pid))
	if err != nil {
		return nil
	}
	rootMount := h.rt.mountReader.FindRootMount(mounts)
	if rootMount == nil {
		return nil
	}

	lowerdir, upperdir, _ := h.rt.mountReader.ParseOverlayFS(rootMount)
	paths := &liveRootPaths{}
	if upperdir != "" {
		paths.upperdir = upperdir
		paths.rootfs = upperdir
		paths.rwSnapshotKey = dockerSnapshotKeyFromPath(upperdir)
	} else {
		paths.rootfs = rootMount.Source
	}
	if lowerdir != "" {
		parts := strings.Split(lowerdir, ":")
		if len(parts) > 0 {
			paths.baseLayer = parts[len(parts)-1]
		}
	}
	if paths.rootfs == "" && paths.upperdir == "" && paths.baseLayer == "" {
		return nil
	}
	return paths
}

func dockerSnapshotKeyFromPath(path string) string {
	cleaned := filepath.Clean(path)
	parts := strings.Split(cleaned, string(filepath.Separator))
	for i, part := range parts {
		if part == "snapshots" && i+2 < len(parts) {
			next := parts[i+1]
			leaf := parts[i+2]
			if next != "" && (leaf == "fs" || leaf == "diff") {
				return next
			}
		}
	}
	return ""
}

func dockerRWIDFromPath(path string) string {
	if snapshotKey := dockerSnapshotKeyFromPath(path); snapshotKey != "" {
		return snapshotKey
	}
	cleaned := filepath.Clean(path)
	if filepath.Base(cleaned) == "diff" || filepath.Base(cleaned) == "fs" {
		return filepath.Base(filepath.Dir(cleaned))
	}
	return ""
}

func (h *containerHandle) liveContainerPID() uint32 {
	if h.inspect == nil || h.inspect.State == nil || h.inspect.State.Pid <= 0 {
		return 0
	}
	return uint32(h.inspect.State.Pid)
}

// dockerNamespaceMap extracts Linux namespace modes from Docker HostConfig.
func dockerNamespaceMap(i *dockertypes.ContainerJSON) map[string]string {
	if i.HostConfig == nil {
		return nil
	}
	ns := make(map[string]string)
	if m := string(i.HostConfig.PidMode); m != "" {
		ns["pid"] = m
	}
	if m := string(i.HostConfig.NetworkMode); m != "" {
		ns["network"] = m
	}
	if m := string(i.HostConfig.IpcMode); m != "" {
		ns["ipc"] = m
	}
	if m := string(i.HostConfig.UTSMode); m != "" {
		ns["uts"] = m
	}
	if m := string(i.HostConfig.UsernsMode); m != "" {
		ns["user"] = m
	}
	if m := string(i.HostConfig.CgroupnsMode); m != "" {
		ns["cgroup"] = m
	}
	if len(ns) == 0 {
		return nil
	}
	return ns
}

// listDockerContainers fetches all containers from the Docker daemon.
func (r *Runtime) listDockerContainers(ctx context.Context) ([]dockertypes.Container, error) {
	return r.dockerClient.ContainerList(ctx, containertypes.ListOptions{All: true})
}

// ---------------------------------------------------------------------------
// runtime.Container — Stdio
// ---------------------------------------------------------------------------

func (h *containerHandle) Stdio(ctx context.Context) (*runtime.ContainerStdio, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}

	i := h.inspect
	s := &runtime.ContainerStdio{}

	// Declarative config from Docker inspect.
	if i.Config != nil {
		s.TTY = runtime.BoolPtr(i.Config.Tty)
		s.OpenStdin = runtime.BoolPtr(i.Config.OpenStdin)
		s.StdinOnce = runtime.BoolPtr(i.Config.StdinOnce)
		s.Docker = &runtime.DockerStdioInfo{
			AttachStdin:  runtime.BoolPtr(i.Config.AttachStdin),
			AttachStdout: runtime.BoolPtr(i.Config.AttachStdout),
			AttachStderr: runtime.BoolPtr(i.Config.AttachStderr),
		}
	}

	// Log path.
	s.LogPath = i.LogPath

	// Infer log path from daemon root when driver is json-file and LogPath is empty.
	if s.LogPath == "" && i.HostConfig != nil && i.HostConfig.LogConfig.Type == "json-file" &&
		h.rt.daemonInfo != nil && h.rt.daemonInfo.DockerRootDir != "" {
		s.LogPath = filepath.Join(h.rt.daemonInfo.DockerRootDir, "containers", h.id, h.id+"-json.log")
	}

	// Proc FD fallback: resolve actual open files on stdin/stdout/stderr when paths are unknown.
	if h.rt.procReader != nil && i.State != nil && i.State.Pid > 0 {
		fd0, fd1, fd2 := h.rt.procReader.ReadStdioFDs(i.State.Pid)
		if s.Stdin == nil && fd0 != nil {
			target := fd0.Target
			s.Stdin = &target
		}
		if s.Stdout == nil && fd1 != nil {
			target := fd1.Target
			s.Stdout = &target
		}
		if s.Stderr == nil && fd2 != nil {
			target := fd2.Target
			s.Stderr = &target
		}
	}

	return s, nil
}

// Compile-time interface check.
var _ runtime.Container = (*containerHandle)(nil)
