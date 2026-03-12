package sysinfo

import (
	"sync"
	"time"

	"github.com/icebergu/c-ray/pkg/models"
)

// clkTck is the number of clock ticks per second (USER_HZ).
// On virtually all Linux systems this is 100.
const clkTck = 100

// processSnapshot stores raw counters from one sampling point.
type processSnapshot struct {
	UTime      uint64
	STime      uint64
	ReadBytes  uint64
	WriteBytes uint64
	Timestamp  time.Time
}

// netSnapshot stores raw counters for one network interface.
type netSnapshot struct {
	RxBytes   uint64
	TxBytes   uint64
	Timestamp time.Time
}

// Sampler calculates CPU%, IO rate and network rate by comparing
// two successive snapshots taken at different points in time.
// It tracks a containerID so that switching containers automatically
// invalidates stale snapshots (container-internal PIDs and interface
// names like eth0 are not globally unique).
type Sampler struct {
	mu          sync.Mutex
	containerID string
	prevProcess map[int]*processSnapshot // PID -> previous snapshot
	prevNetwork map[string]*netSnapshot  // interface name -> previous snapshot
}

// NewSampler creates a new sampler.
func NewSampler() *Sampler {
	return &Sampler{
		prevProcess: make(map[int]*processSnapshot),
		prevNetwork: make(map[string]*netSnapshot),
	}
}

// reset clears all historical snapshots.
func (s *Sampler) reset() {
	s.prevProcess = make(map[int]*processSnapshot)
	s.prevNetwork = make(map[string]*netSnapshot)
}

// ensureContainer resets snapshots if the container has changed.
// Must be called with s.mu held.
func (s *Sampler) ensureContainer(containerID string) {
	if s.containerID != containerID {
		s.reset()
		s.containerID = containerID
	}
}

// CalculateProcessRates computes CPU% and IO rate for each process
// by comparing with the previous snapshot, then stores the current
// snapshot for the next call.
//
// containerID identifies which container the data belongs to; a change
// in containerID automatically resets the snapshot cache.
// cpuCores is the container's CPU limit (quota/period). Pass 0 for unlimited.
// memoryLimit is the container's memory limit in bytes. Pass 0 for unlimited.
func (s *Sampler) CalculateProcessRates(containerID string, processes []*models.Process, cpuCores float64, memoryLimit int64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.ensureContainer(containerID)

	now := time.Now()
	currentPIDs := make(map[int]struct{}, len(processes))

	for _, p := range processes {
		currentPIDs[p.PID] = struct{}{}

		cur := &processSnapshot{
			UTime:      p.UTime,
			STime:      p.STime,
			ReadBytes:  p.ReadBytes,
			WriteBytes: p.WriteBytes,
			Timestamp:  now,
		}

		if prev, ok := s.prevProcess[p.PID]; ok {
			dt := now.Sub(prev.Timestamp).Seconds()
			if dt >= 0.1 { // guard against near-zero interval
				// CPU%: delta ticks / (dt * clkTck) gives fraction of one core.
				deltaCPU := (p.UTime + p.STime) - (prev.UTime + prev.STime)
				rawCoreFraction := float64(deltaCPU) / (dt * clkTck)
				if cpuCores > 0 {
					// Normalize to limit: 100% means using all of the allocated CPU.
					p.CPUPercent = rawCoreFraction / cpuCores * 100.0
				} else {
					// No limit: percentage relative to a single core.
					p.CPUPercent = rawCoreFraction * 100.0
				}
				if p.CPUPercent > 100.0*maxFloat(cpuCores, 1) {
					p.CPUPercent = 100.0 * maxFloat(cpuCores, 1)
				}

				// IO rates
				if p.ReadBytes >= prev.ReadBytes {
					p.ReadBytesPerSec = float64(p.ReadBytes-prev.ReadBytes) / dt
				}
				if p.WriteBytes >= prev.WriteBytes {
					p.WriteBytesPerSec = float64(p.WriteBytes-prev.WriteBytes) / dt
				}
			}
		}

		// Memory percent relative to container limit
		if memoryLimit > 0 && p.MemoryRSS > 0 {
			p.MemoryPercent = float64(p.MemoryRSS) / float64(memoryLimit) * 100.0
		}

		s.prevProcess[p.PID] = cur
	}

	// Prune disappeared PIDs
	for pid := range s.prevProcess {
		if _, alive := currentPIDs[pid]; !alive {
			delete(s.prevProcess, pid)
		}
	}
}

func maxFloat(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

// CalculateNetworkRates computes RX/TX rates and stores the current snapshot.
func (s *Sampler) CalculateNetworkRates(stats []*models.NetworkStats) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	currentIfaces := make(map[string]struct{}, len(stats))

	for _, ns := range stats {
		currentIfaces[ns.Interface] = struct{}{}

		cur := &netSnapshot{
			RxBytes:   ns.RxBytes,
			TxBytes:   ns.TxBytes,
			Timestamp: now,
		}

		if prev, ok := s.prevNetwork[ns.Interface]; ok {
			dt := now.Sub(prev.Timestamp).Seconds()
			if dt >= 0.1 {
				if ns.RxBytes >= prev.RxBytes {
					ns.RxBytesPerSec = float64(ns.RxBytes-prev.RxBytes) / dt
				}
				if ns.TxBytes >= prev.TxBytes {
					ns.TxBytesPerSec = float64(ns.TxBytes-prev.TxBytes) / dt
				}
			}
		}

		s.prevNetwork[ns.Interface] = cur
	}

	// Prune disappeared interfaces
	for iface := range s.prevNetwork {
		if _, alive := currentIfaces[iface]; !alive {
			delete(s.prevNetwork, iface)
		}
	}
}
