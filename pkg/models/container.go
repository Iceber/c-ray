package models

import "time"

// Container represents a container instance
type Container struct {
	ID        string
	Name      string
	Image     string
	ImageID   string
	Status    ContainerStatus
	CreatedAt time.Time
	StartedAt time.Time
	PID       uint32 // Host PID of the container's main process

	// Pod information (extracted from labels)
	PodName      string
	PodNamespace string
	PodUID       string

	// Labels from containerd
	Labels map[string]string
}

// ContainerStatus represents the container state
type ContainerStatus string

const (
	ContainerStatusCreated ContainerStatus = "created"
	ContainerStatusRunning ContainerStatus = "running"
	ContainerStatusPaused  ContainerStatus = "paused"
	ContainerStatusStopped ContainerStatus = "stopped"
	ContainerStatusUnknown ContainerStatus = "unknown"
)

// ContainerDetail contains detailed information about a container
type ContainerDetail struct {
	Container

	// Process information
	ProcessCount int
	Processes    []*Process

	// CGroup information
	CGroupPath    string
	CGroupVersion int // 1 or 2
	CGroupLimits  *CGroupLimits

	// Image information
	ImageName         string
	ImageLayers       []string // Snapshot keys
	SnapshotKey       string   // Container's active snapshot key (RW layer)
	ReadOnlyLayerPath string
	WritableLayerPath string
	MountCount        int
	Mounts            []*Mount

	// Runtime information
	ShimPID       uint32
	OCIBundlePath string
	OCIRuntimeDir string
	Namespaces    map[string]string // ns type -> ns path
	Snapshotter   string            // Snapshotter name (e.g., overlayfs, native)

	// RW Layer usage information
	RWLayerSize   int64 // Content size of RW layer
	RWLayerUsage  int64 // Actual disk usage of RW layer
	RWLayerInodes int64 // Number of inodes used by RW layer

	// Network information
	IPAddress    string
	PortMappings []*PortMapping
}

// CGroupLimits contains cgroup resource limits
type CGroupLimits struct {
	CPUQuota  int64 // CPU quota in microseconds
	CPUPeriod int64 // CPU period in microseconds
	CPUShares int64 // CPU shares

	MemoryLimit int64 // Memory limit in bytes
	MemoryUsage int64 // Current memory usage in bytes

	PidsLimit   int64 // Max number of PIDs
	PidsCurrent int64 // Current number of PIDs

	BlkioWeight uint16 // Block IO weight
}

// Mount represents a filesystem mount
type Mount struct {
	Source      string
	Destination string
	Type        string
	Options     []string
}

// PortMapping represents a port mapping
type PortMapping struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string // tcp or udp
}
