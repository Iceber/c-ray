package crio

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/icebergu/c-ray/pkg/models"
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

func existingPath(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
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
	if strings.Contains(path, ".slice") {
		return "systemd"
	}
	return "cgroupfs"
}

func nsPathFromSpec(spec *runtimespec.Spec, nsType string) string {
	if spec == nil || spec.Linux == nil {
		return ""
	}
	for _, ns := range spec.Linux.Namespaces {
		if string(ns.Type) == nsType {
			return ns.Path
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Process conversion (models.Process → runtime.Process / runtime.ProcessStats)
// ---------------------------------------------------------------------------

func convertProcesses(procs []*models.Process) []*runtime.Process {
	if len(procs) == 0 {
		return nil
	}
	out := make([]*runtime.Process, 0, len(procs))
	for _, p := range procs {
		out = append(out, &runtime.Process{
			PID:     p.PID,
			PPID:    p.PPID,
			Command: p.Command,
			Args:    append([]string(nil), p.Args...),
			State:   p.State,
		})
	}
	return out
}

func convertProcessStats(p *models.Process) *runtime.ProcessStats {
	if p == nil {
		return nil
	}
	ps := &runtime.ProcessStats{
		Process: runtime.Process{
			PID:     p.PID,
			PPID:    p.PPID,
			Command: p.Command,
			Args:    append([]string(nil), p.Args...),
			State:   p.State,
		},
		CPUPercent:       p.CPUPercent,
		MemoryPercent:    p.MemoryPercent,
		MemoryRSS:        p.MemoryRSS,
		ReadBytes:        p.ReadBytes,
		WriteBytes:       p.WriteBytes,
		ReadBytesPerSec:  p.ReadBytesPerSec,
		WriteBytesPerSec: p.WriteBytesPerSec,
	}
	if len(p.Children) > 0 {
		ps.Children = make([]*runtime.ProcessStats, 0, len(p.Children))
		for _, child := range p.Children {
			ps.Children = append(ps.Children, convertProcessStats(child))
		}
	}
	return ps
}

// ---------------------------------------------------------------------------
// Network conversion
// ---------------------------------------------------------------------------

func convertNetworkStats(stats []*models.NetworkStats) []*runtime.NetworkStats {
	if len(stats) == 0 {
		return nil
	}
	out := make([]*runtime.NetworkStats, 0, len(stats))
	for _, s := range stats {
		out = append(out, &runtime.NetworkStats{
			Interface:     s.Interface,
			RxBytes:       s.RxBytes,
			TxBytes:       s.TxBytes,
			RxPackets:     s.RxPackets,
			TxPackets:     s.TxPackets,
			RxErrors:      s.RxErrors,
			TxErrors:      s.TxErrors,
			RxBytesPerSec: s.RxBytesPerSec,
			TxBytesPerSec: s.TxBytesPerSec,
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Disk usage helper
// ---------------------------------------------------------------------------

func dirUsage(path string) runtime.ContainerRWLayerStats {
	var stat syscall.Stat_t
	var size int64
	var inodes int64
	_ = filepath.Walk(path, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() {
			if err := syscall.Stat(filepath.Join(path, info.Name()), &stat); err == nil {
				size += info.Size()
				inodes++
			} else {
				size += info.Size()
				inodes++
			}
		}
		return nil
	})
	return runtime.ContainerRWLayerStats{
		RWLayerUsage:  size,
		RWLayerInodes: inodes,
	}
}

// ---------------------------------------------------------------------------
// RootFS resolution
// ---------------------------------------------------------------------------

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

var _ = fmt.Sprintf // suppress unused import if needed
