package models

import "time"

// Image represents a container image
type Image struct {
	Name      string
	Digest    string
	Size      int64
	CreatedAt time.Time
	Labels    map[string]string

	// Layer information
	Layers []string // Layer digests
}

// ImageConfigInfo holds metadata about the image config
type ImageConfigInfo struct {
	Digest          string
	ContentPath     string
	Size            int64
	TargetMediaType string
	TargetKind      string
	Schema          string
}

// ImageLayer represents a single layer in an image
type ImageLayer struct {
	Index              int    // Layer index (0=base, n=top)
	Label              string // Layer label: "base", "mid", "top"
	CompressedDigest   string // Manifest digest (compressed)
	UncompressedDigest string // RootFS Diff ID (uncompressed)
	Size               int64  // Content size (compressed) from manifest
	CompressionType    string // Compression type: "gzip", "zstd", "", etc.
	ContentPath        string // Content store blob path
	SnapshotKey        string // Snapshotter key (Chain ID)
	SnapshotPath       string // Filesystem path to snapshot (from metadata.db)
	SnapshotExists     bool   // Whether snapshot is unpacked
	UsageSize          int64  // Actual disk usage size (uncompressed)
	UsageInodes        int64  // Number of inodes used by this layer
}
