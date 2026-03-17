package runtime

import "context"

// ObjectRuntime is the decoupled runtime surface for future call sites.
//
// Instead of repeatedly passing container or image IDs back into one large
// service, callers first resolve stable resource handles and then invoke
// resource-scoped methods on those handles. This shape allows implementations
// to cache immutable metadata such as OCI spec, environment, image references,
// namespace maps, snapshotter identity, or image config behind each handle,
// while keeping live data retrieval explicit on methods like RuntimeInfo,
// Processes, Top, and Mounts.
type Runtime interface {
	Connect(ctx context.Context) error
	Close() error

	ContainerRuntime
	ImageRuntime
	PodRuntime
}

// ContainerRuntime resolves container handles.
type ContainerRuntime interface {
	ListContainers(ctx context.Context) ([]Container, error)
	GetContainer(ctx context.Context, id string) (Container, error)
}

// ImageRuntime resolves image handles.
type ImageRuntime interface {
	ListImages(ctx context.Context) ([]Image, error)
	GetImage(ctx context.Context, ref string) (Image, error)
}

// PodRuntime resolves pod handles.
type PodRuntime interface {
	ListPods(ctx context.Context) ([]Pod, error)
}

// Container is a resource handle bound to one container identity.
//
// Metadata and State are intentionally split so implementations can cache
// immutable or rarely-changing metadata while keeping live process- and
// runtime-state retrieval explicit.
type Container interface {
	ID() string

	CRIInfo()
	OCISepc()

	Info(ctx context.Context) (*ContainerInfo, error)
	Config(ctx context.Context) (*ContainerConfig, error)

	Network(ctx context.Context) (*ContainerNetworkState, error)
	Storage(ctx context.Context) (*ContainerStorage, error)

	Mounts(ctx context.Context) ([]*Mount, error)
	Runtime(ctx context.Context) (*RuntimeProfile, error)

	State(ctx context.Context) (*ContainerState, error)

	RWLayerStats(ctx context.Context) (ContainerRWLayerStats, error)

	Processes(ctx context.Context) ([]*Process, error)
	ProcessStats(ctx context.Context) (*ProcessStats, error)
	GetProcessStats(ctx context.Context, pid string) (*ProcessStats, error)

	Image(ctx context.Context) (Image, error)
}

// Image is a resource handle bound to one image reference or digest.
type Image interface {
	Ref() string
	Info(ctx context.Context) (*ImageInfo, error)
	Config(ctx context.Context) (*ImageConfigInfo, error)
	Layers(ctx context.Context, query LayerQuery) ([]*ImageLayer, error)
}

// Pod is a resource handle bound to one pod identity.
type Pod interface {
	UID() string
	Info(ctx context.Context) (*PodInfo, error)
	Containers(ctx context.Context) ([]Container, error)
}

// LayerQuery controls how image layer paths are resolved.
type LayerQuery struct {
	// Snapshotter selects the snapshotter to inspect, for example overlayfs.
	Snapshotter string

	// RWSnapshotKey optionally lets the implementation resolve layer paths from
	// the container's writable layer mount context.
	RWSnapshotKey string
}

// Config contains runtime configuration
type Config struct {
	// Socket path for the runtime (e.g., /run/containerd/containerd.sock)
	SocketPath string

	// Namespace for containerd (default: "k8s.io")
	Namespace string

	// Timeout for operations
	Timeout int // seconds

	// StorageRoot is the graph root for containers/storage (CRI-O).
	// Default: /var/lib/containers/storage
	StorageRoot string

	// StorageRunRoot is the run root for containers/storage (CRI-O).
	// Default: /run/containers/storage
	StorageRunRoot string
}
