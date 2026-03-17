package models

type Process struct {
	PID     int
	PPID    int
	Command string
	Args    []string
	State   string

	UTime uint64
	STime uint64

	MemoryRSS uint64
	MemoryVMS uint64

	ReadBytes  uint64
	WriteBytes uint64
	ReadOps    uint64
	WriteOps   uint64

	CPUPercent       float64
	MemoryPercent    float64
	ReadBytesPerSec  float64
	WriteBytesPerSec float64

	Children []*Process
}

type ProcessTop struct {
	Processes   []*Process
	NetworkIO   []*NetworkStats
	Timestamp   int64
	CPUCores    float64
	MemoryLimit int64
}

type CGroupLimits struct {
	CPUQuota    int64
	CPUPeriod   int64
	CPUShares   int64
	MemoryLimit int64
	MemoryUsage int64
	PidsLimit   int64
	PidsCurrent int64
	BlkioWeight uint16
}

type Mount struct {
	Source      string
	Destination string
	Type        string
	Options     []string
}

type NetworkStats struct {
	Interface string

	RxBytes   uint64
	TxBytes   uint64
	RxPackets uint64
	TxPackets uint64
	RxErrors  uint64
	TxErrors  uint64
	RxDropped uint64
	TxDropped uint64

	RxBytesPerSec float64
	TxBytesPerSec float64
}
