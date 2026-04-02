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
func (h *containerHandle) CRIInfo()   {}
func (h *containerHandle) OCISepc()   {}

// ---------------------------------------------------------------------------
// runtime.Container — Info
// ---------------------------------------------------------------------------

func (h *containerHandle) Info(ctx context.Context) (*runtime.ContainerInfo, error) {
	// Fast path: use summary if available (avoids inspect for list views).
	if h.summary != nil {
		return h.infoFromSummary(), nil
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
		Image:     s.Image,
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
		ID:    i.ID,
		Name:  strings.TrimPrefix(i.Name, "/"),
		Image: i.Config.Image,
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
		cfg.Environment = parseDockerEnv(i.Config.Env)
	}

	// Image.
	if i.Config != nil {
		cfg.ImageName = i.Config.Image
	}

	// CGroup path.
	if i.HostConfig != nil && i.HostConfig.CgroupParent != "" {
		cfg.CGroupPath = i.HostConfig.CgroupParent
		cfg.CGroupDriver = inferDockerCGroupDriver(i.HostConfig.CgroupParent)
	}

	// CGroup version from reader.
	if h.rt.cgroupReader != nil {
		cfg.CGroupVersion = int(h.rt.cgroupReader.GetVersion())
	}

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
			podNet.ObservedInterfaces = convertNetworkStats(stats)
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

	profile := &runtime.RuntimeProfile{
		OCI: &runtime.OCIInfo{},
	}

	// The Docker daemon uses a runtime (typically runc) under the hood.
	if hc := h.inspect.HostConfig; hc != nil && hc.Runtime != "" {
		profile.OCI.RuntimeName = hc.Runtime
	} else {
		profile.OCI.RuntimeName = "runc"
	}

	// RootFS path from GraphDriver merged dir.
	if merged, ok := h.inspect.GraphDriver.Data["MergedDir"]; ok {
		profile.RootFSPath = merged
	}
	if profile.RootFSPath == "" {
		if live := h.resolveLiveRootPaths(); live != nil {
			profile.RootFSPath = live.rootfs
		}
	}

	return profile, nil
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
		usage, inodes := dirDiskUsage(upper)
		return runtime.ContainerRWLayerStats{
			RWLayerUsage:  usage,
			RWLayerInodes: inodes,
		}, nil
	}
	if live := h.resolveLiveRootPaths(); live != nil && live.upperdir != "" {
		usage, inodes := dirDiskUsage(live.upperdir)
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
	return runtime.CollectProcessStats(h.rt.processCollector, pid, h.cgroupPath())
}

func (h *containerHandle) GetProcessStats(ctx context.Context, pid string) (*runtime.ProcessStats, error) {
	containerPID, err := h.containerPID(ctx)
	if err != nil {
		return nil, err
	}
	return runtime.CollectSingleProcessStats(h.rt.processCollector, containerPID, h.cgroupPath(), pid)
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

func (h *containerHandle) cgroupPath() string {
	if h.inspect != nil && h.inspect.HostConfig != nil {
		return h.inspect.HostConfig.CgroupParent
	}
	return ""
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

// listDockerContainers fetches all containers from the Docker daemon.
func (r *Runtime) listDockerContainers(ctx context.Context) ([]dockertypes.Container, error) {
	return r.dockerClient.ContainerList(ctx, containertypes.ListOptions{All: true})
}

// Compile-time interface check.
var _ runtime.Container = (*containerHandle)(nil)
