package crio

import (
	"path/filepath"
	"strings"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// ---------------------------------------------------------------------------
// CRI-O path helpers
// ---------------------------------------------------------------------------

// crioContainerBundleDir returns the CRI-O bundle directory for a container.
// CRI-O stores OCI runtime state under <runRoot>/overlay-containers/<id>/userdata/.
func crioContainerBundleDir(runRoot, containerID string) string {
	return filepath.Join(runRoot, "overlay-containers", containerID, "userdata")
}

// ---------------------------------------------------------------------------
// Spoofed container helpers
// ---------------------------------------------------------------------------

// buildSupplementFromSpecAnnotations creates a criContainerSupplement from
// OCI spec annotations. This is used for spoofed containers that CRI cannot see.
func buildSupplementFromSpecAnnotations(spec *runtimespec.Spec) *criContainerSupplement {
	if spec == nil {
		return nil
	}
	ann := spec.Annotations
	if len(ann) == 0 {
		return nil
	}
	labels := map[string]string{}
	for _, key := range []string{
		"io.kubernetes.container.name",
		"io.kubernetes.pod.name",
		"io.kubernetes.pod.namespace",
		"io.kubernetes.pod.uid",
	} {
		if v, ok := ann[key]; ok {
			labels[key] = v
		}
	}
	return &criContainerSupplement{
		podSandboxID: ann["io.kubernetes.cri-o.SandboxID"],
		image:        ann["io.kubernetes.cri-o.ImageName"],
		imageRef:     ann["io.kubernetes.cri-o.ImageRef"],
		name:         ann["io.kubernetes.cri-o.ContainerName"],
		labels:       labels,
		annotations:  ann,
	}
}

func existingPath(path string) string {
	return runtime.ExistingPath(path)
}

// ---------------------------------------------------------------------------
// Conmon process discovery
// ---------------------------------------------------------------------------

type conmonProcessInfo struct {
	pid        uint32
	binaryPath string
	cmdline    []string
}

// getConmonProcessInfo walks the process tree upward from the container PID
// looking for a conmon process (CRI-O's container monitor).
func getConmonProcessInfo(procReader *sysinfo.ProcReader, taskPID uint32) *conmonProcessInfo {
	if taskPID == 0 || procReader == nil {
		return nil
	}
	currentPID := int(taskPID)
	for depth := 0; depth < 3; depth++ {
		ppid, err := procReader.GetProcessPPID(currentPID)
		if err != nil || ppid <= 0 {
			return nil
		}
		exePath, _ := procReader.ReadExePath(ppid)
		cmdline, _ := procReader.ReadCmdlineRaw(ppid)
		if isConmonProcess(exePath, cmdline) {
			return &conmonProcessInfo{
				pid:        uint32(ppid),
				binaryPath: exePath,
				cmdline:    cmdline,
			}
		}
		currentPID = ppid
	}
	return nil
}

func isConmonProcess(exePath string, cmdline []string) bool {
	base := filepath.Base(exePath)
	if base == "conmon" || strings.HasPrefix(base, "conmon") {
		return true
	}
	if len(cmdline) > 0 {
		base = filepath.Base(cmdline[0])
		if base == "conmon" || strings.HasPrefix(base, "conmon") {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Container name helpers
// ---------------------------------------------------------------------------

func containerName(name string, labels map[string]string, id string) string {
	if k8sName, ok := labels["io.kubernetes.container.name"]; ok {
		return k8sName
	}
	if name != "" {
		return name
	}
	if n, ok := labels["name"]; ok {
		return n
	}
	if len(id) >= 12 {
		return id[:12]
	}
	return id
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

// ---------------------------------------------------------------------------
// OCI spec helpers
// ---------------------------------------------------------------------------

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
		return runtime.ParseEnvVars(spec.Process.Env)
	}
	if criStatus != nil && len(criStatus.Envs) > 0 {
		envs := make([]runtime.EnvVar, 0, len(criStatus.Envs))
		for _, e := range criStatus.Envs {
			envs = append(envs, runtime.EnvVar{
				Key:          e.Key,
				Value:        e.Value,
				IsKubernetes: runtime.IsKubernetesEnvKey(e.Key),
			})
		}
		return envs
	}
	return nil
}

func inferCGroupDriver(path string) string {
	return runtime.InferCGroupDriver(path)
}

var nsPathFromSpec = runtime.NsPathFromSpec

// ---------------------------------------------------------------------------
// Network conversion
// ---------------------------------------------------------------------------

var convertNetworkStats = runtime.ConvertNetworkStats

// ---------------------------------------------------------------------------
// Disk usage helper
// ---------------------------------------------------------------------------

func dirUsage(path string) runtime.ContainerRWLayerStats {
	size, inodes := runtime.DirDiskUsage(path)
	return runtime.ContainerRWLayerStats{
		RWLayerUsage:  size,
		RWLayerInodes: inodes,
	}
}

// ---------------------------------------------------------------------------
// RootFS resolution
// ---------------------------------------------------------------------------

// resolveSpecRootPath resolves the OCI spec root.path. Per OCI spec, if
// the path is relative it is resolved relative to the bundle directory.
func resolveSpecRootPath(rootPath, bundleDir string) string {
	if filepath.IsAbs(rootPath) {
		return rootPath
	}
	return filepath.Join(bundleDir, rootPath)
}

func resolveRootFSPath(rt *Runtime, pid uint32) string {
	if pid == 0 || rt.mountReader == nil {
		return ""
	}
	mounts, err := rt.mountReader.ReadMounts(int(pid))
	if err != nil {
		return ""
	}
	rootMount := rt.mountReader.FindRootMount(mounts)
	if rootMount == nil {
		return ""
	}
	if _, upperdir, _ := rt.mountReader.ParseOverlayFS(rootMount); upperdir != "" {
		return upperdir
	}
	return rootMount.Source
}

// ---------------------------------------------------------------------------
// PodInfo type (needed by client.go ListPods)
// ---------------------------------------------------------------------------

type PodInfo = runtime.PodInfo

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
