package sysinfo

import (
	"fmt"
	"sort"
	"time"

	"github.com/icebergu/c-ray/pkg/models"
)

// ProcessTree represents a tree of processes
type ProcessTree struct {
	procReader *ProcReader
	processes  map[int]*models.Process
}

// NewProcessTree creates a new process tree
func NewProcessTree(procReader *ProcReader) *ProcessTree {
	return &ProcessTree{
		procReader: procReader,
		processes:  make(map[int]*models.Process),
	}
}

// Build builds the process tree for given PIDs
func (t *ProcessTree) Build(pids []int) error {
	// Read all processes
	for _, pid := range pids {
		process, err := t.procReader.ReadProcess(pid)
		if err != nil {
			// Skip processes that we can't read
			continue
		}
		t.processes[pid] = process
	}

	// Build parent-child relationships
	for _, process := range t.processes {
		if parent, exists := t.processes[process.PPID]; exists {
			if parent.Children == nil {
				parent.Children = make([]*models.Process, 0)
			}
			parent.Children = append(parent.Children, process)
		}
	}

	return nil
}

// GetRootProcesses returns processes without parents in the tree
func (t *ProcessTree) GetRootProcesses() []*models.Process {
	roots := make([]*models.Process, 0)
	for _, process := range t.processes {
		// A process is a root if its parent is not in our process map
		if _, exists := t.processes[process.PPID]; !exists {
			roots = append(roots, process)
		}
	}

	// Sort by PID
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].PID < roots[j].PID
	})

	return roots
}

// GetAllProcesses returns all processes in the tree
func (t *ProcessTree) GetAllProcesses() []*models.Process {
	processes := make([]*models.Process, 0, len(t.processes))
	for _, process := range t.processes {
		processes = append(processes, process)
	}

	// Sort by PID
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].PID < processes[j].PID
	})

	return processes
}

// GetProcess returns a specific process by PID
func (t *ProcessTree) GetProcess(pid int) (*models.Process, bool) {
	process, exists := t.processes[pid]
	return process, exists
}

// Count returns the number of processes in the tree
func (t *ProcessTree) Count() int {
	return len(t.processes)
}

// ProcessCollector collects process information for containers
type ProcessCollector struct {
	procReader   *ProcReader
	cgroupReader *CGroupReader
	sampler      *Sampler
}

// NewProcessCollector creates a new process collector
func NewProcessCollector() (*ProcessCollector, error) {
	cgroupReader, err := NewCGroupReader()
	if err != nil {
		return nil, err
	}

	return &ProcessCollector{
		procReader:   NewProcReader(),
		cgroupReader: cgroupReader,
		sampler:      NewSampler(),
	}, nil
}

// CollectContainerProcesses collects all processes for a container
// containerPID is the main process PID of the container
func (c *ProcessCollector) CollectContainerProcesses(containerPID uint32) ([]*models.Process, error) {
	// For containers, we need to read processes from the container's namespace
	// This is done by reading /proc/[containerPID]/root/proc
	containerProcRoot := fmt.Sprintf("/proc/%d/root/proc", containerPID)

	// Create a proc reader for the container's proc filesystem
	containerProcReader := NewProcReaderWithRoot(containerProcRoot)

	// List all PIDs in the container
	pids, err := containerProcReader.ListPIDs()
	if err != nil {
		// Fallback: just return the main process
		process, err := c.procReader.ReadProcess(int(containerPID))
		if err != nil {
			return nil, err
		}
		return []*models.Process{process}, nil
	}

	// Build process tree
	tree := NewProcessTree(containerProcReader)
	if err := tree.Build(pids); err != nil {
		return nil, err
	}

	return tree.GetAllProcesses(), nil
}

// CollectProcessTop collects top-like process information with CPU%, IO rate,
// memory percent, and container-level network IO.
func (c *ProcessCollector) CollectProcessTop(containerPID uint32, cgroupPath string) (*models.ProcessTop, error) {
	processes, err := c.CollectContainerProcesses(containerPID)
	if err != nil {
		return nil, err
	}

	top := &models.ProcessTop{
		Processes: processes,
		Timestamp: time.Now().Unix(),
	}

	// Read cgroup limits for CPU/memory context
	if cgroupPath != "" && c.cgroupReader != nil {
		if limits, err := c.cgroupReader.ReadCGroupLimits(cgroupPath); err == nil {
			if limits.CPUQuota > 0 && limits.CPUPeriod > 0 {
				top.CPUCores = float64(limits.CPUQuota) / float64(limits.CPUPeriod)
			}
			top.MemoryLimit = limits.MemoryLimit
		}
	}

	// Calculate CPU%, memory%, and IO rates via sampler
	containerID := fmt.Sprintf("%d", containerPID)
	c.sampler.CalculateProcessRates(containerID, processes, top.CPUCores, top.MemoryLimit)

	// Read container-level network IO from host PID's namespace
	if netStats, err := c.procReader.ReadNetDev(int(containerPID)); err == nil {
		c.sampler.CalculateNetworkRates(netStats)
		top.NetworkIO = netStats
	}

	return top, nil
}

// BuildProcessTree builds a process tree from a list of processes
func BuildProcessTree(processes []*models.Process) *ProcessTree {
	tree := &ProcessTree{
		processes: make(map[int]*models.Process),
	}

	// Add all processes to the map
	for _, process := range processes {
		tree.processes[process.PID] = process
	}

	// Build parent-child relationships
	for _, process := range tree.processes {
		if parent, exists := tree.processes[process.PPID]; exists {
			if parent.Children == nil {
				parent.Children = make([]*models.Process, 0)
			}
			parent.Children = append(parent.Children, process)
		}
	}

	return tree
}

// FilterProcesses filters processes by a predicate function
func FilterProcesses(processes []*models.Process, predicate func(*models.Process) bool) []*models.Process {
	filtered := make([]*models.Process, 0)
	for _, process := range processes {
		if predicate(process) {
			filtered = append(filtered, process)
		}
	}
	return filtered
}

// SortProcessesByMemory sorts processes by memory usage (descending)
func SortProcessesByMemory(processes []*models.Process) {
	sort.Slice(processes, func(i, j int) bool {
		return processes[i].MemoryRSS > processes[j].MemoryRSS
	})
}

// SortProcessesByIO sorts processes by I/O (descending)
func SortProcessesByIO(processes []*models.Process) {
	sort.Slice(processes, func(i, j int) bool {
		return (processes[i].ReadBytes + processes[i].WriteBytes) >
			(processes[j].ReadBytes + processes[j].WriteBytes)
	})
}
