package containerd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/typeurl/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	runtimecri "github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

const runtimeV2StateBase = "/run/containerd/io.containerd.runtime.v2.task"
const defaultRuncRoot = "/run/containerd/runc"

type criMetadataClient interface {
	InspectContainerMounts(ctx context.Context, containerID string) (*runtimecri.ContainerMounts, error)
	InspectPodSandboxNetwork(ctx context.Context, sandboxID string) (*runtimecri.PodSandboxNetwork, error)
	InspectContainerStatus(ctx context.Context, containerID string) (*runtimecri.ContainerStatusInfo, error)
}

// ContainerdRuntime implements the Runtime interface for containerd
type ContainerdRuntime struct {
	config           *runtime.Config
	client           *client.Client
	processCollector *sysinfo.ProcessCollector
	procReader       *sysinfo.ProcReader
	cgroupReader     *sysinfo.CGroupReader
	mountReader      *sysinfo.MountReader
	criClient        criMetadataClient
}

// NewContainerdRuntime creates a new containerd runtime instance
func NewContainerdRuntime(config *runtime.Config) *ContainerdRuntime {
	processCollector, _ := sysinfo.NewProcessCollector()
	cgroupReader, _ := sysinfo.NewCGroupReader()

	return &ContainerdRuntime{
		config:           config,
		processCollector: processCollector,
		procReader:       sysinfo.NewProcReader(),
		cgroupReader:     cgroupReader,
		mountReader:      sysinfo.NewMountReader(),
		criClient:        runtimecri.NewClient(config.SocketPath),
	}
}

// Connect establishes connection to containerd
func (r *ContainerdRuntime) Connect(ctx context.Context) error {
	if r.client != nil {
		return nil // Already connected
	}

	// Create containerd client
	c, err := client.New(
		r.config.SocketPath,
		client.WithDefaultNamespace(r.config.Namespace),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to containerd at %s: %w", r.config.SocketPath, err)
	}

	r.client = c
	return nil
}

// Close closes the connection to containerd
func (r *ContainerdRuntime) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

// ListContainers returns all containers
func (r *ContainerdRuntime) ListContainers(ctx context.Context) ([]*models.Container, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Get all containers
	containers, err := r.client.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result := make([]*models.Container, 0, len(containers))
	for _, c := range containers {
		container, err := r.convertContainer(ctx, c)
		if err != nil {
			// Log error but continue with other containers
			continue
		}
		result = append(result, container)
	}

	return result, nil
}

// convertContainer converts containerd container to our model
func (r *ContainerdRuntime) convertContainer(ctx context.Context, c client.Container) (*models.Container, error) {
	info, err := c.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container info: %w", err)
	}

	containerValue := r.buildContainerFromInfo(info)
	container := &containerValue

	// Get task to determine status and PID
	task, err := c.Task(ctx, nil)
	if err != nil {
		// Container might not have a task (not running)
		container.Status = models.ContainerStatusStopped
		return container, nil
	}

	r.populateContainerTaskState(ctx, task, container)

	return container, nil
}

// convertStatus converts containerd status to our model
func convertStatus(status string) models.ContainerStatus {
	switch status {
	case "created":
		return models.ContainerStatusCreated
	case "running":
		return models.ContainerStatusRunning
	case "paused":
		return models.ContainerStatusPaused
	case "stopped":
		return models.ContainerStatusStopped
	default:
		return models.ContainerStatusUnknown
	}
}

// GetContainer returns a specific container by ID
func (r *ContainerdRuntime) GetContainer(ctx context.Context, id string) (*models.Container, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	c, err := r.loadContainer(ctx, id)
	if err != nil {
		return nil, err
	}

	return r.convertContainer(ctx, c)
}

func (r *ContainerdRuntime) loadContainer(ctx context.Context, id string) (client.Container, error) {
	c, err := r.client.LoadContainer(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to load container %s: %w", id, err)
	}
	return c, nil
}

func (r *ContainerdRuntime) loadRunningTask(ctx context.Context, id string) (client.Container, client.Task, error) {
	c, err := r.loadContainer(ctx, id)
	if err != nil {
		return nil, nil, err
	}

	task, err := c.Task(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("container is not running")
	}

	return c, task, nil
}

type containerDetailState struct {
	container client.Container
	info      containers.Container
	task      client.Task
	detail    *models.ContainerDetail
}

func (r *ContainerdRuntime) loadContainerDetailState(ctx context.Context, id string) (*containerDetailState, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	c, err := r.loadContainer(ctx, id)
	if err != nil {
		return nil, err
	}

	info, err := c.Info(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container info: %w", err)
	}

	detail := &models.ContainerDetail{
		Container:   r.buildContainerFromInfo(info),
		ImageName:   info.Image,
		SnapshotKey: info.SnapshotKey,
		Snapshotter: info.Snapshotter,
	}

	task, err := c.Task(ctx, nil)
	if err == nil {
		r.populateContainerTaskState(ctx, task, &detail.Container)
	} else {
		detail.Status = models.ContainerStatusStopped
	}

	return &containerDetailState{
		container: c,
		info:      info,
		task:      task,
		detail:    detail,
	}, nil
}

// GetContainerDetail returns detailed information about a container
func (r *ContainerdRuntime) GetContainerDetail(ctx context.Context, id string) (*models.ContainerDetail, error) {
	state, err := r.loadContainerDetailState(ctx, id)
	if err != nil {
		return nil, err
	}

	detail := state.detail
	spec, err := state.container.Spec(ctx)
	if err == nil {
		detail.Namespaces = buildNamespaceMap(spec)
		if spec.Linux != nil && spec.Linux.CgroupsPath != "" {
			detail.CGroupPath = spec.Linux.CgroupsPath
		}
		detail.Environment = buildEnvironmentFromSpec(spec)
		detail.SharedPID = sharedPIDFromSpec(spec)
	}

	if detail.CGroupPath != "" && r.cgroupReader != nil {
		if limits, err := r.cgroupReader.ReadCGroupLimits(detail.CGroupPath); err == nil {
			detail.CGroupLimits = limits
			detail.CGroupVersion = int(r.cgroupReader.GetVersion())
		}
	}

	if r.criClient != nil {
		if statusInfo, err := r.criClient.InspectContainerStatus(ctx, state.info.ID); err == nil && statusInfo != nil {
			applyCRIContainerStatus(detail, statusInfo)
		}
	}

	if detail.PID > 0 && r.processCollector != nil {
		if processes, err := r.processCollector.CollectContainerProcesses(detail.PID); err == nil {
			detail.ProcessCount = len(processes)
		}
	}

	return detail, nil
}

// GetContainerRuntimeInfo returns runtime-specific detail for on-demand runtime views.
func (r *ContainerdRuntime) GetContainerRuntimeInfo(ctx context.Context, id string) (*models.ContainerDetail, error) {
	state, err := r.loadContainerDetailState(ctx, id)
	if err != nil {
		return nil, err
	}

	spec, err := state.container.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %w", err)
	}

	detail := state.detail
	detail.Namespaces = buildNamespaceMap(spec)

	if spec.Linux != nil && spec.Linux.CgroupsPath != "" {
		detail.CGroupPath = spec.Linux.CgroupsPath
	}

	var shimProc *shimProcessInfo
	if state.task != nil {
		shimProc = r.getShimProcessInfo(detail.PID)
		if shimProc != nil {
			detail.ShimPID = shimProc.pid
		} else {
			detail.ShimPID = r.getShimPID(detail.PID)
		}
	}

	r.populateRuntimeProfile(detail, state.info, spec, shimProc, "")

	return detail, nil
}

// GetContainerStorageInfo returns storage-specific detail for on-demand filesystem views.
func (r *ContainerdRuntime) GetContainerStorageInfo(ctx context.Context, id string) (*models.ContainerDetail, error) {
	state, err := r.loadContainerDetailState(ctx, id)
	if err != nil {
		return nil, err
	}

	spec, err := state.container.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %w", err)
	}

	detail := state.detail
	if state.info.SnapshotKey != "" {
		snapshotter := r.client.SnapshotService(state.info.Snapshotter)
		if snapshotter != nil {
			if path, err := r.getRWLayerPathFromMounts(ctx, snapshotter, state.info.SnapshotKey); err == nil {
				detail.WritableLayerPath = path
			}

			if usage, err := snapshotter.Usage(ctx, state.info.SnapshotKey); err == nil {
				detail.RWLayerUsage = usage.Size
				detail.RWLayerInodes = usage.Inodes
			}
		}
	}

	mounts, mountRootFSPath := r.resolveContainerMounts(ctx, state.info.ID, spec, detail.PID)
	detail.Mounts = mounts
	detail.MountCount = len(mounts)
	r.populateStorageProfile(detail, state.info, mountRootFSPath)

	return detail, nil
}

// GetContainerNetworkInfo returns network-specific detail for on-demand network views.
func (r *ContainerdRuntime) GetContainerNetworkInfo(ctx context.Context, id string) (*models.ContainerDetail, error) {
	state, err := r.loadContainerDetailState(ctx, id)
	if err != nil {
		return nil, err
	}

	spec, err := state.container.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %w", err)
	}

	detail := state.detail
	detail.Namespaces = buildNamespaceMap(spec)
	r.populatePodNetwork(ctx, detail, state.info, spec)

	return detail, nil
}

func (r *ContainerdRuntime) buildContainerFromInfo(info containers.Container) models.Container {
	container := models.Container{
		ID:        info.ID,
		Image:     info.Image,
		CreatedAt: info.CreatedAt,
		Labels:    info.Labels,
	}

	if name, ok := info.Labels["io.kubernetes.container.name"]; ok {
		container.Name = name
	} else if name, ok := info.Labels["name"]; ok {
		container.Name = name
	} else if len(info.ID) >= 12 {
		container.Name = info.ID[:12]
	} else {
		container.Name = info.ID
	}

	container.PodName = info.Labels["io.kubernetes.pod.name"]
	container.PodNamespace = info.Labels["io.kubernetes.pod.namespace"]
	container.PodUID = info.Labels["io.kubernetes.pod.uid"]

	return container
}

func buildNamespaceMap(spec *runtimespec.Spec) map[string]string {
	if spec == nil || spec.Linux == nil || len(spec.Linux.Namespaces) == 0 {
		return nil
	}

	namespaces := make(map[string]string, len(spec.Linux.Namespaces))
	for _, ns := range spec.Linux.Namespaces {
		namespaces[string(ns.Type)] = ns.Path
	}

	return namespaces
}

func buildEnvironmentFromSpec(spec *runtimespec.Spec) []models.EnvVar {
	if spec == nil || spec.Process == nil || len(spec.Process.Env) == 0 {
		return nil
	}

	envs := make([]models.EnvVar, 0, len(spec.Process.Env))
	for _, entry := range spec.Process.Env {
		key, value, found := strings.Cut(entry, "=")
		if !found || key == "" {
			continue
		}
		envs = append(envs, models.EnvVar{
			Key:          key,
			Value:        value,
			IsKubernetes: isKubernetesEnvKey(key),
		})
	}
	return envs
}

func sharedPIDFromSpec(spec *runtimespec.Spec) *bool {
	if spec == nil || spec.Linux == nil {
		return nil
	}
	for _, ns := range spec.Linux.Namespaces {
		if string(ns.Type) != "pid" {
			continue
		}
		shared := ns.Path != ""
		return &shared
	}
	return nil
}

func isKubernetesEnvKey(key string) bool {
	return strings.HasPrefix(key, "KUBERNETES_") || strings.HasPrefix(key, "POD_") || strings.HasPrefix(key, "SERVICE_")
}

func applyCRIContainerStatus(detail *models.ContainerDetail, statusInfo *runtimecri.ContainerStatusInfo) {
	if detail == nil || statusInfo == nil {
		return
	}
	if detail.StartedAt.IsZero() && !statusInfo.StartedAt.IsZero() {
		detail.StartedAt = statusInfo.StartedAt
	}
	if detail.ExitedAt.IsZero() && !statusInfo.FinishedAt.IsZero() {
		detail.ExitedAt = statusInfo.FinishedAt
	}
	if detail.ExitCode == nil && statusInfo.ExitCode != nil {
		detail.ExitCode = statusInfo.ExitCode
	}
	if detail.ExitReason == "" {
		detail.ExitReason = statusInfo.Reason
	}
	if detail.RestartCount == nil {
		detail.RestartCount = statusInfo.RestartCount
	}
	if detail.SharedPID == nil {
		detail.SharedPID = statusInfo.SharedPID
	}
	if len(detail.Environment) == 0 && len(statusInfo.Envs) > 0 {
		detail.Environment = make([]models.EnvVar, 0, len(statusInfo.Envs))
		for _, env := range statusInfo.Envs {
			detail.Environment = append(detail.Environment, models.EnvVar{
				Key:          env.Key,
				Value:        env.Value,
				IsKubernetes: isKubernetesEnvKey(env.Key),
			})
		}
	}
}

func (r *ContainerdRuntime) populatePodNetwork(ctx context.Context, detail *models.ContainerDetail, info containers.Container, spec *runtimespec.Spec) {
	if detail == nil {
		return
	}

	podNetwork := &models.PodNetworkInfo{
		SandboxID: info.SandboxID,
	}
	if path := namespacePathFromMap(detail.Namespaces, "network"); path != "" {
		podNetwork.NetNSPath = path
		podNetwork.NamespaceSource = "containerd-spec"
	}

	if info.SandboxID == "" {
		podNetwork.Warnings = append(podNetwork.Warnings, "sandbox id unresolved")
		if shouldAttachPodNetwork(podNetwork) {
			detail.PodNetwork = podNetwork
		}
		return
	}
	if r.criClient == nil {
		podNetwork.Warnings = append(podNetwork.Warnings, "cri metadata client unavailable")
		if shouldAttachPodNetwork(podNetwork) {
			detail.PodNetwork = podNetwork
		}
		return
	}

	criNetwork, err := r.criClient.InspectPodSandboxNetwork(ctx, info.SandboxID)
	if err != nil {
		podNetwork.Warnings = append(podNetwork.Warnings, fmt.Sprintf("cri pod sandbox status failed: %v", err))
		if shouldAttachPodNetwork(podNetwork) {
			detail.PodNetwork = podNetwork
		}
		return
	}

	if criNetwork != nil {
		podNetwork.SandboxState = criNetwork.SandboxState
		podNetwork.PrimaryIP = criNetwork.PrimaryIP
		podNetwork.AdditionalIPs = append([]string(nil), criNetwork.AdditionalIPs...)
		podNetwork.HostNetwork = criNetwork.HostNetwork
		podNetwork.NamespaceMode = criNetwork.NamespaceMode
		podNetwork.NamespaceTargetID = criNetwork.NamespaceTargetID
		podNetwork.Hostname = criNetwork.Hostname
		podNetwork.RuntimeHandler = criNetwork.RuntimeHandler
		podNetwork.RuntimeType = criNetwork.RuntimeType
		podNetwork.StatusSource = criNetwork.StatusSource
		podNetwork.ConfigSource = criNetwork.ConfigSource
		if len(criNetwork.PortMappings) > 0 {
			podNetwork.PortMappings = convertCRIPortMappings(criNetwork.PortMappings)
		}
		if criNetwork.DNS != nil {
			podNetwork.DNS = &models.DNSConfig{
				Domain:   criNetwork.DNS.Domain,
				Servers:  append([]string(nil), criNetwork.DNS.Servers...),
				Searches: append([]string(nil), criNetwork.DNS.Searches...),
				Options:  append([]string(nil), criNetwork.DNS.Options...),
			}
		}
		if criNetwork.CNI != nil {
			podNetwork.CNI = convertCRICNIResult(criNetwork.CNI)
		}
		if criNetwork.NetNSPath != "" {
			if podNetwork.NetNSPath != "" && podNetwork.NetNSPath != criNetwork.NetNSPath {
				podNetwork.Warnings = append(podNetwork.Warnings, fmt.Sprintf("netns path mismatch: spec=%s cri=%s", podNetwork.NetNSPath, criNetwork.NetNSPath))
			}
			podNetwork.NetNSPath = criNetwork.NetNSPath
			podNetwork.NamespaceSource = criNetwork.NamespaceSource
		}
		podNetwork.Warnings = append(podNetwork.Warnings, criNetwork.Warnings...)
	}

	if detail.PID > 0 && r.procReader != nil {
		if stats, err := r.procReader.ReadNetDev(int(detail.PID)); err == nil {
			podNetwork.ObservedInterfaces = append([]*models.NetworkStats(nil), stats...)
		} else {
			podNetwork.Warnings = append(podNetwork.Warnings, fmt.Sprintf("procfs net/dev read failed: %v", err))
		}
	}

	if podNetwork.NetNSPath == "" {
		if path := runtimeSpecNetworkPath(spec); path != "" {
			podNetwork.NetNSPath = path
			if podNetwork.NamespaceSource == "" {
				podNetwork.NamespaceSource = "containerd-spec"
			}
		}
	}

	detail.IPAddress = podNetwork.PrimaryIP
	detail.PortMappings = podNetwork.PortMappings
	if shouldAttachPodNetwork(podNetwork) {
		detail.PodNetwork = podNetwork
	}
}

func namespacePathFromMap(namespaces map[string]string, nsType string) string {
	if len(namespaces) == 0 {
		return ""
	}
	return namespaces[nsType]
}

func runtimeSpecNetworkPath(spec *runtimespec.Spec) string {
	if spec == nil || spec.Linux == nil {
		return ""
	}
	for _, ns := range spec.Linux.Namespaces {
		if string(ns.Type) != "network" {
			continue
		}
		return ns.Path
	}
	return ""
}

func convertCRIPortMappings(mappings []*runtimecri.PortMapping) []*models.PortMapping {
	if len(mappings) == 0 {
		return nil
	}

	result := make([]*models.PortMapping, 0, len(mappings))
	for _, mapping := range mappings {
		if mapping == nil {
			continue
		}
		result = append(result, &models.PortMapping{
			HostIP:        mapping.HostIP,
			HostPort:      mapping.HostPort,
			ContainerPort: mapping.ContainerPort,
			Protocol:      mapping.Protocol,
		})
	}
	return result
}

func convertCRICNIResult(result *runtimecri.CNIResultInfo) *models.CNIResultInfo {
	if result == nil {
		return nil
	}

	converted := &models.CNIResultInfo{}
	for _, iface := range result.Interfaces {
		if iface == nil {
			continue
		}
		entry := &models.CNIInterface{
			Name:       iface.Name,
			MAC:        iface.MAC,
			Sandbox:    iface.Sandbox,
			PciID:      iface.PciID,
			SocketPath: iface.SocketPath,
		}
		for _, addr := range iface.Addresses {
			if addr == nil {
				continue
			}
			entry.Addresses = append(entry.Addresses, &models.CNIInterfaceAddress{
				CIDR:    addr.CIDR,
				Gateway: addr.Gateway,
				Family:  addr.Family,
			})
		}
		converted.Interfaces = append(converted.Interfaces, entry)
	}
	for _, route := range result.Routes {
		if route == nil {
			continue
		}
		converted.Routes = append(converted.Routes, &models.CNIRoute{
			Destination: route.Destination,
			Gateway:     route.Gateway,
		})
	}
	if result.DNS != nil {
		converted.DNS = &models.DNSConfig{
			Domain:   result.DNS.Domain,
			Servers:  append([]string(nil), result.DNS.Servers...),
			Searches: append([]string(nil), result.DNS.Searches...),
			Options:  append([]string(nil), result.DNS.Options...),
		}
	}
	if len(converted.Interfaces) == 0 && len(converted.Routes) == 0 && converted.DNS == nil {
		return nil
	}
	return converted
}

func shouldAttachPodNetwork(info *models.PodNetworkInfo) bool {
	if info == nil {
		return false
	}
	return info.SandboxID != "" || info.PrimaryIP != "" || len(info.AdditionalIPs) > 0 || info.NetNSPath != "" || len(info.PortMappings) > 0 || info.Hostname != "" || len(info.ObservedInterfaces) > 0 || len(info.Warnings) > 0
}

func (r *ContainerdRuntime) populateContainerTaskState(ctx context.Context, task client.Task, container *models.Container) {
	container.PID = task.Pid()

	status, err := task.Status(ctx)
	if err != nil {
		container.Status = models.ContainerStatusUnknown
		return
	}

	container.Status = convertStatus(string(status.Status))
}

func buildMountsFromSpec(specMounts []runtimespec.Mount) []*models.Mount {
	if len(specMounts) == 0 {
		return nil
	}

	mounts := make([]*models.Mount, 0, len(specMounts))
	for _, m := range specMounts {
		mounts = append(mounts, &models.Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        m.Type,
			Options:     append([]string(nil), m.Options...),
		})
	}

	return mounts
}

type shimProcessInfo struct {
	pid        uint32
	binaryPath string
	cmdline    []string
	cwd        string
}

func (r *ContainerdRuntime) getShimProcessInfo(taskPID uint32) *shimProcessInfo {
	if taskPID == 0 || r.procReader == nil {
		return nil
	}

	currentPID := int(taskPID)
	for depth := 0; depth < 3; depth++ {
		ppid, err := r.procReader.GetProcessPPID(currentPID)
		if err != nil || ppid <= 0 {
			return nil
		}

		exePath, _ := r.procReader.ReadExePath(ppid)
		cmdline, _ := r.procReader.ReadCmdlineRaw(ppid)
		if isShimProcess(exePath, cmdline) {
			cwd, _ := r.procReader.ReadCwd(ppid)
			return &shimProcessInfo{
				pid:        uint32(ppid),
				binaryPath: exePath,
				cmdline:    cmdline,
				cwd:        cwd,
			}
		}

		currentPID = ppid
	}

	return nil
}

func isShimProcess(exePath string, cmdline []string) bool {
	if strings.Contains(filepath.Base(exePath), "containerd-shim") {
		return true
	}
	if len(cmdline) > 0 && strings.Contains(filepath.Base(cmdline[0]), "containerd-shim") {
		return true
	}
	return false
}

func (r *ContainerdRuntime) getShimPID(taskPID uint32) uint32 {
	if taskPID == 0 || r.procReader == nil {
		return 0
	}

	ppid, err := r.procReader.GetProcessPPID(int(taskPID))
	if err != nil || ppid <= 0 {
		return 0
	}

	return uint32(ppid)
}

func (r *ContainerdRuntime) populateRuntimeProfile(detail *models.ContainerDetail, info containers.Container, spec *runtimespec.Spec, shimProc *shimProcessInfo, mountRootFSPath string) {
	if detail.RuntimeProfile == nil {
		detail.RuntimeProfile = &models.RuntimeProfile{}
	}

	profile := detail.RuntimeProfile
	profile.OCI = &models.OCIInfo{}
	profile.Shim = &models.ShimInfo{}
	profile.CGroup = &models.CGroupInfo{}
	profile.RootFS = &models.RootFSInfo{}

	bundleDir, bundleSource := r.resolveOCIBundleDir(r.config.Namespace, info.ID)
	stateDir, stateSource := r.resolveOCIStateDir(info, r.config.Namespace)

	profile.OCI.RuntimeName = info.Runtime.Name
	profile.OCI.RuntimeSource = "containerd"
	profile.OCI.StateDir = stateDir
	profile.OCI.StateDirSource = stateSource
	profile.OCI.BundleDir = bundleDir
	profile.OCI.BundleDirSource = bundleSource
	profile.OCI.SandboxID = info.SandboxID

	profile.Shim.PID = detail.ShimPID
	profile.Shim.BundleDir = bundleDir
	profile.Shim.Source = bundleSource
	if shimProc != nil {
		profile.Shim.BinaryPath = shimProc.binaryPath
		profile.Shim.Cmdline = append([]string(nil), shimProc.cmdline...)
		profile.Shim.Source = "procfs"
	}

	if runtimeBinary, runtimeSource := r.resolveRuntimeBinaryPath(bundleDir, info.Runtime, shimProc); runtimeBinary != "" {
		profile.OCI.RuntimeBinary = runtimeBinary
		if runtimeSource != "" {
			profile.OCI.RuntimeSource = runtimeSource
		}
	}

	if configPath := existingPath(filepath.Join(bundleDir, "config.json")); configPath != "" {
		profile.OCI.ConfigPath = configPath
		profile.OCI.ConfigSource = bundleSource
	}

	if relativePath := detail.CGroupPath; relativePath != "" {
		profile.CGroup.RelativePath = relativePath
		profile.CGroup.AbsolutePath = filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(relativePath, "/"))
		profile.CGroup.Driver = inferCGroupDriver(relativePath)
		profile.CGroup.Source = "spec"
		profile.CGroup.Version = detail.CGroupVersion
	}

	if detail.PID > 0 && r.procReader != nil {
		if procCgroupPath, err := r.procReader.ReadUnifiedCGroupPath(int(detail.PID)); err == nil {
			if profile.CGroup.RelativePath == "" {
				profile.CGroup.RelativePath = procCgroupPath
				profile.CGroup.AbsolutePath = filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(procCgroupPath, "/"))
				profile.CGroup.Driver = inferCGroupDriver(procCgroupPath)
				profile.CGroup.Source = "procfs"
			}
		}
	}

	if rootfsPath := existingPath(filepath.Join(bundleDir, "rootfs")); rootfsPath != "" {
		profile.RootFS.BundleRootFSPath = rootfsPath
		profile.RootFS.Source = bundleSource
	}
	if mountRootFSPath != "" {
		profile.RootFS.MountRootFSPath = mountRootFSPath
		if profile.RootFS.Source == "" {
			profile.RootFS.Source = "mountinfo"
		}
	}

	if socketAddress, sandboxID, sandboxBundleDir, source := r.resolveShimSocketAddress(bundleDir, info.ID, profile.OCI.SandboxID); socketAddress != "" {
		profile.Shim.SocketAddress = socketAddress
		profile.Shim.SandboxBundleDir = sandboxBundleDir
		if profile.OCI.SandboxID == "" {
			profile.OCI.SandboxID = sandboxID
		}
		profile.Shim.Source = source
	}

	detail.OCIBundlePath = profile.OCI.BundleDir
	detail.OCIRuntimeDir = profile.OCI.StateDir
	if profile.Shim.PID > 0 {
		detail.ShimPID = profile.Shim.PID
	}
}

func (r *ContainerdRuntime) populateStorageProfile(detail *models.ContainerDetail, info containers.Container, mountRootFSPath string) {
	if detail.RuntimeProfile == nil {
		detail.RuntimeProfile = &models.RuntimeProfile{}
	}
	if detail.RuntimeProfile.RootFS == nil {
		detail.RuntimeProfile.RootFS = &models.RootFSInfo{}
	}

	rootfs := detail.RuntimeProfile.RootFS
	bundleDir, bundleSource := r.resolveOCIBundleDir(r.config.Namespace, info.ID)
	if rootfsPath := existingPath(filepath.Join(bundleDir, "rootfs")); rootfsPath != "" {
		rootfs.BundleRootFSPath = rootfsPath
		rootfs.Source = bundleSource
	}
	if mountRootFSPath != "" {
		rootfs.MountRootFSPath = mountRootFSPath
		if rootfs.Source == "" {
			rootfs.Source = "mountinfo"
		}
	}
}

func (r *ContainerdRuntime) resolveRuntimeBinaryPath(bundleDir string, runtimeInfo containers.RuntimeInfo, shimProc *shimProcessInfo) (string, string) {
	if opts := resolveRuncOptions(runtimeInfo); opts != nil && opts.BinaryName != "" {
		return opts.BinaryName, "runtime-options"
	}
	runtimeName := runtimeInfo.Name
	shimBinaryPath := filepath.Join(bundleDir, "shim-binary-path")
	if data, err := os.ReadFile(shimBinaryPath); err == nil {
		if value := strings.TrimSpace(string(data)); value != "" {
			return value, "bundle"
		}
	}
	if shimProc != nil && shimProc.binaryPath != "" {
		return shimProc.binaryPath, "procfs"
	}
	if strings.HasPrefix(runtimeName, "/") {
		return runtimeName, "containerd"
	}
	parts := strings.Split(runtimeName, ".")
	if len(parts) >= 2 {
		return "containerd-shim-" + parts[len(parts)-2] + "-" + parts[len(parts)-1], "derived"
	}
	return runtimeName, "derived"
}

func (r *ContainerdRuntime) resolveOCIBundleDir(namespace string, containerID string) (string, string) {
	return filepath.Join(runtimeV2StateBase, namespace, containerID), "convention"
}

func (r *ContainerdRuntime) resolveOCIStateDir(info containers.Container, namespace string) (string, string) {
	if root, source := r.resolveRuncRoot(info.Runtime); root != "" {
		return filepath.Join(root, namespace, info.ID), source
	}
	return filepath.Join(runtimeV2StateBase, namespace, info.ID), "convention"
}

func (r *ContainerdRuntime) resolveRuncRoot(runtimeInfo containers.RuntimeInfo) (string, string) {
	if opts := resolveRuncOptions(runtimeInfo); opts != nil {
		if opts.Root != "" {
			return opts.Root, "runtime-options"
		}
		if isRuncRuntimeName(runtimeInfo.Name) {
			return defaultRuncRoot, "runtime-default"
		}
	}
	if isRuncRuntimeName(runtimeInfo.Name) {
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

func (r *ContainerdRuntime) resolveShimSocketAddress(bundleDir string, containerID string, sandboxIDHint string) (string, string, string, string) {
	if address, err := readBootstrapAddress(filepath.Join(bundleDir, "bootstrap.json")); err == nil {
		return address, "", "", "bundle"
	}
	if address, err := readAddressFile(filepath.Join(bundleDir, "address")); err == nil {
		return address, "", "", "bundle"
	}

	sandboxID := sandboxIDHint
	if sandboxID == "" {
		sandboxData, err := os.ReadFile(filepath.Join(bundleDir, "sandbox"))
		if err == nil {
			sandboxID = strings.TrimSpace(string(sandboxData))
		}
	}
	if sandboxID != "" {
		sandboxBundleDir := filepath.Join(filepath.Dir(bundleDir), sandboxID)
		if address, bootstrapErr := readBootstrapAddress(filepath.Join(sandboxBundleDir, "bootstrap.json")); bootstrapErr == nil {
			return address, sandboxID, sandboxBundleDir, "sandbox-bundle"
		}
		if address, addressErr := readAddressFile(filepath.Join(sandboxBundleDir, "address")); addressErr == nil {
			return address, sandboxID, sandboxBundleDir, "sandbox-bundle"
		}
		return computeShimSocketAddress(r.config.Namespace, sandboxID), sandboxID, sandboxBundleDir, "inferred"
	}

	return computeShimSocketAddress(r.config.Namespace, containerID), "", "", "inferred"
}

func readBootstrapAddress(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}

	var params struct {
		Address  string `json:"address"`
		Protocol string `json:"protocol"`
	}
	if err := json.Unmarshal(data, &params); err != nil {
		return "", err
	}
	if params.Address == "" {
		return "", fmt.Errorf("bootstrap address missing")
	}
	if params.Protocol != "" {
		return params.Protocol + "+" + params.Address, nil
	}
	return params.Address, nil
}

func readAddressFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", fmt.Errorf("address file empty")
	}
	return value, nil
}

func computeShimSocketAddress(namespace string, id string) string {
	path := filepath.Join(runtimeV2StateBase, namespace, id)
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("unix:///run/containerd/s/%x", sum)
}

func existingPath(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

func isRuncRuntimeName(runtimeName string) bool {
	return strings.Contains(runtimeName, ".runc.") || strings.HasSuffix(runtimeName, "runc")
}

func inferCGroupDriver(path string) string {
	if path == "" {
		return ""
	}
	if strings.Contains(path, ".slice") || strings.Contains(path, ":cri-containerd:") {
		return "systemd"
	}
	return "cgroupfs"
}

func (r *ContainerdRuntime) resolveMountRootFSPath(mounts []*models.Mount) string {
	if r.mountReader == nil {
		return ""
	}
	rootMount := r.mountReader.FindRootMount(mounts)
	if rootMount == nil {
		return ""
	}
	if lowerdir, upperdir, _ := r.mountReader.ParseOverlayFS(rootMount); lowerdir != "" || upperdir != "" {
		if upperdir != "" {
			return upperdir
		}
	}
	return rootMount.Source
}

// ListImages returns all images
func (r *ContainerdRuntime) ListImages(ctx context.Context) ([]*models.Image, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	images, err := r.client.ListImages(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list images: %w", err)
	}

	result := make([]*models.Image, 0, len(images))
	for _, img := range images {
		image := &models.Image{
			Name:      img.Name(),
			CreatedAt: img.Metadata().CreatedAt,
			Labels:    img.Metadata().Labels,
		}

		// Get image size
		size, err := img.Size(ctx)
		if err == nil {
			image.Size = size
		}

		// Get image digest
		if img.Target().Digest != "" {
			image.Digest = img.Target().Digest.String()
		}

		result = append(result, image)
	}

	return result, nil
}

// GetImage returns a specific image by name or digest
func (r *ContainerdRuntime) GetImage(ctx context.Context, ref string) (*models.Image, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	img, err := r.client.GetImage(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", ref, err)
	}

	image := &models.Image{
		Name:      img.Name(),
		CreatedAt: img.Metadata().CreatedAt,
		Labels:    img.Metadata().Labels,
	}

	// Get image size
	size, err := img.Size(ctx)
	if err == nil {
		image.Size = size
	}

	// Get image digest
	if img.Target().Digest != "" {
		image.Digest = img.Target().Digest.String()
	}

	return image, nil
}

// imageInfo holds all parsed image metadata for a given image reference
type imageInfo struct {
	target     ocispec.Descriptor // Original image target descriptor
	configDesc ocispec.Descriptor
	manifest   ocispec.Manifest
	diffIDs    []digest.Digest
}

// getImageInfo retrieves and parses all image metadata in one operation
// This ensures consistency across config, manifest, and diffIDs by using the same platform
func (r *ContainerdRuntime) getImageInfo(ctx context.Context, imageID string) (*imageInfo, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	img, err := r.client.GetImage(ctx, imageID)
	if err != nil {
		return nil, fmt.Errorf("failed to get image %s: %w", imageID, err)
	}

	cs := r.client.ContentStore()

	// Get config descriptor (platform-matched by containerd)
	configDesc, err := img.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image config: %w", err)
	}

	// Get manifest that matches the config (ensures platform consistency)
	manifest, err := r.getManifestForConfig(ctx, cs, img.Target(), configDesc.Digest)
	if err != nil {
		return nil, fmt.Errorf("failed to get image manifest: %w", err)
	}

	// Parse diffIDs from config
	diffIDs, err := r.parseDiffIDsFromConfig(ctx, cs, configDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse diff ids: %w", err)
	}

	return &imageInfo{
		target:     img.Target(),
		configDesc: configDesc,
		manifest:   manifest,
		diffIDs:    diffIDs,
	}, nil
}

// GetImageConfigInfo returns metadata about the image config
func (r *ContainerdRuntime) GetImageConfigInfo(ctx context.Context, imageID string) (*models.ImageConfigInfo, error) {
	info, err := r.getImageInfo(ctx, imageID)
	if err != nil {
		return nil, err
	}

	targetKind, schema := describeImageTarget(info.target.MediaType)

	return &models.ImageConfigInfo{
		Digest:          info.configDesc.Digest.String(),
		ContentPath:     r.getContentPath(info.configDesc.Digest),
		Size:            info.configDesc.Size,
		TargetMediaType: info.target.MediaType,
		TargetKind:      targetKind,
		Schema:          schema,
	}, nil
}

// GetImageLayers returns detailed layer information for an image (from top to base)
// If rwSnapshotKey is provided, layer paths are resolved from the RW layer's mount info
func (r *ContainerdRuntime) GetImageLayers(ctx context.Context, imageID string, snapshotterName string, rwSnapshotKey string) ([]*models.ImageLayer, error) {
	info, err := r.getImageInfo(ctx, imageID)
	if err != nil {
		return nil, err
	}

	if len(info.manifest.Layers) != len(info.diffIDs) {
		return nil, fmt.Errorf("layer count mismatch: manifest has %d layers, config has %d diff ids",
			len(info.manifest.Layers), len(info.diffIDs))
	}

	return r.buildImageLayers(ctx, info.manifest, info.diffIDs, snapshotterName, rwSnapshotKey)
}

// buildImageLayers constructs ImageLayer models from manifest and diffIDs
// Uses RW Layer mounts to get all Read-Only Layer paths in one call
func (r *ContainerdRuntime) buildImageLayers(ctx context.Context, manifest ocispec.Manifest, diffIDs []digest.Digest, snapshotterName string, rwSnapshotKey string) ([]*models.ImageLayer, error) {
	// Use provided snapshotter name or default to overlayfs
	if snapshotterName == "" {
		snapshotterName = "overlayfs"
	}
	snapshotter := r.client.SnapshotService(snapshotterName)
	layerCount := len(manifest.Layers)
	layers := make([]*models.ImageLayer, layerCount)

	// Calculate Chain IDs from base to top
	chainIDs := r.calculateChainIDs(diffIDs)

	// Get Read-Only Layer paths from RW Layer mounts (single call)
	var roPaths []string
	if rwSnapshotKey != "" {
		roPaths = r.getReadOnlyLayerPathsFromMounts(ctx, snapshotter, rwSnapshotKey)
	}

	// Build layers from base to top (natural order matching diff_ids and chainIDs)
	// layers[0] = base layer, layers[n-1] = top layer
	for i := 0; i < layerCount; i++ {
		chainID := chainIDs[i].String()
		layer := &models.ImageLayer{
			Index:              i,
			CompressedDigest:   manifest.Layers[i].Digest.String(),
			UncompressedDigest: diffIDs[i].String(),
			Size:               manifest.Layers[i].Size,
			ContentPath:        r.getContentPath(manifest.Layers[i].Digest),
			SnapshotKey:        chainID,
		}

		// Get compression type from media type
		layer.CompressionType = r.getCompressionType(manifest.Layers[i].MediaType)

		// Check if snapshot exists
		if _, err := snapshotter.Stat(ctx, chainID); err == nil {
			layer.SnapshotExists = true
			// Map Read-Only Layer path from pre-fetched paths
			// roPaths order: [top, mid..., base]
			// layers order: [base(0), mid..., top(n-1)]
			// So roPaths index = (layerCount - 1 - i)
			if len(roPaths) > 0 && i < len(roPaths) {
				roIndex := len(roPaths) - 1 - i
				if roIndex >= 0 && roIndex < len(roPaths) {
					layer.SnapshotPath = roPaths[roIndex]
				}
			}

			// Get usage information for this layer
			if usage, err := snapshotter.Usage(ctx, chainID); err == nil {
				layer.UsageSize = usage.Size
				layer.UsageInodes = usage.Inodes
			}
		}

		layers[i] = layer
	}

	return layers, nil
}

// getReadOnlyLayerPathsFromMounts extracts Read-Only Layer paths from RW Layer mounts
// Returns paths in order: [top, mid..., base] (matching lowerdir order)
func (r *ContainerdRuntime) getReadOnlyLayerPathsFromMounts(ctx context.Context, snapshotter snapshots.Snapshotter, rwKey string) []string {

	mounts, err := snapshotter.Mounts(ctx, rwKey)
	if err != nil {
		return nil
	}

	for _, m := range mounts {
		if m.Type == "overlay" {
			for _, opt := range m.Options {
				if len(opt) > 9 && opt[:9] == "lowerdir=" {
					// lowerdir format: /path/to/top:/path/to/mid:/path/to/base
					return strings.Split(opt[9:], ":")
				}
			}
		}
	}

	return nil
}

// getRWLayerPathFromMounts retrieves RW layer path (upperdir) using Snapshotter Mounts API
func (r *ContainerdRuntime) getRWLayerPathFromMounts(ctx context.Context, snapshotter snapshots.Snapshotter, key string) (string, error) {
	mounts, err := snapshotter.Mounts(ctx, key)
	if err != nil {
		return "", err
	}

	// Parse mount options to find upperdir (RW layer)
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

// getManifestForConfig finds the manifest that contains the given config digest
// This handles multi-arch images by iterating through the index to find the matching platform
func (r *ContainerdRuntime) getManifestForConfig(ctx context.Context, cs content.Store, target ocispec.Descriptor, configDigest digest.Digest) (ocispec.Manifest, error) {
	// If target is already a manifest, check if it matches
	if images.IsManifestType(target.MediaType) {
		manifest, err := images.Manifest(ctx, cs, target, nil)
		if err != nil {
			return ocispec.Manifest{}, err
		}
		if manifest.Config.Digest != configDigest {
			return ocispec.Manifest{}, fmt.Errorf("manifest config digest mismatch")
		}
		return manifest, nil
	}

	// Target is likely an index (multi-arch), read and iterate through manifests
	data, err := content.ReadBlob(ctx, cs, target)
	if err != nil {
		return ocispec.Manifest{}, fmt.Errorf("failed to read image index: %w", err)
	}

	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return ocispec.Manifest{}, fmt.Errorf("failed to unmarshal image index: %w", err)
	}

	for _, desc := range index.Manifests {
		if !images.IsManifestType(desc.MediaType) {
			continue
		}
		manifest, err := images.Manifest(ctx, cs, desc, nil)
		if err != nil {
			continue
		}
		if manifest.Config.Digest == configDigest {
			return manifest, nil
		}
	}

	return ocispec.Manifest{}, fmt.Errorf("no manifest found with config digest %s", configDigest)
}

// parseDiffIDsFromConfig extracts RootFS DiffIDs directly from config descriptor
func (r *ContainerdRuntime) parseDiffIDsFromConfig(ctx context.Context, cs content.Store, configDesc ocispec.Descriptor) ([]digest.Digest, error) {
	// Read config blob directly
	data, err := content.ReadBlob(ctx, cs, configDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var config struct {
		RootFS struct {
			Type    string
			DiffIDs []digest.Digest `json:"diff_ids"`
		} `json:"rootfs"`
	}

	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return config.RootFS.DiffIDs, nil
}

// calculateChainIDs calculates Chain IDs from base to top
// Layer 0 (base): chain[0] = diffIDs[0]
// Layer n: chain[n] = sha256(chain[n-1] + " " + diffIDs[n])
// Note: Use String() to get full digest with "sha256:" prefix
func (r *ContainerdRuntime) calculateChainIDs(diffIDs []digest.Digest) []digest.Digest {
	chainIDs := make([]digest.Digest, len(diffIDs))

	for i, diffID := range diffIDs {
		if i == 0 {
			chainIDs[i] = diffID
		} else {
			chainIDs[i] = digest.FromString(chainIDs[i-1].String() + " " + diffID.String())
		}
	}

	return chainIDs
}

// getContentPath generates the content store blob path
func (r *ContainerdRuntime) getContentPath(dgst digest.Digest) string {
	algo := dgst.Algorithm()
	encoded := dgst.Encoded()

	// Default containerd content store path
	return fmt.Sprintf("/var/lib/containerd/io.containerd.content.v1.content/blobs/%s/%s", algo, encoded)
}

// getCompressionType extracts compression type from layer media type
func (r *ContainerdRuntime) getCompressionType(mediaType string) string {
	switch mediaType {
	// Gzip compressed
	case images.MediaTypeDockerSchema2LayerGzip,
		images.MediaTypeDockerSchema2LayerForeignGzip,
		ocispec.MediaTypeImageLayerGzip:
		return "gzip"
	// Zstd compressed
	case images.MediaTypeDockerSchema2LayerZstd,
		ocispec.MediaTypeImageLayerZstd:
		return "zstd"
	// Uncompressed
	case images.MediaTypeDockerSchema2Layer,
		images.MediaTypeDockerSchema2LayerForeign,
		ocispec.MediaTypeImageLayer:
		return ""
	default:
		// Try to extract from OCI media type with + suffix
		if strings.Contains(mediaType, "+gzip") {
			return "gzip"
		}
		if strings.Contains(mediaType, "+zstd") {
			return "zstd"
		}
		return ""
	}
}

func describeImageTarget(mediaType string) (string, string) {
	kind := "Unknown"
	schema := "Unknown"

	if images.IsManifestType(mediaType) {
		kind = "Manifest"
	} else {
		switch mediaType {
		case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
			kind = "Index"
		}
	}

	switch {
	case strings.Contains(mediaType, "docker"):
		schema = "Docker"
	case strings.Contains(mediaType, "oci"):
		schema = "OCI"
	}

	return kind, schema
}

// ListPods returns all pods
func (r *ContainerdRuntime) ListPods(ctx context.Context) ([]*models.Pod, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	// Get all containers first
	containers, err := r.ListContainers(ctx)
	if err != nil {
		return nil, err
	}

	// Group containers by pod
	podMap := make(map[string]*models.Pod)
	for _, container := range containers {
		// Skip containers without pod information
		if container.PodUID == "" {
			continue
		}

		pod, exists := podMap[container.PodUID]
		if !exists {
			pod = &models.Pod{
				Name:       container.PodName,
				Namespace:  container.PodNamespace,
				UID:        container.PodUID,
				Containers: make([]*models.Container, 0),
			}
			podMap[container.PodUID] = pod
		}

		pod.Containers = append(pod.Containers, container)
	}

	// Convert map to slice
	result := make([]*models.Pod, 0, len(podMap))
	for _, pod := range podMap {
		result = append(result, pod)
	}

	return result, nil
}

// GetContainerProcesses returns process information for a container
func (r *ContainerdRuntime) GetContainerProcesses(ctx context.Context, id string) ([]*models.Process, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if r.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}

	_, task, err := r.loadRunningTask(ctx, id)
	if err != nil {
		return nil, err
	}

	// Collect processes
	return r.processCollector.CollectContainerProcesses(task.Pid())
}

// GetContainerTop returns top-like process information
func (r *ContainerdRuntime) GetContainerTop(ctx context.Context, id string) (*models.ProcessTop, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	if r.processCollector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}

	c, task, err := r.loadRunningTask(ctx, id)
	if err != nil {
		return nil, err
	}

	// Get cgroup path from OCI spec for CPU/memory limit context
	var cgroupPath string
	if spec, err := c.Spec(ctx); err == nil && spec.Linux != nil {
		cgroupPath = spec.Linux.CgroupsPath
	}

	// Collect top information with rate calculation
	return r.processCollector.CollectProcessTop(task.Pid(), cgroupPath)
}

// GetContainerMounts returns mount information for a container
func (r *ContainerdRuntime) GetContainerMounts(ctx context.Context, id string) ([]*models.Mount, error) {
	if r.client == nil {
		return nil, fmt.Errorf("client not connected")
	}

	c, task, err := r.loadRunningTask(ctx, id)
	if err != nil {
		return nil, err
	}

	spec, err := c.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %w", err)
	}

	mounts, _ := r.resolveContainerMounts(ctx, id, spec, task.Pid())
	return mounts, nil
}
