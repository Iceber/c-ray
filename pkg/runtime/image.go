package runtime

import "time"

type ImageInfo struct {
	Name      string
	Digest    string
	Size      int64
	CreatedAt time.Time
}

// ImageConfigInfo contains the image-config fields surfaced in storage and summary views.
type ImageConfigInfo struct {
	ContentPath     string
	TargetMediaType string
	TargetKind      string
	Schema          string
	StorageBackend  ImageBackendType
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
