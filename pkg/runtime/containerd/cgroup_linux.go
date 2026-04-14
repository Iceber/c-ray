//go:build linux

package containerd

import (
	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

// loadCGroupStats populates live cgroup statistics into info.
// On Linux with cgroupv2 it uses the containerd/cgroups/v3 manager;
// for cgroupv1 it uses the sysinfo CGroupReader for both limits and
// cpuacct/cpu.stat counters.
func loadCGroupStats(info *runtime.ContainerCGroupInfo, cgroupReader *sysinfo.CGroupReader) {
	if info.Version == 2 {
		if loadCGroupV2Stats(info, cgroupReader) {
			return
		}
	}

	// cgroup v1, or v2 Load failed — use sysinfo reader.
	loadCGroupV1Stats(info, cgroupReader)
}

func loadCGroupV2Stats(info *runtime.ContainerCGroupInfo, cgroupReader *sysinfo.CGroupReader) bool {
	mgr, err := cgroup2.Load(info.Path)
	if err != nil {
		return false
	}

	if controllers, err := mgr.Controllers(); err == nil {
		info.Controllers = controllers
	}
	if metrics, err := mgr.Stat(); err == nil {
		if cpu := metrics.GetCPU(); cpu != nil {
			info.CPUUsageUsec = cpu.GetUsageUsec()
			info.CPUUserUsec = cpu.GetUserUsec()
			info.CPUSystemUsec = cpu.GetSystemUsec()
			info.CPUNrPeriods = cpu.GetNrPeriods()
			info.CPUThrottled = cpu.GetNrThrottled()
		}
		if mem := metrics.GetMemory(); mem != nil {
			info.MemoryUsage = mem.GetUsage()
			info.MemoryLimit = mem.GetUsageLimit()
			info.MemorySwap = mem.GetSwapUsage()
		}
		if pids := metrics.GetPids(); pids != nil {
			info.PidsCurrent = pids.GetCurrent()
			info.PidsLimit = pids.GetLimit()
		}
	}

	// CPU limits (quota/period/weight) from sysinfo, which reads cpu.max
	// and cpu.weight for v2.
	runtime.PopulateCGroupStatsFromReader(info, cgroupReader)
	return true
}

func loadCGroupV1Stats(info *runtime.ContainerCGroupInfo, cgroupReader *sysinfo.CGroupReader) {
	runtime.PopulateCGroupStatsFromReader(info, cgroupReader)
}
