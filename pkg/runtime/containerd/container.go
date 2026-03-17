package containerd

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/typeurl/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// containerHandle implements runtime.Container.
//
// Immutable data loaded from containerd Info, OCI spec and CRI status is
// cached behind sync.Once so that repeated calls to Config, Storage, Network
// etc. never re-fetch data that cannot change for the lifetime of a container.
type containerHandle struct {
	rt  *Runtime
	raw client.Container
	id  string

	// --- cached immutable data (populated via sync.Once) ---

	infoOnce sync.Once
	info     containers.Container
	infoErr  error

	specOnce sync.Once
	spec     *runtimespec.Spec
	specErr  error

	criStatusOnce sync.Once
	criStatus     *cri.ContainerStatus

	criMountsOnce sync.Once
	criMounts     *cri.ContainerMountSet
}

func (r *Runtime) newContainerHandle(ctx context.Context, c client.Container) (*containerHandle, error) {
	h := &containerHandle{
		rt:  r,
		raw: c,
		id:  c.ID(),
	}
	// Eagerly load lightweight containerd info so that Info() never fails
	// for basic identity fields.
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return nil, h.infoErr
	}
	return h, nil
}

// ---------------------------------------------------------------------------
// Cache loaders
// ---------------------------------------------------------------------------

func (h *containerHandle) ensureInfo(ctx context.Context) {
	h.infoOnce.Do(func() {
		h.info, h.infoErr = h.raw.Info(ctx)
	})
}

func (h *containerHandle) ensureSpec(ctx context.Context) {
	h.specOnce.Do(func() {
		h.spec, h.specErr = h.raw.Spec(ctx)
	})
}

func (h *containerHandle) ensureCRIStatus(ctx context.Context) {
	h.criStatusOnce.Do(func() {
		if h.rt.criClient == nil {
			return
		}
		h.criStatus, _ = h.rt.criClient.InspectContainerStatus(ctx, h.id)
	})
}

func (h *containerHandle) ensureCRIMounts(ctx context.Context) {
	h.criMountsOnce.Do(func() {
		if h.rt.criClient == nil {
			return
		}
		h.criMounts, _ = h.rt.criClient.InspectContainerMounts(ctx, h.id)
	})
}

// ---------------------------------------------------------------------------
// runtime.Container identity
// ---------------------------------------------------------------------------

func (h *containerHandle) ID() string { return h.id }

// CRIInfo triggers eager caching of CRI container status.
func (h *containerHandle) CRIInfo() {}

// OCISepc triggers eager caching of the OCI spec.
func (h *containerHandle) OCISepc() {}

// ---------------------------------------------------------------------------
// runtime.Container — Info
// ---------------------------------------------------------------------------

func (h *containerHandle) Info(ctx context.Context) (*runtime.ContainerInfo, error) {
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return nil, h.infoErr
	}
	info := h.info

	ci := &runtime.ContainerInfo{
		ID:           info.ID,
		Name:         containerName(info),
		Image:        info.Image,
		PodName:      info.Labels["io.kubernetes.pod.name"],
		PodNamespace: info.Labels["io.kubernetes.pod.namespace"],
		PodUID:       info.Labels["io.kubernetes.pod.uid"],
		CreatedAt:    info.CreatedAt,
	}

	// Populate live PID and status from task.
	task, err := h.raw.Task(ctx, nil)
	if err != nil {
		ci.Status = runtime.ContainerStatusStopped
	} else {
		ci.PID = task.Pid()
		status, err := task.Status(ctx)
		if err != nil {
			ci.Status = runtime.ContainerStatusUnknown
		} else {
			ci.Status = convertStatus(string(status.Status))
		}
	}

	return ci, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Config (cached-friendly, immutable)
// ---------------------------------------------------------------------------

func (h *containerHandle) Config(ctx context.Context) (*runtime.ContainerConfig, error) {
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return nil, h.infoErr
	}
	h.ensureSpec(ctx)
	if h.specErr != nil {
		return nil, h.specErr
	}
	h.ensureCRIStatus(ctx)

	info := h.info
	spec := h.spec

	cfg := &runtime.ContainerConfig{
		ImageName:   info.Image,
		Snapshotter: info.Snapshotter,
		SnapshotKey: info.SnapshotKey,
		Namespaces:  buildNamespaceMap(spec),
	}

	if spec.Linux != nil && spec.Linux.CgroupsPath != "" {
		cfg.CGroupPath = spec.Linux.CgroupsPath
		cfg.CGroupDriver = inferCGroupDriver(spec.Linux.CgroupsPath)
	}

	cfg.Environment = buildEnvironment(spec, h.criStatus)

	// CGroup version from reader.
	if cfg.CGroupPath != "" && h.rt.cgroupReader != nil {
		cfg.CGroupVersion = int(h.rt.cgroupReader.GetVersion())
		cfg.CGroupMountedPath = "/sys/fs/cgroup" + "/" + strings.TrimPrefix(cfg.CGroupPath, "/")
	}

	// Snapshot layer paths.
	if info.SnapshotKey != "" {
		snapshotter := h.rt.client.SnapshotService(info.Snapshotter)
		if snapshotter != nil {
			if path, err := rwLayerPathFromMounts(ctx, snapshotter, info.SnapshotKey); err == nil {
				cfg.WritableLayerPath = path
			}
			if paths := readOnlyLayerPathsFromMounts(ctx, snapshotter, info.SnapshotKey); len(paths) > 0 {
				cfg.ReadOnlyLayerPath = paths[len(paths)-1] // base layer
			}
		}
	}

	return cfg, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — State (live)
// ---------------------------------------------------------------------------

func (h *containerHandle) State(ctx context.Context) (*runtime.ContainerState, error) {
	h.ensureCRIStatus(ctx)

	state := &runtime.ContainerState{}

	task, err := h.raw.Task(ctx, nil)
	if err != nil {
		state.Status = runtime.ContainerStatusStopped
	} else {
		state.PID = task.Pid()
		if status, err := task.Status(ctx); err == nil {
			state.Status = convertStatus(string(status.Status))
		} else {
			state.Status = runtime.ContainerStatusUnknown
		}

		// Process count.
		if h.rt.processCollector != nil {
			if procs, err := h.rt.processCollector.CollectContainerProcesses(task.Pid()); err == nil {
				state.ProcessCount = len(procs)
			}
		}

		// PPID.
		if h.rt.procReader != nil {
			if ppid, err := h.rt.procReader.GetProcessPPID(int(task.Pid())); err == nil {
				state.PPID = uint32(ppid)
			}
		}
	}

	// CRI-sourced lifecycle fields.
	if cri := h.criStatus; cri != nil {
		if !cri.StartedAt.IsZero() {
			state.StartedAt = cri.StartedAt
		}
		if !cri.FinishedAt.IsZero() {
			state.ExitedAt = cri.FinishedAt
		}
		state.ExitCode = cri.ExitCode
		state.ExitReason = cri.Reason
		state.RestartCount = cri.RestartCount
	}

	return state, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Network (live)
// ---------------------------------------------------------------------------

func (h *containerHandle) Network(ctx context.Context) (*runtime.ContainerNetworkState, error) {
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return nil, h.infoErr
	}
	h.ensureSpec(ctx)

	info := h.info
	podNet := h.buildPodNetwork(ctx, info)
	if podNet == nil {
		return nil, nil
	}
	return &runtime.ContainerNetworkState{PodNetwork: podNet}, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Storage (cached read-only layers)
// ---------------------------------------------------------------------------

func (h *containerHandle) Storage(ctx context.Context) (*runtime.ContainerStorage, error) {
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return nil, h.infoErr
	}
	info := h.info
	if info.SnapshotKey == "" {
		return nil, nil
	}

	snapshotter := h.rt.client.SnapshotService(info.Snapshotter)
	if snapshotter == nil {
		return nil, nil
	}

	storage := &runtime.ContainerStorage{}

	// RW layer path.
	if path, err := rwLayerPathFromMounts(ctx, snapshotter, info.SnapshotKey); err == nil {
		storage.RWLayerPath = path
	}

	// Build read-only layers by resolving image.
	img, err := h.Image(ctx)
	if err == nil {
		layers, err := img.Layers(ctx, runtime.LayerQuery{
			Snapshotter:   info.Snapshotter,
			RWSnapshotKey: info.SnapshotKey,
		})
		if err == nil {
			storage.ReadOnlyLayers = layers
		}
	}

	return storage, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Mounts (live + CRI + spec merge)
// ---------------------------------------------------------------------------

func (h *containerHandle) Mounts(ctx context.Context) ([]*runtime.Mount, error) {
	h.ensureSpec(ctx)
	if h.specErr != nil {
		return nil, h.specErr
	}
	h.ensureCRIMounts(ctx)

	var pid uint32
	if task, err := h.raw.Task(ctx, nil); err == nil {
		pid = task.Pid()
	}

	mounts, _ := h.rt.resolveContainerMounts(ctx, h.id, h.spec, pid, h.criMounts)
	return mounts, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Runtime
// ---------------------------------------------------------------------------

func (h *containerHandle) Runtime(ctx context.Context) (*runtime.RuntimeProfile, error) {
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return nil, h.infoErr
	}
	h.ensureSpec(ctx)

	info := h.info
	profile := &runtime.RuntimeProfile{
		OCI:  &runtime.OCIInfo{},
		Shim: &runtime.ContainerdShimInfo{},
	}

	namespace := h.rt.config.Namespace
	bundleDir := runtimeV2BundleDir(namespace, info.ID)
	stateDir, _ := resolveOCIStateDir(info, namespace)

	profile.OCI.RuntimeName = info.Runtime.Name
	profile.OCI.StateDir = stateDir
	profile.OCI.BundleDir = bundleDir
	profile.OCI.SandboxID = info.SandboxID

	if runtimeBinary := resolveRuntimeBinary(info.Runtime); runtimeBinary != "" {
		profile.OCI.RuntimeBinary = runtimeBinary
	}
	if configPath := existingPath(bundleDir + "/config.json"); configPath != "" {
		profile.OCI.ConfigPath = configPath
	}

	// Shim process.
	var pid uint32
	if task, err := h.raw.Task(ctx, nil); err == nil {
		pid = task.Pid()
	}
	if pid > 0 && h.rt.procReader != nil {
		if shim := getShimProcessInfo(h.rt.procReader, pid); shim != nil {
			profile.Shim.BinaryPath = shim.binaryPath
			profile.Shim.Cmdline = append([]string(nil), shim.cmdline...)
			profile.Shim.SocketAddress = resolveShimSocketAddress(bundleDir, info.ID, info.SandboxID, namespace)
			profile.Shim.SandboxBundleDir = resolveShimSandboxBundleDir(bundleDir, info.SandboxID)
		}
	}

	// RootFS path from live mounts.
	if pid > 0 && h.rt.mountReader != nil {
		if rootFS := h.rt.resolveRootFSPath(pid); rootFS != "" {
			profile.RootFSPath = rootFS
		}
	}

	return profile, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — RWLayerStats (live)
// ---------------------------------------------------------------------------

func (h *containerHandle) RWLayerStats(ctx context.Context) (runtime.ContainerRWLayerStats, error) {
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return runtime.ContainerRWLayerStats{}, h.infoErr
	}
	info := h.info
	if info.SnapshotKey == "" {
		return runtime.ContainerRWLayerStats{}, nil
	}
	snapshotter := h.rt.client.SnapshotService(info.Snapshotter)
	if snapshotter == nil {
		return runtime.ContainerRWLayerStats{}, nil
	}
	usage, err := snapshotter.Usage(ctx, info.SnapshotKey)
	if err != nil {
		return runtime.ContainerRWLayerStats{}, err
	}
	return runtime.ContainerRWLayerStats{
		RWLayerUsage:  usage.Size,
		RWLayerInodes: usage.Inodes,
	}, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Processes / ProcessStats (live)
// ---------------------------------------------------------------------------

func (h *containerHandle) Processes(ctx context.Context) ([]*runtime.Process, error) {
	task, err := h.raw.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("container is not running")
	}
	if h.rt.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}
	procs, err := h.rt.processCollector.CollectContainerProcesses(task.Pid())
	if err != nil {
		return nil, err
	}
	return convertProcesses(procs), nil
}

func (h *containerHandle) ProcessStats(ctx context.Context) (*runtime.ProcessStats, error) {
	task, err := h.raw.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("container is not running")
	}
	if h.rt.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}

	h.ensureSpec(ctx)
	var cgroupPath string
	if h.spec != nil && h.spec.Linux != nil {
		cgroupPath = h.spec.Linux.CgroupsPath
	}

	top, err := h.rt.processCollector.CollectProcessTop(task.Pid(), cgroupPath)
	if err != nil {
		return nil, err
	}
	if len(top.Processes) == 0 {
		return nil, nil
	}
	return convertProcessStats(top.Processes[0]), nil
}

func (h *containerHandle) GetProcessStats(ctx context.Context, pid string) (*runtime.ProcessStats, error) {
	task, err := h.raw.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("container is not running")
	}
	if h.rt.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}

	h.ensureSpec(ctx)
	var cgroupPath string
	if h.spec != nil && h.spec.Linux != nil {
		cgroupPath = h.spec.Linux.CgroupsPath
	}

	targetPID, err := strconv.Atoi(pid)
	if err != nil || targetPID <= 0 {
		return nil, fmt.Errorf("invalid process pid %s", pid)
	}

	top, err := h.rt.processCollector.CollectProcessTop(task.Pid(), cgroupPath, targetPID)
	if err != nil {
		return nil, err
	}
	if len(top.Processes) > 0 {
		return convertProcessStats(top.Processes[0]), nil
	}
	return nil, fmt.Errorf("process %s not found", pid)
}

// ---------------------------------------------------------------------------
// runtime.Container — Image
// ---------------------------------------------------------------------------

func (h *containerHandle) Image(ctx context.Context) (runtime.Image, error) {
	h.ensureInfo(ctx)
	if h.infoErr != nil {
		return nil, h.infoErr
	}
	if h.info.Image == "" {
		return nil, fmt.Errorf("container has no image reference")
	}
	return h.rt.GetImage(ctx, h.info.Image)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func containerName(info containers.Container) string {
	if name, ok := info.Labels["io.kubernetes.container.name"]; ok {
		return name
	}
	if name, ok := info.Labels["name"]; ok {
		return name
	}
	if len(info.ID) >= 12 {
		return info.ID[:12]
	}
	return info.ID
}

func convertStatus(status string) runtime.ContainerStatus {
	switch status {
	case "created":
		return runtime.ContainerStatusCreated
	case "running":
		return runtime.ContainerStatusRunning
	case "paused":
		return runtime.ContainerStatusPaused
	case "stopped":
		return runtime.ContainerStatusStopped
	default:
		return runtime.ContainerStatusUnknown
	}
}

func buildNamespaceMap(spec *runtimespec.Spec) map[string]string {
	if spec == nil || spec.Linux == nil || len(spec.Linux.Namespaces) == 0 {
		return nil
	}
	m := make(map[string]string, len(spec.Linux.Namespaces))
	for _, ns := range spec.Linux.Namespaces {
		m[string(ns.Type)] = ns.Path
	}
	return m
}

func buildEnvironment(spec *runtimespec.Spec, criStatus *cri.ContainerStatus) []runtime.EnvVar {
	if spec != nil && spec.Process != nil && len(spec.Process.Env) > 0 {
		return parseSpecEnv(spec.Process.Env)
	}
	if criStatus != nil && len(criStatus.Envs) > 0 {
		envs := make([]runtime.EnvVar, 0, len(criStatus.Envs))
		for _, e := range criStatus.Envs {
			envs = append(envs, runtime.EnvVar{
				Key:          e.Key,
				Value:        e.Value,
				IsKubernetes: isKubernetesEnvKey(e.Key),
			})
		}
		return envs
	}
	return nil
}

func parseSpecEnv(envEntries []string) []runtime.EnvVar {
	envs := make([]runtime.EnvVar, 0, len(envEntries))
	for _, entry := range envEntries {
		key, value, found := strings.Cut(entry, "=")
		if !found || key == "" {
			continue
		}
		envs = append(envs, runtime.EnvVar{
			Key:          key,
			Value:        value,
			IsKubernetes: isKubernetesEnvKey(key),
		})
	}
	return envs
}

func isKubernetesEnvKey(key string) bool {
	return strings.HasPrefix(key, "KUBERNETES_") ||
		strings.HasPrefix(key, "POD_") ||
		strings.HasPrefix(key, "SERVICE_")
}

func inferCGroupDriver(path string) string {
	if strings.Contains(path, ".slice") || strings.Contains(path, ":cri-containerd:") {
		return "systemd"
	}
	return "cgroupfs"
}

// ---------------------------------------------------------------------------
// Snapshot helpers
// ---------------------------------------------------------------------------

func rwLayerPathFromMounts(ctx context.Context, snapshotter snapshots.Snapshotter, key string) (string, error) {
	mounts, err := snapshotter.Mounts(ctx, key)
	if err != nil {
		return "", err
	}
	for _, m := range mounts {
		if m.Type == "overlay" {
			for _, opt := range m.Options {
				if len(opt) > 9 && opt[:9] == "upperdir=" {
					return opt[9:], nil
				}
			}
		}
	}
	return "", fmt.Errorf("no upperdir found in mounts")
}

func readOnlyLayerPathsFromMounts(ctx context.Context, snapshotter snapshots.Snapshotter, rwKey string) []string {
	mounts, err := snapshotter.Mounts(ctx, rwKey)
	if err != nil {
		return nil
	}
	for _, m := range mounts {
		if m.Type == "overlay" {
			for _, opt := range m.Options {
				if len(opt) > 9 && opt[:9] == "lowerdir=" {
					return strings.Split(opt[9:], ":")
				}
			}
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// OCI runtime helpers
// ---------------------------------------------------------------------------

const runtimeV2StateBase = "/run/containerd/io.containerd.runtime.v2.task"
const defaultRuncRoot = "/run/containerd/runc"

func runtimeV2BundleDir(namespace, containerID string) string {
	return runtimeV2StateBase + "/" + namespace + "/" + containerID
}

func resolveOCIStateDir(info containers.Container, namespace string) (string, string) {
	if root, source := resolveRuncRoot(info.Runtime); root != "" {
		return root + "/" + namespace + "/" + info.ID, source
	}
	return runtimeV2BundleDir(namespace, info.ID), "convention"
}

func resolveRuncRoot(runtimeInfo containers.RuntimeInfo) (string, string) {
	if opts := resolveRuncOptions(runtimeInfo); opts != nil {
		if opts.Root != "" {
			return opts.Root, "runtime-options"
		}
		if isRuncRuntime(runtimeInfo.Name) {
			return defaultRuncRoot, "runtime-default"
		}
	}
	if isRuncRuntime(runtimeInfo.Name) {
		return defaultRuncRoot, "runtime-default"
	}
	return "", ""
}

func resolveRuncOptions(runtimeInfo containers.RuntimeInfo) *runcoptions.Options {
	if runtimeInfo.Options == nil {
		return nil
	}
	var opts runcoptions.Options
	if err := typeurl.UnmarshalTo(runtimeInfo.Options, &opts); err != nil {
		return nil
	}
	return &opts
}

func resolveRuntimeBinary(runtimeInfo containers.RuntimeInfo) string {
	if opts := resolveRuncOptions(runtimeInfo); opts != nil && opts.BinaryName != "" {
		return opts.BinaryName
	}
	name := runtimeInfo.Name
	if strings.HasPrefix(name, "/") {
		return name
	}
	parts := strings.Split(name, ".")
	if len(parts) >= 2 {
		return "containerd-shim-" + parts[len(parts)-2] + "-" + parts[len(parts)-1]
	}
	return name
}

func isRuncRuntime(name string) bool {
	return strings.Contains(name, ".runc.") || strings.HasSuffix(name, "runc")
}

func existingPath(path string) string {
	// We import os lazily here through the helper.
	return existingPathCheck(path)
}

// Compile-time interface check.
var _ runtime.Container = (*containerHandle)(nil)
