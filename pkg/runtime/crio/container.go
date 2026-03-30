package crio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	cstorage "go.podman.io/storage"
)

// criContainerSupplement holds CRI-sourced data that supplements a storage container.
type criContainerSupplement struct {
	podSandboxID string
	image        string
	imageRef     string
	name         string
	labels       map[string]string
	annotations  map[string]string
}

// containerHandle implements runtime.Container for CRI-O.
//
// Primary identity and storage data comes from containers/storage.
// Runtime state (PID, status, labels, pod association) is supplemented by CRI.
type containerHandle struct {
	rt *Runtime
	id string

	// from containers/storage (primary)
	names     []string
	imageID   string
	layerID   string
	createdAt time.Time

	// from CRI (supplementary, may be nil)
	cri *criContainerSupplement

	// lazy-loaded CRI detailed status
	statusOnce sync.Once

	// lazy-loaded CRI mounts
	mountsOnce sync.Once
	mountSet   *cri.ContainerMountSet

	// lazy-loaded OCI spec
	specOnce sync.Once
	spec     *runtimespec.Spec
	specErr  error
}

func (r *Runtime) newContainerHandle(ctr *cstorage.Container, supplement *criContainerSupplement) *containerHandle {
	return &containerHandle{
		rt:        r,
		id:        ctr.ID,
		names:     append([]string(nil), ctr.Names...),
		imageID:   ctr.ImageID,
		layerID:   ctr.LayerID,
		createdAt: ctr.Created,
		cri:       supplement,
	}
}

// ---------------------------------------------------------------------------
// Cache loaders
// ---------------------------------------------------------------------------

func (h *containerHandle) ensureCRIMounts(ctx context.Context) {
	h.mountsOnce.Do(func() {
		h.mountSet, _ = h.rt.criClient.InspectContainerMounts(ctx, h.id)
	})
}

func (h *containerHandle) ensureSpec(_ context.Context) {
	h.specOnce.Do(func() {
		h.spec, h.specErr = h.readOCISpec()
	})
}

// readOCISpec reads the OCI runtime spec from CRI-O's bundle directory.
func (h *containerHandle) readOCISpec() (*runtimespec.Spec, error) {
	bundleDir := crioContainerBundleDir(h.rt.storageRunRoot, h.id)
	specPath := bundleDir + "/config.json"
	data, err := os.ReadFile(specPath)
	if err != nil {
		return nil, fmt.Errorf("read spec %s: %w", specPath, err)
	}
	var spec runtimespec.Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse spec: %w", err)
	}
	return &spec, nil
}

// pid returns the container's init PID, preferring CRI verbose info.
func (h *containerHandle) pid(ctx context.Context) uint32 {
	status, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id)
	if status != nil && status.PID > 0 {
		return status.PID
	}
	return 0
}

// containerName derives a display name from CRI labels or storage names.
func (h *containerHandle) containerName() string {
	if h.cri != nil {
		if k8sName, ok := h.cri.labels["io.kubernetes.container.name"]; ok {
			return k8sName
		}
		if h.cri.name != "" {
			return h.cri.name
		}
	}
	if len(h.names) > 0 {
		return h.names[0]
	}
	if len(h.id) >= 12 {
		return h.id[:12]
	}
	return h.id
}

// resolveRWLayerPath returns the RW layer filesystem path from storage metadata.
func (h *containerHandle) resolveRWLayerPath() string {
	if h.layerID == "" {
		return ""
	}
	store, err := h.rt.getStore()
	if err != nil {
		return ""
	}
	layer, err := store.Layer(h.layerID)
	if err != nil {
		return ""
	}
	if driver, err := store.GraphDriver(); err == nil {
		if meta, err := driver.Metadata(layer.ID); err == nil {
			if path := bestPathFromDriverMetadata(meta, store.GraphRoot(), store.GraphDriverName(), layer.ID); path != "" {
				return path
			}
		}
	}
	if store.GraphDriverName() == defaultGraphDriver {
		return filepath.Join(store.GraphRoot(), store.GraphDriverName(), layer.ID, "diff")
	}
	return ""
}

// ---------------------------------------------------------------------------
// runtime.Container identity
// ---------------------------------------------------------------------------

func (h *containerHandle) ID() string { return h.id }
func (h *containerHandle) CRIInfo()   {}
func (h *containerHandle) OCISepc()   {}

// ---------------------------------------------------------------------------
// runtime.Container — Info
// ---------------------------------------------------------------------------

func (h *containerHandle) Info(ctx context.Context) (*runtime.ContainerInfo, error) {
	info := &runtime.ContainerInfo{
		ID:        h.id,
		Name:      h.containerName(),
		Image:     h.imageID,
		CreatedAt: h.createdAt,
		PID:       h.pid(ctx),
	}

	if h.cri != nil {
		if h.cri.image != "" {
			info.Image = h.cri.image
		}
		status, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id)
		if status != nil {
			info.Status = convertStatus(status.Status)
		}
		info.PodName = h.cri.labels["io.kubernetes.pod.name"]
		info.PodNamespace = h.cri.labels["io.kubernetes.pod.namespace"]
		info.PodUID = h.cri.labels["io.kubernetes.pod.uid"]
	}

	return info, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Config
// ---------------------------------------------------------------------------

func (h *containerHandle) Config(ctx context.Context) (*runtime.ContainerConfig, error) {
	h.ensureSpec(ctx)

	cfg := &runtime.ContainerConfig{}

	if h.cri != nil && h.cri.image != "" {
		cfg.ImageName = h.cri.image
	} else {
		cfg.ImageName = h.imageID
	}

	if h.spec != nil {
		cfg.Namespaces = buildNamespaceMap(h.spec)
		if h.spec.Linux != nil && h.spec.Linux.CgroupsPath != "" {
			cfg.CGroupPath = h.spec.Linux.CgroupsPath
			cfg.CGroupDriver = inferCGroupDriver(h.spec.Linux.CgroupsPath)
		}
	}

	status, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id)
	cfg.Environment = buildEnvironment(h.spec, status)

	if cfg.CGroupPath != "" && h.rt.cgroupReader != nil {
		cfg.CGroupVersion = int(h.rt.cgroupReader.GetVersion())
		cfg.CGroupMountedPath = "/sys/fs/cgroup" + "/" + strings.TrimPrefix(cfg.CGroupPath, "/")
	}

	// RW layer path from storage.
	cfg.WritableLayerPath = h.resolveRWLayerPath()
	return cfg, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — State
// ---------------------------------------------------------------------------

func (h *containerHandle) State(ctx context.Context) (*runtime.ContainerState, error) {
	pid := h.pid(ctx)

	state := &runtime.ContainerState{
		PID: pid,
	}

	if h.cri != nil {
		status, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id)
		state.Status = convertStatus(status.Status)
	} else {
		state.Status = runtime.ContainerStatusUnknown
	}

	if pid > 0 {
		if h.rt.processCollector != nil {
			if procs, err := h.rt.processCollector.CollectContainerProcesses(pid); err == nil {
				state.ProcessCount = len(procs)
			}
		}
		if h.rt.procReader != nil {
			if ppid, err := h.rt.procReader.GetProcessPPID(int(pid)); err == nil {
				state.PPID = uint32(ppid)
			}
		}
	}

	if status, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id); status != nil {
		if !status.StartedAt.IsZero() {
			state.StartedAt = status.StartedAt
		}
		if !status.FinishedAt.IsZero() {
			state.ExitedAt = status.FinishedAt
		}
		state.ExitCode = status.ExitCode
		state.ExitReason = status.Reason
		state.RestartCount = status.RestartCount
	}

	return state, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Network
// ---------------------------------------------------------------------------

func (h *containerHandle) Network(ctx context.Context) (*runtime.ContainerNetworkState, error) {
	h.ensureSpec(ctx)
	podNet := h.buildPodNetwork(ctx)
	if podNet == nil {
		return nil, nil
	}
	return &runtime.ContainerNetworkState{PodNetwork: podNet}, nil
}

func (h *containerHandle) buildPodNetwork(ctx context.Context) *runtime.PodNetworkInfo {
	sandboxID := ""
	if h.cri != nil {
		sandboxID = h.cri.podSandboxID
	}

	podNet := &runtime.PodNetworkInfo{
		SandboxID: sandboxID,
	}

	if pid := h.pid(ctx); pid > 0 && h.rt.procReader != nil {
		if stats, err := h.rt.procReader.ReadNetDev(int(pid)); err == nil {
			podNet.ObservedInterfaces = convertNetworkStats(stats)
		}
	}

	// Netns from spec.
	if h.spec != nil {
		if path := nsPathFromSpec(h.spec, "network"); path != "" {
			podNet.NetNSPath = path
		}
	}

	if sandboxID == "" {
		podNet.Warnings = append(podNet.Warnings, "sandbox id unresolved")
		if cri.ShouldAttachPodNetwork(podNet) {
			return podNet
		}
		return nil
	}

	if h.rt.criClient == nil {
		podNet.Warnings = append(podNet.Warnings, "cri metadata client unavailable")
		if cri.ShouldAttachPodNetwork(podNet) {
			return podNet
		}
		return nil
	}

	if err := h.rt.criClient.ApplyPodSandboxNetwork(ctx, sandboxID, podNet); err != nil {
		podNet.Warnings = append(podNet.Warnings, fmt.Sprintf("cri pod sandbox status failed: %v", err))
		if cri.ShouldAttachPodNetwork(podNet) {
			return podNet
		}
		return nil
	}

	if cri.ShouldAttachPodNetwork(podNet) {
		return podNet
	}
	return nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Storage
// ---------------------------------------------------------------------------

func (h *containerHandle) Storage(ctx context.Context) (*runtime.ContainerStorage, error) {
	storage := &runtime.ContainerStorage{
		RWLayerPath: h.resolveRWLayerPath(),
	}

	// Read-only layers from storage metadata.
	img, err := h.Image(ctx)
	if err == nil {
		layers, err := img.Layers(ctx, runtime.LayerQuery{})
		if err == nil {
			storage.ReadOnlyLayers = layers
		}
	}

	return storage, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Mounts
// ---------------------------------------------------------------------------

func (h *containerHandle) Mounts(ctx context.Context) ([]*runtime.Mount, error) {
	h.ensureSpec(ctx)
	h.ensureCRIMounts(ctx)

	pid := h.pid(ctx)
	return resolveContainerMounts(h.rt, h.spec, pid, h.mountSet)
}

// ---------------------------------------------------------------------------
// runtime.Container — Runtime
// ---------------------------------------------------------------------------

func (h *containerHandle) Runtime(ctx context.Context) (*runtime.RuntimeProfile, error) {
	h.ensureSpec(ctx)
	pid := h.pid(ctx)

	profile := &runtime.RuntimeProfile{
		OCI:    &runtime.OCIInfo{},
		Conmon: &runtime.ConmonInfo{},
	}

	bundleDir := crioContainerBundleDir(h.rt.storageRunRoot, h.id)
	profile.OCI.BundleDir = bundleDir
	profile.OCI.RuntimeName = "cri-o"

	if h.cri != nil {
		profile.OCI.SandboxID = h.cri.podSandboxID
		// Detect OCI runtime binary from annotation.
		if rt, ok := h.cri.annotations["io.kubernetes.cri-o.RuntimeHandler"]; ok {
			profile.OCI.RuntimeName = rt
		}
		// Log path from annotations.
		if logPath, ok := h.cri.annotations["io.kubernetes.cri-o.LogPath"]; ok {
			profile.Conmon.LogPath = logPath
		}
	}

	if configPath := existingPath(bundleDir + "/config.json"); configPath != "" {
		profile.OCI.ConfigPath = configPath
	}

	// Conmon detection.
	if pid > 0 && h.rt.procReader != nil {
		if conmon := getConmonProcessInfo(h.rt.procReader, pid); conmon != nil {
			profile.Conmon.PID = conmon.pid
			profile.Conmon.BinaryPath = conmon.binaryPath
			profile.Conmon.Cmdline = append([]string(nil), conmon.cmdline...)
		}
	}

	// RootFS path from OCI spec root.
	if h.spec != nil && h.spec.Root != nil && h.spec.Root.Path != "" {
		profile.RootFSPath = resolveSpecRootPath(h.spec.Root.Path, bundleDir)
	}

	return profile, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — RWLayerStats
// ---------------------------------------------------------------------------

func (h *containerHandle) RWLayerStats(ctx context.Context) (runtime.ContainerRWLayerStats, error) {
	rwPath := h.resolveRWLayerPath()
	if rwPath == "" {
		// Fallback to live mounts.
		pid := h.pid(ctx)
		if pid == 0 || h.rt.mountReader == nil {
			return runtime.ContainerRWLayerStats{}, nil
		}
		mounts, err := h.rt.mountReader.ReadMounts(int(pid))
		if err != nil {
			return runtime.ContainerRWLayerStats{}, nil
		}
		rootMount := h.rt.mountReader.FindRootMount(mounts)
		if rootMount == nil {
			return runtime.ContainerRWLayerStats{}, nil
		}
		_, upperdir, _ := h.rt.mountReader.ParseOverlayFS(rootMount)
		if upperdir == "" {
			return runtime.ContainerRWLayerStats{}, nil
		}
		rwPath = upperdir
	}
	return dirUsage(rwPath), nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Processes / ProcessStats
// ---------------------------------------------------------------------------

func (h *containerHandle) Processes(ctx context.Context) ([]*runtime.Process, error) {
	pid := h.pid(ctx)
	if pid == 0 {
		return nil, fmt.Errorf("container is not running")
	}
	if h.rt.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}
	procs, err := h.rt.processCollector.CollectContainerProcesses(pid)
	if err != nil {
		return nil, err
	}
	return convertProcesses(procs), nil
}

func (h *containerHandle) ProcessStats(ctx context.Context) (*runtime.ProcessStats, error) {
	pid := h.pid(ctx)
	if pid == 0 {
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

	top, err := h.rt.processCollector.CollectProcessTop(pid, cgroupPath)
	if err != nil {
		return nil, err
	}
	if len(top.Processes) == 0 {
		return nil, nil
	}
	return convertProcessStats(top.Processes[0]), nil
}

func (h *containerHandle) GetProcessStats(ctx context.Context, pidStr string) (*runtime.ProcessStats, error) {
	pid := h.pid(ctx)
	if pid == 0 {
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

	targetPID, err := strconv.Atoi(pidStr)
	if err != nil || targetPID <= 0 {
		return nil, fmt.Errorf("invalid process pid %s", pidStr)
	}

	top, err := h.rt.processCollector.CollectProcessTop(pid, cgroupPath, targetPID)
	if err != nil {
		return nil, err
	}
	if len(top.Processes) > 0 {
		return convertProcessStats(top.Processes[0]), nil
	}
	return nil, fmt.Errorf("process %s not found", pidStr)
}

// ---------------------------------------------------------------------------
// runtime.Container — Image
// ---------------------------------------------------------------------------

func (h *containerHandle) Image(ctx context.Context) (runtime.Image, error) {
	// Primary: look up by storage imageID.
	if h.imageID != "" {
		return h.rt.GetImage(ctx, h.imageID)
	}
	// Fallback: CRI image reference.
	if h.cri != nil {
		ref := h.cri.image
		if ref == "" {
			ref = h.cri.imageRef
		}
		if ref != "" {
			return h.rt.GetImage(ctx, ref)
		}
	}
	return nil, fmt.Errorf("container has no image reference")
}

// Compile-time interface check.
var _ runtime.Container = (*containerHandle)(nil)
