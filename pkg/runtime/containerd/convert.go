package containerd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/containerd/containerd/v2/core/containers"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// ---------------------------------------------------------------------------
// Shim process discovery
// ---------------------------------------------------------------------------

type shimProcessInfo struct {
	pid        uint32
	binaryPath string
	cmdline    []string
}

func getShimProcessInfo(procReader *sysinfo.ProcReader, taskPID uint32) *shimProcessInfo {
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
		if isShimProcess(exePath, cmdline) {
			return &shimProcessInfo{
				pid:        uint32(ppid),
				binaryPath: exePath,
				cmdline:    cmdline,
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

// ---------------------------------------------------------------------------
// Shim socket resolution
// ---------------------------------------------------------------------------

func resolveShimSocketAddress(bundleDir, containerID, sandboxIDHint, namespace string) string {
	if address, err := readBootstrapAddress(filepath.Join(bundleDir, "bootstrap.json")); err == nil {
		return address
	}
	if address, err := readAddressFile(filepath.Join(bundleDir, "address")); err == nil {
		return address
	}

	sandboxID := sandboxIDHint
	if sandboxID == "" {
		if data, err := os.ReadFile(filepath.Join(bundleDir, "sandbox")); err == nil {
			sandboxID = strings.TrimSpace(string(data))
		}
	}
	if sandboxID != "" {
		sandboxBundleDir := filepath.Join(filepath.Dir(bundleDir), sandboxID)
		if address, err := readBootstrapAddress(filepath.Join(sandboxBundleDir, "bootstrap.json")); err == nil {
			return address
		}
		if address, err := readAddressFile(filepath.Join(sandboxBundleDir, "address")); err == nil {
			return address
		}
		return computeShimSocketAddress(namespace, sandboxID)
	}

	return computeShimSocketAddress(namespace, containerID)
}

func resolveShimSandboxBundleDir(bundleDir, sandboxID string) string {
	if sandboxID == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(bundleDir), sandboxID)
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

func computeShimSocketAddress(namespace, id string) string {
	path := runtimeV2StateBase + "/" + namespace + "/" + id
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("unix:///run/containerd/s/%x", sum)
}

func existingPathCheck(path string) string {
	if _, err := os.Stat(path); err == nil {
		return path
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
// Pod network building
// ---------------------------------------------------------------------------

func (h *containerHandle) buildPodNetwork(ctx context.Context, info containers.Container) *runtime.PodNetworkInfo {
	podNet := &runtime.PodNetworkInfo{
		SandboxID: info.SandboxID,
	}

	var pid uint32
	if task, err := h.raw.Task(ctx, nil); err == nil {
		pid = task.Pid()
	}
	if pid > 0 && h.rt.procReader != nil {
		if stats, err := h.rt.procReader.ReadNetDev(int(pid)); err == nil {
			podNet.ObservedInterfaces = convertNetworkStats(stats)
		}
	}

	// Try to get netns path from spec namespace map.
	if h.spec != nil {
		if path := nsPathFromSpec(h.spec, "network"); path != "" {
			podNet.NetNSPath = path
		}
	}

	if info.SandboxID == "" {
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

	if err := h.rt.criClient.ApplyPodSandboxNetwork(ctx, info.SandboxID, podNet); err != nil {
		podNet.Warnings = append(podNet.Warnings, fmt.Sprintf("cri pod sandbox status failed: %v", err))
		if cri.ShouldAttachPodNetwork(podNet) {
			return podNet
		}
		return nil
	}

	// Fallback netns from spec if still unresolved.
	if podNet.NetNSPath == "" && h.spec != nil {
		if path := nsPathFromSpec(h.spec, "network"); path != "" {
			podNet.NetNSPath = path
		}
	}

	if cri.ShouldAttachPodNetwork(podNet) {
		return podNet
	}
	return nil
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
