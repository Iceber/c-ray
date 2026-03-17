package crio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// containerHandle implements runtime.Container for CRI-O.
//
// Basic listing data comes from the CRI ListContainers response.
// Rich data (PID, env, OCI spec) is lazy-loaded via CRI ContainerStatus
// (verbose) and filesystem access.
type containerHandle struct {
	rt *Runtime
	id string

	// from CRI ListContainers (available immediately)
	podSandboxID string
	name         string
	image        string
	imageRef     string
	state        string
	createdAt    time.Time
	labels       map[string]string
	annotations  map[string]string

	// lazy-loaded CRI detailed status
	statusOnce sync.Once
	status     *cri.ContainerStatus

	// lazy-loaded CRI mounts
	mountsOnce sync.Once
	mountSet   *cri.ContainerMountSet

	// lazy-loaded OCI spec
	specOnce sync.Once
	spec     *runtimespec.Spec
	specErr  error
}

func (r *Runtime) newContainerHandle(c *runtimeapi.Container) *containerHandle {
	h := &containerHandle{
		rt:           r,
		id:           c.GetId(),
		podSandboxID: c.GetPodSandboxId(),
		image:        c.GetImage().GetImage(),
		imageRef:     c.GetImageRef(),
		labels:       c.GetLabels(),
		annotations:  c.GetAnnotations(),
	}
	if meta := c.GetMetadata(); meta != nil {
		h.name = meta.GetName()
	}
	if created := c.GetCreatedAt(); created > 0 {
		h.createdAt = time.Unix(0, created)
	}
	switch c.GetState() {
	case runtimeapi.ContainerState_CONTAINER_CREATED:
		h.state = "created"
	case runtimeapi.ContainerState_CONTAINER_RUNNING:
		h.state = "running"
	case runtimeapi.ContainerState_CONTAINER_EXITED:
		h.state = "stopped"
	default:
		h.state = "unknown"
	}
	return h
}

// ---------------------------------------------------------------------------
// Cache loaders
// ---------------------------------------------------------------------------

func (h *containerHandle) ensureCRIStatus(ctx context.Context) {
	h.statusOnce.Do(func() {
		h.status, _ = h.rt.criClient.InspectContainerStatus(ctx, h.id)
	})
}

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
	h.ensureCRIStatus(ctx)
	if h.status != nil && h.status.PID > 0 {
		return h.status.PID
	}
	return 0
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
	return &runtime.ContainerInfo{
		ID:           h.id,
		Name:         containerName(h.name, h.labels, h.id),
		Image:        h.image,
		PodName:      h.labels["io.kubernetes.pod.name"],
		PodNamespace: h.labels["io.kubernetes.pod.namespace"],
		PodUID:       h.labels["io.kubernetes.pod.uid"],
		Status:       convertStatus(h.state),
		CreatedAt:    h.createdAt,
		PID:          h.pid(ctx),
	}, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Config
// ---------------------------------------------------------------------------

func (h *containerHandle) Config(ctx context.Context) (*runtime.ContainerConfig, error) {
	h.ensureSpec(ctx)
	h.ensureCRIStatus(ctx)

	cfg := &runtime.ContainerConfig{
		ImageName: h.image,
	}

	if h.spec != nil {
		cfg.Namespaces = buildNamespaceMap(h.spec)
		if h.spec.Linux != nil && h.spec.Linux.CgroupsPath != "" {
			cfg.CGroupPath = h.spec.Linux.CgroupsPath
			cfg.CGroupDriver = inferCGroupDriver(h.spec.Linux.CgroupsPath)
		}
	}

	cfg.Environment = buildEnvironment(h.spec, h.status)

	if cfg.CGroupPath != "" && h.rt.cgroupReader != nil {
		cfg.CGroupVersion = int(h.rt.cgroupReader.GetVersion())
		cfg.CGroupMountedPath = "/sys/fs/cgroup" + "/" + strings.TrimPrefix(cfg.CGroupPath, "/")
	}

	// RW layer path from live mounts.
	if pid := h.pid(ctx); pid > 0 && h.rt.mountReader != nil {
		if rootFS := resolveRootFSPath(h.rt, pid); rootFS != "" {
			cfg.WritableLayerPath = rootFS
		}
	}

	return cfg, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — State
// ---------------------------------------------------------------------------

func (h *containerHandle) State(ctx context.Context) (*runtime.ContainerState, error) {
	h.ensureCRIStatus(ctx)
	pid := h.pid(ctx)

	state := &runtime.ContainerState{
		Status: convertStatus(h.state),
		PID:    pid,
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

	if cri := h.status; cri != nil {
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
	podNet := &runtime.PodNetworkInfo{
		SandboxID: h.podSandboxID,
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

	if h.podSandboxID == "" {
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

	if err := h.rt.criClient.ApplyPodSandboxNetwork(ctx, h.podSandboxID, podNet); err != nil {
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
	storage := &runtime.ContainerStorage{}

	// RW layer from live mounts.
	if pid := h.pid(ctx); pid > 0 && h.rt.mountReader != nil {
		mounts, err := h.rt.mountReader.ReadMounts(int(pid))
		if err == nil {
			if rootMount := h.rt.mountReader.FindRootMount(mounts); rootMount != nil {
				if _, upperdir, _ := h.rt.mountReader.ParseOverlayFS(rootMount); upperdir != "" {
					storage.RWLayerPath = upperdir
				}
			}
		}
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
	profile.OCI.SandboxID = h.podSandboxID
	profile.OCI.RuntimeName = "cri-o"

	// Detect OCI runtime binary from annotation.
	if rt, ok := h.annotations["io.kubernetes.cri-o.RuntimeHandler"]; ok {
		profile.OCI.RuntimeName = rt
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

	// Log path from annotations.
	if logPath, ok := h.annotations["io.kubernetes.cri-o.LogPath"]; ok {
		profile.Conmon.LogPath = logPath
	}

	// RootFS path from live mounts.
	if pid > 0 && h.rt.mountReader != nil {
		if rootFS := resolveRootFSPath(h.rt, pid); rootFS != "" {
			profile.RootFSPath = rootFS
		}
	}

	return profile, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — RWLayerStats
// ---------------------------------------------------------------------------

func (h *containerHandle) RWLayerStats(ctx context.Context) (runtime.ContainerRWLayerStats, error) {
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
	return dirUsage(upperdir), nil
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
	ref := h.image
	if ref == "" {
		ref = h.imageRef
	}
	if ref == "" {
		return nil, fmt.Errorf("container has no image reference")
	}
	return h.rt.GetImage(ctx, ref)
}

// Compile-time interface check.
var _ runtime.Container = (*containerHandle)(nil)
