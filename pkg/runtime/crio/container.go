package crio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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

	// spoofed container: CRI cannot see it, supplement built from OCI spec.
	spoofed     bool
	spoofedOnce sync.Once

	// one-time refresh of h.cri with authoritative data from ContainerStatus
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

// ensureCRISupplement upgrades h.cri with authoritative labels, annotations,
// image, imageRef, and name sourced from ContainerStatus (verbose=true).
// This runs once and is a no-op for spoofed containers.
// Must be called after ensureSpoofedSupplement so that h.spoofed is set.
func (h *containerHandle) ensureCRISupplement(ctx context.Context) {
	h.ensureSpoofedSupplement(ctx)
	if h.spoofed {
		return
	}
	h.statusOnce.Do(func() {
		status, err := h.rt.criClient.InspectContainerStatus(ctx, h.id)
		if err != nil || status == nil {
			return
		}
		if h.cri == nil {
			h.cri = &criContainerSupplement{}
		}
		if len(status.Labels) > 0 {
			h.cri.labels = status.Labels
		}
		if len(status.Annotations) > 0 {
			h.cri.annotations = status.Annotations
		}
		if status.Image != "" && h.cri.image == "" {
			h.cri.image = status.Image
		}
		if status.ImageRef != "" && h.cri.imageRef == "" {
			h.cri.imageRef = status.ImageRef
		}
		if status.Name != "" && h.cri.name == "" {
			h.cri.name = status.Name
		}
	})
}

func (h *containerHandle) ensureSpec(_ context.Context) {
	h.specOnce.Do(func() {
		h.spec, h.specErr = h.readOCISpec()
	})
}

// ensureSpoofedSupplement lazily checks OCI spec annotations for spoofed
// containers (not visible to CRI) and builds a CRI supplement from annotations.
func (h *containerHandle) ensureSpoofedSupplement(ctx context.Context) {
	if h.cri != nil {
		return
	}
	h.spoofedOnce.Do(func() {
		h.ensureSpec(ctx)
		if h.spec == nil {
			return
		}
		if h.spec.Annotations["spoofed.crio.io"] != "true" {
			return
		}
		h.spoofed = true
		h.cri = buildSupplementFromSpecAnnotations(h.spec)
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
// Spoofed containers are not visible to CRI so we skip the call.
func (h *containerHandle) pid(ctx context.Context) uint32 {
	if h.spoofed {
		return 0
	}
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
	h.ensureCRISupplement(ctx)

	info := &runtime.ContainerInfo{
		ID:        h.id,
		Name:      h.containerName(),
		CreatedAt: h.createdAt,
		PID:       h.pid(ctx),
	}

	if h.cri != nil {
		// ImageRef from io.kubernetes.cri-o.ImageName annotation.
		if v := h.cri.annotations["io.kubernetes.cri-o.ImageName"]; v != "" {
			info.ImageRef = v
		} else if h.cri.image != "" {
			info.ImageRef = h.cri.image
		}
		// ImageID from io.kubernetes.cri-o.ImageRef annotation.
		if v := h.cri.annotations["io.kubernetes.cri-o.ImageRef"]; v != "" {
			info.ImageID = v
		}

		// Spoofed containers are not visible to CRI; skip status call.
		if !h.spoofed {
			status, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id)
			if status != nil {
				info.Status = runtime.ConvertOCIContainerStatus(status.Status)
			}
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
	h.ensureCRISupplement(ctx)
	h.ensureSpec(ctx)

	cfg := &runtime.ContainerConfig{}
	if store, err := h.rt.getStore(); err == nil {
		driverName := store.GraphDriverName()
		if driverName != "" {
			cfg.Backend = &runtime.LayerBackend{
				Kind: runtime.LayerBackendContainersStorage,
				Name: driverName,
			}
		}
	}

	if h.cri != nil {
		if v := h.cri.annotations["io.kubernetes.cri-o.ImageName"]; v != "" {
			cfg.ImageRef = v
		} else if h.cri.image != "" {
			cfg.ImageRef = h.cri.image
		}
		if v := h.cri.annotations["io.kubernetes.cri-o.ImageRef"]; v != "" {
			cfg.ImageID = v
		}
	}

	if h.spec != nil {
		cfg.Namespaces = runtime.BuildNamespaceMap(h.spec)
		if h.spec.Linux != nil && h.spec.Linux.CgroupsPath != "" {
			cfg.CGroupPath = h.spec.Linux.CgroupsPath
			cfg.CGroupDriver = runtime.InferCGroupDriver(h.spec.Linux.CgroupsPath)
		}
	}

	var status *cri.ContainerStatus
	if !h.spoofed {
		status, _ = h.rt.criClient.InspectContainerStatus(ctx, h.id)
	}
	cfg.Environment = cri.BuildEnvironment(h.spec, status)

	if cfg.CGroupPath != "" && h.rt.cgroupReader != nil {
		cfg.CGroupVersion = int(h.rt.cgroupReader.GetVersion())
		cfg.CGroupRootPath = runtime.CGroupRootPath()
	}

	// RW layer path from storage.
	cfg.WritableLayerPath = h.resolveRWLayerPath()
	return cfg, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — State
// ---------------------------------------------------------------------------

func (h *containerHandle) State(ctx context.Context) (*runtime.ContainerState, error) {
	h.ensureCRISupplement(ctx)
	pid := h.pid(ctx)

	state := &runtime.ContainerState{
		PID: pid,
	}

	if h.spoofed {
		state.Status = runtime.ContainerStatusStopped
	} else if h.cri != nil {
		status, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id)
		if status != nil {
			state.Status = runtime.ConvertOCIContainerStatus(status.Status)
		} else {
			state.Status = runtime.ContainerStatusUnknown
		}
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

	if !h.spoofed {
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
	}

	return state, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — CGroup
// ---------------------------------------------------------------------------

func (h *containerHandle) CGroup(ctx context.Context) (*runtime.ContainerCGroupInfo, error) {
	h.ensureSpec(ctx)

	var rawPath string
	if h.spec != nil && h.spec.Linux != nil {
		rawPath = h.spec.Linux.CgroupsPath
	}

	info := &runtime.ContainerCGroupInfo{
		RootPath: runtime.CGroupRootPath(),
	}
	if rawPath != "" {
		info.Driver = runtime.InferCGroupDriver(rawPath)
	}

	if h.rt.cgroupReader != nil {
		info.Version = int(h.rt.cgroupReader.GetVersion())
		info.Path = runtime.ResolveCGroupPath(rawPath, info.Version, h.pid(ctx), h.rt.procReader)
	} else {
		info.Path = runtime.NormalizeCGroupPath(rawPath)
	}
	if info.Driver == "" && info.Path != "" {
		info.Driver = runtime.InferCGroupDriver(info.Path)
	}
	runtime.PopulateCGroupStatsFromReader(info, h.rt.cgroupReader)

	info.SpecResources = runtime.ExtractSpecResources(h.spec)

	return info, nil
}

// ---------------------------------------------------------------------------
// runtime.Container — Network
// ---------------------------------------------------------------------------

func (h *containerHandle) Network(ctx context.Context) (*runtime.ContainerNetworkState, error) {
	h.ensureCRISupplement(ctx)
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

	// ReadOnly from OCI spec.
	h.ensureSpec(ctx)
	if h.spec != nil && h.spec.Root != nil && h.spec.Root.Readonly {
		storage.ReadOnly = true
	}
	if store, err := h.rt.getStore(); err == nil {
		driverName := store.GraphDriverName()
		if driverName != "" {
			storage.Backend = &runtime.LayerBackend{
				Kind: runtime.LayerBackendContainersStorage,
				Name: driverName,
			}
		}
		if driverName != "" || h.layerID != "" {
			storage.Crio = &runtime.CRIOContainerStorage{
				StorageDriver: driverName,
				RWLayerID:     h.layerID,
			}
		}
	} else if h.layerID != "" {
		storage.Crio = &runtime.CRIOContainerStorage{
			RWLayerID: h.layerID,
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
	h.ensureCRISupplement(ctx)
	h.ensureSpec(ctx)
	pid := h.pid(ctx)

	profile := &runtime.RuntimeProfile{
		OCI:    &runtime.OCIInfo{},
		Conmon: &runtime.ConmonInfo{},
	}

	bundleDir := crioContainerBundleDir(h.rt.storageRunRoot, h.id)
	profile.OCI.BundleDir = bundleDir
	if statePath := runtime.ExistingPath(bundleDir + "/state.json"); statePath != "" {
		profile.OCI.StatePath = statePath
	}
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

	if configPath := runtime.ExistingPath(bundleDir + "/config.json"); configPath != "" {
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
	return runtime.CollectProcesses(h.rt.processCollector, pid)
}

func (h *containerHandle) ProcessStats(ctx context.Context) (*runtime.ProcessStats, error) {
	pid := h.pid(ctx)
	if pid == 0 {
		return nil, fmt.Errorf("container is not running")
	}
	return runtime.CollectProcessStats(h.rt.processCollector, pid, h.resolvedCGroupPath(ctx, pid))
}

func (h *containerHandle) GetProcessStats(ctx context.Context, pidStr string) (*runtime.ProcessStats, error) {
	pid := h.pid(ctx)
	if pid == 0 {
		return nil, fmt.Errorf("container is not running")
	}
	return runtime.CollectSingleProcessStats(h.rt.processCollector, pid, h.resolvedCGroupPath(ctx, pid), pidStr)
}

// ---------------------------------------------------------------------------
// runtime.Container — Image
// ---------------------------------------------------------------------------

func (h *containerHandle) Image(ctx context.Context) (runtime.Image, error) {
	h.ensureCRISupplement(ctx)
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

func (h *containerHandle) resolvedCGroupPath(ctx context.Context, pid uint32) string {
	h.ensureSpec(ctx)
	var rawPath string
	if h.spec != nil && h.spec.Linux != nil {
		rawPath = h.spec.Linux.CgroupsPath
	}
	version := 0
	if h.rt.cgroupReader != nil {
		version = int(h.rt.cgroupReader.GetVersion())
	}
	return runtime.ResolveCGroupPath(rawPath, version, pid, h.rt.procReader)
}

// ---------------------------------------------------------------------------
// runtime.Container — Stdio
// ---------------------------------------------------------------------------

func (h *containerHandle) Stdio(ctx context.Context) (*runtime.ContainerStdio, error) {
	h.ensureCRISupplement(ctx)
	h.ensureSpec(ctx)
	pid := h.pid(ctx)

	s := &runtime.ContainerStdio{}
	bundleDir := crioContainerBundleDir(h.rt.storageRunRoot, h.id)

	// OCI spec: Process.Terminal
	if h.spec != nil && h.spec.Process != nil {
		s.TTY = runtime.BoolPtr(h.spec.Process.Terminal)
	}

	// CRI verbose info (live fields: TTY, Stdin, StdinOnce, LogPath).
	crioInfo := &runtime.CRIOStdioInfo{}
	if !h.spoofed {
		if criStatus, _ := h.rt.criClient.InspectContainerStatus(ctx, h.id); criStatus != nil {
			if criStatus.TTY != nil {
				s.TTY = criStatus.TTY
			}
			s.OpenStdin = criStatus.Stdin
			s.StdinOnce = criStatus.StdinOnce

			s.CRI = &runtime.CRIStdioInfo{
				ConfigLogPath: criStatus.ConfigLogPath,
				StatusLogPath: criStatus.StatusLogPath,
			}

			if criStatus.StatusLogPath != "" {
				s.LogPath = criStatus.StatusLogPath
			} else if criStatus.ConfigLogPath != "" {
				s.LogPath = criStatus.ConfigLogPath
			}
		}
	}

	if logPath, ok := h.cri.annotations["io.kubernetes.cri-o.LogPath"]; ok {
		crioInfo.AnnotationLogPath = logPath
		if s.LogPath == "" {
			s.LogPath = logPath
		}
	}

	// Conmon process inspection.
	attachSocket := ""
	if pid > 0 && h.rt.procReader != nil {
		if conmon := getConmonProcessInfo(h.rt.procReader, pid); conmon != nil {
			if fields := runtime.ParseConmonCmdline(conmon.cmdline); fields != nil {
				crioInfo.LogPathFromConmonCmd = fields.LogPath
				if fields.LogPath != "" && s.LogPath == "" {
					s.LogPath = fields.LogPath
				}
				attachSocket = fields.AttachSocket
				if fields.Terminal != nil {
					s.TTY = fields.Terminal
				}
			}
		}
	}

	// Discover attach/control files in bundle dir.
	if attachSocket == "" {
		attachSocket = runtime.FindUserdataFile(bundleDir, "attach")
	}
	controlFile := runtime.FindUserdataFile(bundleDir, "ctl")
	winszFile := runtime.FindUserdataFile(bundleDir, "winsz")

	// Build AttachInfo.
	if attachSocket != "" || controlFile != "" {
		crioInfo.Attach = &runtime.AttachInfo{
			Socket:        attachSocket,
			ControlSocket: controlFile,
			ResizeFile:    winszFile,
		}
	}

	s.CRIO = crioInfo

	// Proc FD fallback: resolve actual open files on stdin/stdout/stderr when paths are unknown.
	if pid > 0 && h.rt.procReader != nil {
		fd0, fd1, fd2 := h.rt.procReader.ReadStdioFDs(int(pid))
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
