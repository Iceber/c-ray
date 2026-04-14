package runtime

import (
	"strings"

	"github.com/icebergu/c-ray/pkg/sysinfo"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

// cgroupRootPath is the detected cgroup filesystem mount point, initialised
// once at package init. All backends use this value instead of hard-coding
// "/sys/fs/cgroup".
var cgroupRootPath = "/sys/fs/cgroup"

// CGroupRootPath returns the cgroup filesystem mount point (singleton).
func CGroupRootPath() string {
	return cgroupRootPath
}

// ResolveCGroupPath returns a cgroupfs path that can be used to read live
// stats. When a running PID is available the path is always read from
// /proc/[pid]/cgroup (works for both v1 and v2, regardless of cgroup driver).
// Only when procfs is unavailable (stopped container) do we fall back to
// normalizing the configured path based on its format (systemd vs cgroupfs).
func ResolveCGroupPath(configuredPath string, version int, pid uint32, procReader *sysinfo.ProcReader) string {
	if pid > 0 && procReader != nil {
		if livePath, err := procReader.ReadCGroupPath(int(pid)); err == nil && livePath != "" {
			return normalizeAbsoluteCGroupPath(livePath)
		}
	}
	return NormalizeCGroupPath(configuredPath)
}

// NormalizeCGroupPath converts config-level cgroup paths into a cgroupfs path.
// This handles both plain cgroupfs paths and systemd unit syntax.
func NormalizeCGroupPath(cgroupPath string) string {
	cgroupPath = strings.TrimSpace(cgroupPath)
	if cgroupPath == "" {
		return ""
	}

	if InferCGroupDriver(cgroupPath) == "systemd" && strings.Contains(cgroupPath, ":") {
		if path := systemdCGroupPath(cgroupPath); path != "" {
			return path
		}
	}

	return normalizeAbsoluteCGroupPath(cgroupPath)
}

func normalizeAbsoluteCGroupPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	return "/" + strings.TrimPrefix(path, "/")
}

func systemdCGroupPath(cgroupPath string) string {
	parts := strings.SplitN(cgroupPath, ":", 3)
	if len(parts) != 3 {
		return ""
	}

	slice := strings.TrimSpace(parts[0])
	prefix := strings.TrimSpace(parts[1])
	name := strings.TrimSpace(parts[2])
	if slice == "" || name == "" {
		return ""
	}

	base := expandSystemdSlicePath(slice)
	if base == "" {
		return ""
	}

	unit := name
	if !strings.HasSuffix(unit, ".slice") && !strings.HasSuffix(unit, ".scope") {
		if prefix != "" {
			unit = prefix + "-" + unit
		}
		unit += ".scope"
	}

	return base + "/" + unit
}

func expandSystemdSlicePath(slice string) string {
	slice = strings.TrimSpace(strings.TrimPrefix(slice, "/"))
	if slice == "" {
		return ""
	}
	if slice == "-.slice" {
		return "/"
	}
	if !strings.HasSuffix(slice, ".slice") || strings.Contains(slice, "/") {
		return ""
	}

	name := strings.TrimSuffix(slice, ".slice")
	if name == "" {
		return ""
	}

	parts := strings.Split(name, "-")
	var path strings.Builder
	for i := range parts {
		if parts[i] == "" {
			return ""
		}
		path.WriteString("/")
		path.WriteString(strings.Join(parts[:i+1], "-"))
		path.WriteString(".slice")
	}
	return path.String()
}

// ContainerCGroupInfo aggregates container config–level cgroup data together
// with live resource statistics read from the cgroup filesystem.
type ContainerCGroupInfo struct {
	// --- config-level (from OCI spec / CRI) ---
	Version  int
	Driver   string
	Path     string
	RootPath string

	SpecResources *CGroupSpecResources `json:",omitempty"`

	// --- live stats (from cgroups filesystem) ---
	CPUQuota  int64  // CPU quota in usec (-1 = unlimited for v1, 0 = unset)
	CPUPeriod uint64 // CPU period in usec
	CPUWeight uint64 // CPU shares (v1) or weight (v2)

	CPUUsageUsec  uint64
	CPUUserUsec   uint64
	CPUSystemUsec uint64
	CPUNrPeriods  uint64
	CPUThrottled  uint64

	MemoryUsage uint64
	MemoryLimit uint64
	MemorySwap  uint64

	PidsCurrent uint64
	PidsLimit   uint64

	BlkioWeight uint16

	// Controllers lists the active cgroup controllers (e.g. cpu, memory, pids).
	Controllers []string
}

// CGroupSpecResources captures the user-declared resource constraints
// extracted from the OCI runtime spec or Docker HostConfig.
type CGroupSpecResources struct {
	CPUShares          *uint64           `json:",omitempty"`
	CPUQuota           *int64            `json:",omitempty"`
	CPUPeriod          *uint64           `json:",omitempty"`
	CPUBurst           *uint64           `json:",omitempty"`
	CPUSetCPUs         string            `json:",omitempty"`
	CPUSetMems         string            `json:",omitempty"`
	CPURealtimeRuntime *int64            `json:",omitempty"`
	CPURealtimePeriod  *uint64           `json:",omitempty"`
	MemoryLimit        *int64            `json:",omitempty"`
	MemoryReservation  *int64            `json:",omitempty"`
	MemorySwap         *int64            `json:",omitempty"`
	MemorySwappiness   *uint64           `json:",omitempty"`
	OOMKillDisable     *bool             `json:",omitempty"`
	PidsLimit          *int64            `json:",omitempty"`
	BlkioWeight        *uint16           `json:",omitempty"`
	Unified            map[string]string `json:",omitempty"`
}

// ExtractSpecResources builds CGroupSpecResources from an OCI runtime spec.
func ExtractSpecResources(spec *runtimespec.Spec) *CGroupSpecResources {
	if spec == nil || spec.Linux == nil || spec.Linux.Resources == nil {
		return nil
	}
	res := spec.Linux.Resources
	sr := &CGroupSpecResources{}
	hasValue := false

	if cpu := res.CPU; cpu != nil {
		if cpu.Shares != nil {
			sr.CPUShares = cpu.Shares
			hasValue = true
		}
		if cpu.Quota != nil {
			sr.CPUQuota = cpu.Quota
			hasValue = true
		}
		if cpu.Period != nil {
			sr.CPUPeriod = cpu.Period
			hasValue = true
		}
		if cpu.Burst != nil {
			sr.CPUBurst = cpu.Burst
			hasValue = true
		}
		if cpu.Cpus != "" {
			sr.CPUSetCPUs = cpu.Cpus
			hasValue = true
		}
		if cpu.Mems != "" {
			sr.CPUSetMems = cpu.Mems
			hasValue = true
		}
		if cpu.RealtimeRuntime != nil {
			sr.CPURealtimeRuntime = cpu.RealtimeRuntime
			hasValue = true
		}
		if cpu.RealtimePeriod != nil {
			sr.CPURealtimePeriod = cpu.RealtimePeriod
			hasValue = true
		}
	}

	if mem := res.Memory; mem != nil {
		if mem.Limit != nil {
			sr.MemoryLimit = mem.Limit
			hasValue = true
		}
		if mem.Reservation != nil {
			sr.MemoryReservation = mem.Reservation
			hasValue = true
		}
		if mem.Swap != nil {
			sr.MemorySwap = mem.Swap
			hasValue = true
		}
		if mem.Swappiness != nil {
			sr.MemorySwappiness = mem.Swappiness
			hasValue = true
		}
		if mem.DisableOOMKiller != nil {
			sr.OOMKillDisable = mem.DisableOOMKiller
			hasValue = true
		}
	}

	if pids := res.Pids; pids != nil && pids.Limit != nil {
		sr.PidsLimit = pids.Limit
		hasValue = true
	}

	if blkio := res.BlockIO; blkio != nil && blkio.Weight != nil {
		sr.BlkioWeight = blkio.Weight
		hasValue = true
	}

	if len(res.Unified) > 0 {
		sr.Unified = res.Unified
		hasValue = true
	}

	if !hasValue {
		return nil
	}
	return sr
}

// PopulateCGroupStatsFromReader fills basic cgroup limits and CPU counters
// from the filesystem reader. This keeps Docker, CRI-O and containerd
// fallback logic consistent.
func PopulateCGroupStatsFromReader(info *ContainerCGroupInfo, cgroupReader *sysinfo.CGroupReader) {
	if info == nil || cgroupReader == nil || info.Path == "" {
		return
	}

	if limits, err := cgroupReader.ReadCGroupLimits(info.Path); err == nil {
		info.CPUQuota = limits.CPUQuota
		info.CPUPeriod = uint64(limits.CPUPeriod)
		info.CPUWeight = uint64(limits.CPUShares)
		info.MemoryUsage = uint64(limits.MemoryUsage)
		info.MemoryLimit = uint64(limits.MemoryLimit)
		info.PidsCurrent = uint64(limits.PidsCurrent)
		info.PidsLimit = uint64(limits.PidsLimit)
		info.BlkioWeight = limits.BlkioWeight
	}

	if cpuStats, err := cgroupReader.ReadCGroupCPUStats(info.Path); err == nil {
		info.CPUUsageUsec = cpuStats.UsageUsec
		info.CPUUserUsec = cpuStats.UserUsec
		info.CPUSystemUsec = cpuStats.SystemUsec
		info.CPUNrPeriods = cpuStats.NrPeriods
		info.CPUThrottled = cpuStats.NrThrottled
	}
}
