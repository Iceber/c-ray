package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// ConvertProcesses converts models.Process to runtime.Process.
func ConvertProcesses(procs []*models.Process) []*Process {
	if len(procs) == 0 {
		return nil
	}
	out := make([]*Process, 0, len(procs))
	for _, p := range procs {
		out = append(out, &Process{
			PID:     p.PID,
			PPID:    p.PPID,
			Command: p.Command,
			Args:    append([]string(nil), p.Args...),
			State:   p.State,
		})
	}
	return out
}

// ConvertProcessStats converts a models.Process (with stats) to runtime.ProcessStats.
func ConvertProcessStats(p *models.Process) *ProcessStats {
	if p == nil {
		return nil
	}
	ps := &ProcessStats{
		Process: Process{
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
		ps.Children = make([]*ProcessStats, 0, len(p.Children))
		for _, child := range p.Children {
			ps.Children = append(ps.Children, ConvertProcessStats(child))
		}
	}
	return ps
}

// ConvertNetworkStats converts models.NetworkStats to runtime.NetworkStats.
func ConvertNetworkStats(stats []*models.NetworkStats) []*NetworkStats {
	if len(stats) == 0 {
		return nil
	}
	out := make([]*NetworkStats, 0, len(stats))
	for _, s := range stats {
		out = append(out, &NetworkStats{
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

// NsPathFromSpec extracts the namespace path for a given type from an OCI spec.
func NsPathFromSpec(spec *runtimespec.Spec, nsType string) string {
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

// InferCGroupDriver guesses the cgroup driver from a cgroup path.
func InferCGroupDriver(cgroupPath string) string {
	if cgroupPath == "" {
		return ""
	}
	if strings.Contains(cgroupPath, ".slice") || strings.Contains(cgroupPath, ".scope") {
		return "systemd"
	}
	return "cgroupfs"
}

// ParseEnvVars converts "KEY=VALUE" env entries to EnvVar slices.
func ParseEnvVars(envEntries []string) []EnvVar {
	envs := make([]EnvVar, 0, len(envEntries))
	for _, entry := range envEntries {
		key, value, found := strings.Cut(entry, "=")
		if !found || key == "" {
			continue
		}
		envs = append(envs, EnvVar{
			Key:          key,
			Value:        value,
			IsKubernetes: IsKubernetesEnvKey(key),
		})
	}
	return envs
}

// IsKubernetesEnvKey returns true for Kubernetes-injected environment variable keys.
func IsKubernetesEnvKey(key string) bool {
	return strings.HasPrefix(key, "KUBERNETES_") ||
		strings.HasPrefix(key, "POD_") ||
		strings.HasPrefix(key, "SERVICE_")
}

// DirDiskUsage walks a directory and returns total size and inode count.
func DirDiskUsage(dir string) (int64, int64) {
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

// ExistingPath returns path if it exists on disk, otherwise "".
func ExistingPath(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
	}
	return ""
}

// SpecToV1Mounts converts OCI spec mounts to runtime.Mount.
func SpecToV1Mounts(specMounts []runtimespec.Mount) []*Mount {
	if len(specMounts) == 0 {
		return nil
	}
	out := make([]*Mount, 0, len(specMounts))
	for _, m := range specMounts {
		out = append(out, &Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        m.Type,
			Options:     append([]string(nil), m.Options...),
		})
	}
	return out
}

// ModelMountsToV1 converts models.Mount to runtime.Mount.
func ModelMountsToV1(mounts []*models.Mount) []*Mount {
	if len(mounts) == 0 {
		return nil
	}
	out := make([]*Mount, 0, len(mounts))
	for _, m := range mounts {
		out = append(out, &Mount{
			Source:      m.Source,
			Destination: m.Destination,
			Type:        m.Type,
			Options:     append([]string(nil), m.Options...),
		})
	}
	return out
}

// ---------------------------------------------------------------------------
// Process collection helpers (backend-agnostic)
// ---------------------------------------------------------------------------

// CollectProcesses calls the collector and converts the result.
// Returns an error if the collector is nil.
func CollectProcesses(collector *sysinfo.ProcessCollector, pid uint32) ([]*Process, error) {
	if collector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}
	procs, err := collector.CollectContainerProcesses(pid)
	if err != nil {
		return nil, err
	}
	return ConvertProcesses(procs), nil
}

// CollectProcessStats returns stats for the root process of the container.
func CollectProcessStats(collector *sysinfo.ProcessCollector, pid uint32, cgroupPath string) (*ProcessStats, error) {
	if collector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}
	top, err := collector.CollectProcessTop(pid, cgroupPath)
	if err != nil {
		return nil, err
	}
	if len(top.Processes) == 0 {
		return nil, nil
	}
	return ConvertProcessStats(top.Processes[0]), nil
}

// CollectSingleProcessStats returns stats for a specific process (identified by
// pidStr) inside the container whose root PID is containerPID.
func CollectSingleProcessStats(collector *sysinfo.ProcessCollector, containerPID uint32, cgroupPath string, pidStr string) (*ProcessStats, error) {
	if collector == nil {
		return nil, fmt.Errorf("process collector not initialized")
	}
	targetPID, err := strconv.Atoi(pidStr)
	if err != nil || targetPID <= 0 {
		return nil, fmt.Errorf("invalid process pid %s", pidStr)
	}
	top, err := collector.CollectProcessTop(containerPID, cgroupPath, targetPID)
	if err != nil {
		return nil, err
	}
	if len(top.Processes) > 0 {
		return ConvertProcessStats(top.Processes[0]), nil
	}
	return nil, fmt.Errorf("process %s not found", pidStr)
}
