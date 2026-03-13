package models

import "time"

// EnvVar represents one process environment variable.
type EnvVar struct {
	Key          string
	Value        string
	IsKubernetes bool
}

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
	Environment  []EnvVar
	SharedPID    *bool
	RestartCount *uint32
	ExitedAt     time.Time
	ExitCode     *int32
	ExitReason   string

	// CGroup information
	CGroupPath    string
	CGroupVersion int // 1 or 2
	CGroupLimits  *CGroupLimits

	// Image information
	ImageName         string
	ImageConfig       *ImageConfigInfo
	ImageLayers       []string // Snapshot keys
	SnapshotKey       string   // Container's active snapshot key (RW layer)
	ReadOnlyLayerPath string
	WritableLayerPath string
	MountCount        int
	Mounts            []*Mount

	// Runtime information
	ShimPID        uint32
	OCIBundlePath  string
	OCIRuntimeDir  string
	Namespaces     map[string]string // ns type -> ns path
	Snapshotter    string            // Snapshotter name (e.g., overlayfs, native)
	RuntimeProfile *RuntimeProfile

	// RW Layer usage information
	RWLayerSize   int64 // Content size of RW layer
	RWLayerUsage  int64 // Actual disk usage of RW layer
	RWLayerInodes int64 // Number of inodes used by RW layer

	// Network information
	IPAddress    string
	PortMappings []*PortMapping
	PodNetwork   *PodNetworkInfo
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
	HostPath    string
	LiveSource  string
	Origin      MountOrigin
	State       MountState
	Note        string
}

// MountOrigin describes which subsystem contributed the mount row.
type MountOrigin string

const (
	MountOriginCRI            MountOrigin = "cri"
	MountOriginRuntimeDefault MountOrigin = "runtime-default"
	MountOriginLiveExtra      MountOrigin = "live-extra"
)

// MountState describes whether a mount was declared, observed live, or both.
type MountState string

const (
	MountStateDeclaredLive MountState = "declared+live"
	MountStateDeclaredOnly MountState = "declared-only"
	MountStateLiveOnly     MountState = "live-only"
)

// PortMapping represents a port mapping
type PortMapping struct {
	HostIP        string
	HostPort      uint16
	ContainerPort uint16
	Protocol      string // tcp or udp
}

// DNSConfig describes Pod-level DNS settings from CRI sandbox config.
type DNSConfig struct {
	Domain   string
	Servers  []string
	Searches []string
	Options  []string
}

// CNIInterfaceAddress describes one CNI-assigned address in CIDR form.
type CNIInterfaceAddress struct {
	CIDR    string
	Gateway string
	Family  string
}

// CNIInterface describes one interface returned by CNI result.
type CNIInterface struct {
	Name       string
	MAC        string
	Sandbox    string
	PciID      string
	SocketPath string
	Addresses  []*CNIInterfaceAddress
}

// CNIRoute describes one route returned by CNI result.
type CNIRoute struct {
	Destination string
	Gateway     string
}

// CNIResultInfo contains normalized network data from CNI result.
type CNIResultInfo struct {
	Interfaces []*CNIInterface
	Routes     []*CNIRoute
	DNS        *DNSConfig
}

// PodNetworkInfo contains PodSandbox-scoped network metadata collected from CRI.
type PodNetworkInfo struct {
	SandboxID          string
	SandboxState       string
	PrimaryIP          string
	AdditionalIPs      []string
	HostNetwork        bool
	NamespaceMode      string
	NamespaceTargetID  string
	NetNSPath          string
	Hostname           string
	DNS                *DNSConfig
	PortMappings       []*PortMapping
	RuntimeHandler     string
	RuntimeType        string
	StatusSource       string
	ConfigSource       string
	NamespaceSource    string
	CNI                *CNIResultInfo
	ObservedInterfaces []*NetworkStats
	Warnings           []string
}

// RuntimeProfile contains structured runtime-specific information.
type RuntimeProfile struct {
	OCI    *OCIInfo
	Shim   *ShimInfo
	CGroup *CGroupInfo
	RootFS *RootFSInfo
}

// OCIInfo contains OCI runtime and bundle metadata.
type OCIInfo struct {
	RuntimeName     string
	RuntimeBinary   string
	StateDir        string
	BundleDir       string
	ConfigPath      string
	SandboxID       string
	ConfigSource    string
	StateDirSource  string
	BundleDirSource string
	RuntimeSource   string
}

// ShimInfo contains serving shim process metadata.
type ShimInfo struct {
	PID              uint32
	BinaryPath       string
	SocketAddress    string
	Cmdline          []string
	BundleDir        string
	SandboxBundleDir string
	Source           string
}

// CGroupInfo contains cgroup v2 metadata for the task.
type CGroupInfo struct {
	RelativePath string
	AbsolutePath string
	Version      int
	Driver       string
	Source       string
}

// RootFSInfo contains runtime root filesystem paths.
type RootFSInfo struct {
	BundleRootFSPath string
	MountRootFSPath  string
	Source           string
}
