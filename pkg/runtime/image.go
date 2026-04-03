package runtime

import "time"

type ImageInfo struct {
	Names     []string
	Digest    string
	Size      int64
	CreatedAt time.Time
}

// ImageManifest describes a single platform manifest resolved from the image.
type ImageManifest struct {
	Digest     string
	Platform   string // e.g. "linux/amd64"
	Path       string // on-disk path of the manifest blob (when known)
	ConfigPath string // on-disk path of the config blob (when known)
}

// ImageConfigInfo contains the image-config fields surfaced in storage and summary views.
type ImageConfigInfo struct {
	TargetMediaType string
	TargetKind      string
	Schema          string
	StorageBackend  ImageBackendType

	// IndexPath is the on-disk path of the image index blob.
	// Empty when the image is a single-platform manifest (not an index).
	IndexPath string

	// Manifest is the manifest for the current/matched platform.
	Manifest *ImageManifest
	// Manifests lists all platform manifests when the image is an index.
	Manifests []*ImageManifest
}

// ImageLayer contains the layer fields rendered by the rootfs layers view.
type ImageLayer struct {
	Index              int
	CompressedDigest   string
	UncompressedDigest string
	Size               int64
	CompressionType    string

	// Path is the resolved filesystem path for this layer when the runtime can determine it.
	Path        string
	UsageSize   int64
	UsageInodes int64

	Containerd *ImageContainerdLayer
	Crio       *ImageCRIOLayer
	Docker     *ImageDockerLayer
}

// ImageDockerLayer contains Docker-specific layer fields (classic image store).
type ImageDockerLayer struct {
	CacheID       string
	GraphDriver   string
	ShortLinkID   string
	ShortLinkPath string
}

type ImageContainerdLayer struct {
	ContentPath string
	SnapshotKey string
}

// ImageCRIOLayer contains CRI-O/containers-storage specific layer fields.
type ImageCRIOLayer struct {
	ID            string
	Metadata      map[string]string
	Names         []string
	OverlayLinkID string
}

type LayerBackendKind string

const (
	LayerBackendDockerGraphDriver     LayerBackendKind = "docker-graphdriver"
	LayerBackendContainerdSnapshotter LayerBackendKind = "containerd-snapshotter"
	LayerBackendContainersStorage     LayerBackendKind = "containers-storage"
)

type LayerBackend struct {
	Kind LayerBackendKind
	Name string
}

type ContainerStorage struct {
	ReadOnlyLayers []*ImageLayer

	RWLayerPath string
	ReadOnly    bool
	Backend     *LayerBackend

	Containerd *ContainerdContainerStorage
	Docker     *DockerContainerStorage
	Crio       *CRIOContainerStorage
}

type ContainerdContainerStorage struct {
	Snapshotter   string
	RWSnapshotKey string
}

type DockerContainerStorage struct {
	GraphDriver   string
	Snapshotter   string
	RWSnapshotKey string
	RWLayerID     string
}

type CRIOContainerStorage struct {
	StorageDriver string
	RWLayerID     string
}
