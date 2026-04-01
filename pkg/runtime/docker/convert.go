package docker

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
)

// convertDockerStatus maps Docker container state strings to runtime status.
func convertDockerStatus(state string) runtime.ContainerStatus {
	switch strings.ToLower(state) {
	case "created":
		return runtime.ContainerStatusCreated
	case "running", "restarting":
		return runtime.ContainerStatusRunning
	case "paused":
		return runtime.ContainerStatusPaused
	case "exited", "dead", "removing":
		return runtime.ContainerStatusStopped
	default:
		return runtime.ContainerStatusUnknown
	}
}

// dockerContainerName extracts a display name from Docker container names.
// Docker prepends "/" to names; a container may have multiple names.
func dockerContainerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

// parseDockerEnv converts Docker-style "KEY=VALUE" env strings.
func parseDockerEnv(env []string) []runtime.EnvVar {
	out := make([]runtime.EnvVar, 0, len(env))
	for _, e := range env {
		k, v, _ := strings.Cut(e, "=")
		isK8s := strings.HasPrefix(k, "KUBERNETES_") ||
			strings.HasPrefix(k, "HOSTNAME") ||
			strings.HasPrefix(k, "KUBERNETES_SERVICE_")
		out = append(out, runtime.EnvVar{
			Key:          k,
			Value:        v,
			IsKubernetes: isK8s,
		})
	}
	return out
}

// inferDockerCGroupDriver guesses the cgroup driver from the cgroup parent path.
func inferDockerCGroupDriver(cgroupParent string) string {
	if cgroupParent == "" {
		return ""
	}
	if strings.Contains(cgroupParent, ".slice") || strings.Contains(cgroupParent, ".scope") {
		return "systemd"
	}
	return "cgroupfs"
}

// convertProcesses converts models.Process to runtime.Process.
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

// convertProcessStats converts a models.Process (with stats) to runtime.ProcessStats.
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

// convertNetworkStats converts models.NetworkStats to runtime.NetworkStats.
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

// dirDiskUsage walks a directory and returns total size and inode count.
func dirDiskUsage(dir string) (int64, int64) {
	var totalSize, totalInodes int64
	_ = filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // best-effort
		}
		totalInodes++
		if !info.IsDir() {
			totalSize += info.Size()
		}
		return nil
	})
	return totalSize, totalInodes
}
