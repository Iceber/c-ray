package models

// Process represents a process in a container
type Process struct {
	PID     int
	PPID    int
	Command string
	Args    []string
	State   string

	// Resource usage
	CPUPercent    float64
	MemoryPercent float64
	MemoryRSS     uint64 // Resident Set Size in bytes
	MemoryVMS     uint64 // Virtual Memory Size in bytes

	// IO statistics
	ReadBytes  uint64
	WriteBytes uint64
	ReadOps    uint64
	WriteOps   uint64

	// Children processes
	Children []*Process
}

// ProcessTop represents top-like process information
type ProcessTop struct {
	Processes []*Process
	Timestamp int64 // Unix timestamp
}
