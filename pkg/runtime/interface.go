package runtime

import (
	"context"

	"github.com/icebergu/c-ray/pkg/models"
)

// Runtime defines the interface for container runtime operations
// This abstraction allows supporting multiple runtimes (containerd, cri-o, docker)
type Runtime interface {
	// Connect establishes connection to the runtime
	Connect(ctx context.Context) error

	// Close closes the connection to the runtime
	Close() error

	// ListContainers returns all containers
	ListContainers(ctx context.Context) ([]*models.Container, error)

	// GetContainer returns a specific container by ID
	GetContainer(ctx context.Context, id string) (*models.Container, error)

	// GetContainerDetail returns overview information for the container detail page
	GetContainerDetail(ctx context.Context, id string) (*models.ContainerDetail, error)

	// GetContainerRuntimeInfo returns runtime/shim/OCI detail for on-demand runtime views
	GetContainerRuntimeInfo(ctx context.Context, id string) (*models.ContainerDetail, error)

	// GetContainerStorageInfo returns storage/snapshot/rootfs detail for on-demand storage views
	GetContainerStorageInfo(ctx context.Context, id string) (*models.ContainerDetail, error)

	// GetContainerNetworkInfo returns network namespace and CNI/CRI detail for on-demand network views
	GetContainerNetworkInfo(ctx context.Context, id string) (*models.ContainerDetail, error)

	// ListImages returns all images
	ListImages(ctx context.Context) ([]*models.Image, error)

	// GetImage returns a specific image by name or digest
	GetImage(ctx context.Context, ref string) (*models.Image, error)

	// GetImageLayers returns detailed layer information for an image (from top to base)
	// snapshotter is the name of the snapshotter to check for layer existence (e.g., "overlayfs", "native")
	// rwSnapshotKey is optional; if provided, layer paths are resolved from the RW layer's mount info
	GetImageLayers(ctx context.Context, imageID string, snapshotter string, rwSnapshotKey string) ([]*models.ImageLayer, error)

	// GetImageConfigInfo returns metadata about the image config
	GetImageConfigInfo(ctx context.Context, imageID string) (*models.ImageConfigInfo, error)

	// ListPods returns all pods (extracted from container labels)
	ListPods(ctx context.Context) ([]*models.Pod, error)

	// GetContainerProcesses returns process information for a container
	GetContainerProcesses(ctx context.Context, id string) ([]*models.Process, error)

	// GetContainerTop returns top-like process information
	GetContainerTop(ctx context.Context, id string) (*models.ProcessTop, error)

	// GetContainerMounts returns mount information for a container
	GetContainerMounts(ctx context.Context, id string) ([]*models.Mount, error)
}

// Config contains runtime configuration
type Config struct {
	// Socket path for the runtime (e.g., /run/containerd/containerd.sock)
	SocketPath string

	// Namespace for containerd (default: "k8s.io")
	Namespace string

	// Timeout for operations
	Timeout int // seconds
}
