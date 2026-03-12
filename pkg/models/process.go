package models

// Process represents a process in a container
type Process struct {
	PID     int
	PPID    int
	Command string
	Args    []string
	State   string

	// CPU time (raw ticks from /proc/[pid]/stat, used for rate calculation)
	UTime uint64 // User mode CPU time in clock ticks
	STime uint64 // System mode CPU time in clock ticks

	// Resource usage (calculated from sampling)
	CPUPercent    float64
	MemoryPercent float64
	MemoryRSS     uint64 // Resident Set Size in bytes
	MemoryVMS     uint64 // Virtual Memory Size in bytes

	// IO statistics (cumulative from /proc/[pid]/io)
	ReadBytes  uint64
	WriteBytes uint64
	ReadOps    uint64
	WriteOps   uint64

	// IO rates (calculated from sampling)
	ReadBytesPerSec  float64
	WriteBytesPerSec float64

	// Children processes
	Children []*Process
}

// ProcessTop represents top-like process information
type ProcessTop struct {
	Processes   []*Process
	NetworkIO   []*NetworkStats // Container-level network IO (per interface)
	Timestamp   int64           // Unix timestamp
	CPUCores    float64         // CPU cores limit (0 = unlimited)
	MemoryLimit int64           // Memory limit in bytes (0 = unlimited)
}

// NetworkStats represents network IO statistics for a single interface
type NetworkStats struct {
	Interface string

	// Cumulative counters
	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
	RxErrors  uint64
	TxErrors  uint64
	RxDropped uint64
	TxDropped uint64

	// Rates (calculated from sampling)
	RxBytesPerSec float64
	TxBytesPerSec float64
}
