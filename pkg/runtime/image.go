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
}

// ImageLayer contains the layer fields rendered by the rootfs layers view.
type ImageLayer struct {
	Index              int
	CompressedDigest   string
	UncompressedDigest string
	Size               int64
	CompressionType    string

	Path        string
	UsageSize   int64
	UsageInodes int64

	ImageContainerdLayer
	ImageCRIOLayer
}

type ImageContainerdLayer struct {
	ContentPath string
	SnapshotKey string
}

// ImageCRIOLayer contains CRI-O/containers-storage specific layer fields.
type ImageCRIOLayer struct {
	StorageID string
}

type ContainerStorage struct {
	ReadOnlyLayers []*ImageLayer

	RWLayerPath string
}
