package runtime

import "time"

// This file defines the runtime package's trimmed view types.
//
// They are intentionally narrower than pkg/models and are shaped around the
// current object-oriented runtime interfaces plus the fields actually consumed
// by the UI. New fields should be added here only when the runtime interface
// needs to expose them.

// ContainerStatus is the container lifecycle state exposed by runtime handles.
type ContainerStatus string

const (
	ContainerStatusCreated ContainerStatus = "created"
	ContainerStatusRunning ContainerStatus = "running"
	ContainerStatusPaused  ContainerStatus = "paused"
	ContainerStatusStopped ContainerStatus = "stopped"
	ContainerStatusUnknown ContainerStatus = "unknown"
)

// ContainerInfo is a lightweight container summary returned by handle lookup.
//
// It is intentionally small and may mix identity plus inexpensive state needed
// by list or header views. Richer data is split between ContainerMetadata and
// ContainerState.
type ContainerInfo struct {
	ID      string
	Name    string
	Image   string
	ImageID string // 未使用

	PodName      string
	PodNamespace string
	PodUID       string

	PID       uint32
	Status    ContainerStatus
	StartedAt time.Time // 未使用
	CreatedAt time.Time
}

// ContainerMetadata contains cache-friendly container metadata derived from
// sources like containerd info, OCI spec, and CRI config.
type ContainerConfig struct {
	Environment []EnvVar

	CGroupDriver      string
	CGroupPath        string
	CGroupMountedPath string
	CGroupVersion     int

	ImageName string

	Snapshotter       string
	SnapshotKey       string
	ReadOnlyLayerPath string
	WritableLayerPath string
	Namespaces        map[string]string
}

type ContainerState struct {
	Status ContainerStatus

	PID          uint32
	PPID         uint32
	ProcessCount int

	StartedAt    time.Time
	RestartCount *uint32

	ExitedAt   time.Time
	ExitCode   *int32
	ExitReason string
}

// ContainerStorageState contains refreshable storage counters associated with
// the active writable layer and live mount discovery.
type ContainerRWLayerStats struct {
	RWLayerUsage  int64
	RWLayerInodes int64
}

// ContainerNetworkState contains refreshable network inspection data for the
// network tab and related summary surfaces.
type ContainerNetworkState struct {
	PodNetwork *PodNetworkInfo
}

// EnvVar describes one environment variable exposed in process summaries.
type EnvVar struct {
	Key          string
	Value        string
	IsKubernetes bool
}

type Process struct {
	PID     int
	PPID    int
	Command string
	Args    []string
	State   string
}

// Process contains the process fields currently used by the top and tree views.
type ProcessStats struct {
	Process

	CPUPercent       float64
	MemoryPercent    float64
	MemoryRSS        uint64
	ReadBytes        uint64
	WriteBytes       uint64
	ReadBytesPerSec  float64
	WriteBytesPerSec float64
	Children         []*ProcessStats
}

// ProcessTop contains the snapshot used by the live top-style process view.
type ContainerStats struct {
	Processes   []*ProcessStats
	NetworkIO   []*NetworkStats
	Timestamp   int64
	CPUCores    float64
	MemoryLimit int64
}

// RuntimeProfile groups runtime-specific metadata used by runtime and storage views.
type RuntimeProfile struct {
	OCI    *OCIInfo
	Shim   *ContainerdShimInfo
	Conmon *ConmonInfo

	RootFSPath string
}

// ConmonInfo contains the conmon (container monitor) process information
// used by CRI-O runtime.
type ConmonInfo struct {
	PID        uint32
	BinaryPath string
	Cmdline    []string
	LogDriver  string
	LogPath    string
}

// OCIInfo contains the OCI runtime fields currently displayed by the UI.
type OCIInfo struct {
	RuntimeName   string
	RuntimeBinary string
	StateDir      string
	BundleDir     string
	ConfigPath    string
	SandboxID     string
}

// ShimInfo contains the shim process fields currently displayed by the UI.
type ContainerdShimInfo struct {
	BinaryPath       string
	SocketAddress    string
	Cmdline          []string
	SandboxBundleDir string
}
