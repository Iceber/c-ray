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

// buildNsPIDToHostPIDMap builds a mapping from container-namespace PIDs to
// host PIDs by scanning host /proc for processes that share the same PID
// namespace as the container's init process (containerPID).
//
// The algorithm:
//  1. Resolve the PID namespace inode of containerPID via /proc/<pid>/ns/pid.
//  2. Iterate all host PIDs; for each that lives in the same namespace, read
//     NSpid: from /proc/<hostPID>/status. The final entry in NSpid is the
//     innermost-namespace (container) PID.
func (c *ProcessCollector) buildNsPIDToHostPIDMap(containerPID uint32) map[int]int {
	containerNS, err := c.procReader.ReadPIDNamespaceInode(int(containerPID))
	if err != nil {
		return nil
	}

	hostPIDs, err := c.procReader.ListPIDs()
	if err != nil {
		return nil
	}

	m := make(map[int]int, len(hostPIDs)/4)
	for _, hostPID := range hostPIDs {
		nsInode, err := c.procReader.ReadPIDNamespaceInode(hostPID)
		if err != nil || nsInode != containerNS {
			continue
		}
		nsPIDs, err := c.procReader.ReadNSpid(hostPID)
		if err != nil || len(nsPIDs) == 0 {
			continue
		}
		// The last entry is the PID as seen inside the container namespace.
		nsPID := nsPIDs[len(nsPIDs)-1]
		m[nsPID] = hostPID
	}
	return m
}

// applyHostPIDs fills HostPID and HostPPID on each process using the
// provided nsPID-to-hostPID map. Unknown parents whose namespace PID is
// not present in the map (e.g. the container init whose shim parent lives
// outside the namespace, showing as PPID=0 inside) are left as zero by
// this function; callers should follow up with resolveHostPIDs to fill
// those gaps via the host procfs.
func applyHostPIDs(procs []*models.Process, nsPIDToHostPID map[int]int) {
	if nsPIDToHostPID == nil {
		return
	}
	for _, p := range procs {
		if hpid, ok := nsPIDToHostPID[p.PID]; ok {
			p.HostPID = hpid
		}
		if hppid, ok := nsPIDToHostPID[p.PPID]; ok {
			p.HostPPID = hppid
		}
	}
}

// resolveHostPIDs applies the nsPID-to-hostPID namespace map and then fills
// any remaining HostPPID gaps via a direct host-procfs lookup.
//
// Why a second pass is needed: processes whose parent lives outside the
// container PID namespace (always true for the container init / PID-1) have
// PPID=0 in their stat file as seen from the container procfs. That key is
// absent from the namespace map, so HostPPID stays 0 after applyHostPIDs.
// The fallback reads /proc/<HostPID>/stat on the host, which shows the real
// parent (e.g. the shim) and fills HostPPID correctly.
func (c *ProcessCollector) resolveHostPIDs(procs []*models.Process, containerPID uint32) {
	applyHostPIDs(procs, c.buildNsPIDToHostPIDMap(containerPID))

	// Fallback: for any process where HostPID is known but HostPPID is still
	// zero, read the PPID from the host procfs. This covers:
	//   • The container init (PID 1), whose parent (shim) is outside the ns.
	//   • Any process whose parent exited mid-scan and was not in the map.
	for _, p := range procs {
		if p.HostPID > 0 && p.HostPPID == 0 {
			if ppid, err := c.procReader.GetProcessPPID(p.HostPID); err == nil && ppid > 0 {
				p.HostPPID = ppid
			}
		}
	}
}

// CollectContainerProcesses collects all processes for a container.
// containerPID is the main process PID of the container (host namespace).
func (c *ProcessCollector) CollectContainerProcesses(containerPID uint32) ([]*models.Process, error) {
	// For containers, we need to read processes from the container's namespace
	// This is done by reading /proc/[containerPID]/root/proc
	containerProcRoot := fmt.Sprintf("/proc/%d/root/proc", containerPID)

	// Create a proc reader for the container's proc filesystem
	containerProcReader := NewProcReaderWithRoot(containerProcRoot)

	// List all PIDs in the container
	pids, err := containerProcReader.ListPIDs()
	if err != nil {
		// Fallback: just return the main process read from the host proc.
		// In this path the PID is already a host PID.
		process, err := c.procReader.ReadProcess(int(containerPID))
		if err != nil {
			return nil, err
		}
		process.HostPID = process.PID
		if ppid, err := c.procReader.GetProcessPPID(process.PID); err == nil {
			process.HostPPID = ppid
		}
		return []*models.Process{process}, nil
	}

	// Build process tree using container-namespace proc reader.
	tree := NewProcessTree(containerProcReader)
	if err := tree.Build(pids); err != nil {
		return nil, err
	}

	procs := tree.GetAllProcesses()

	// Resolve host PIDs for every process in the container namespace.
	c.resolveHostPIDs(procs, containerPID)

	return procs, nil
}

// CollectProcessTop collects top-like process information with CPU%, IO rate,
// memory percent, and container-level network IO.
func (c *ProcessCollector) CollectProcessTop(containerPID uint32, cgroupPath string, targetPIDs ...int) (*models.ProcessTop, error) {
	processes, err := c.collectTopProcesses(containerPID, targetPIDs...)
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

func (c *ProcessCollector) collectTopProcesses(containerPID uint32, targetPIDs ...int) ([]*models.Process, error) {
	if len(targetPIDs) == 0 || targetPIDs[0] <= 0 {
		return c.CollectContainerProcesses(containerPID)
	}

	containerProcRoot := fmt.Sprintf("/proc/%d/root/proc", containerPID)
	containerProcReader := NewProcReaderWithRoot(containerProcRoot)

	process, err := containerProcReader.ReadProcess(targetPIDs[0])
	if err != nil {
		return nil, err
	}

	procs := []*models.Process{process}
	c.resolveHostPIDs(procs, containerPID)
	return procs, nil
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
