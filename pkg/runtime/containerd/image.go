package containerd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// imageHandle implements runtime.Image.
//
// Image metadata (config, manifest, diffIDs) is immutable once pushed and is
// loaded lazily then cached for the lifetime of the handle.
type imageHandle struct {
	rt    *Runtime
	raw   client.Image
	ref   string
	names []string

	metaOnce sync.Once
	meta     *imageMeta
	metaErr  error
}

// imageMeta groups the parsed image metadata that is resolved together.
type imageMeta struct {
	target       ocispec.Descriptor
	configDesc   ocispec.Descriptor
	manifestDesc ocispec.Descriptor // the per-platform manifest descriptor (same as target when not index)
	manifest     ocispec.Manifest
	platform     string
	diffIDs      []digest.Digest
}

func newImageHandle(rt *Runtime, raw client.Image) *imageHandle {
	return &imageHandle{
		rt:    rt,
		raw:   raw,
		ref:   raw.Name(),
		names: []string{raw.Name()},
	}
}

func newGroupedImageHandle(rt *Runtime, raw client.Image, names []string) *imageHandle {
	resolvedNames := append([]string(nil), names...)
	ref := raw.Name()
	if len(resolvedNames) > 0 {
		ref = resolvedNames[0]
	}
	return &imageHandle{
		rt:    rt,
		raw:   raw,
		ref:   ref,
		names: resolvedNames,
	}
}

// ---------------------------------------------------------------------------
// Cache loader
// ---------------------------------------------------------------------------

func (h *imageHandle) ensureMeta(ctx context.Context) {
	h.metaOnce.Do(func() {
		h.meta, h.metaErr = h.loadMeta(ctx)
	})
}

func (h *imageHandle) loadMeta(ctx context.Context) (*imageMeta, error) {
	cs := h.rt.client.ContentStore()

	configDesc, err := h.raw.Config(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get image config: %w", err)
	}

	manifestDesc, manifest, err := getManifestForConfig(ctx, cs, h.raw.Target(), configDesc.Digest)
	if err != nil {
		return nil, fmt.Errorf("failed to get image manifest: %w", err)
	}

	configMeta, err := parseImageConfigMeta(ctx, cs, configDesc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse image config: %w", err)
	}

	return &imageMeta{
		target:       h.raw.Target(),
		configDesc:   configDesc,
		manifestDesc: manifestDesc,
		manifest:     manifest,
		platform:     configMeta.platform,
		diffIDs:      configMeta.diffIDs,
	}, nil
}

// ---------------------------------------------------------------------------
// runtime.Image
// ---------------------------------------------------------------------------

func (h *imageHandle) Ref() string { return h.ref }

func (h *imageHandle) Info(ctx context.Context) (*runtime.ImageInfo, error) {
	size, _ := h.raw.Size(ctx)
	d := ""
	if h.raw.Target().Digest != "" {
		d = h.raw.Target().Digest.String()
	}
	names := append([]string(nil), h.names...)
	if len(names) == 0 {
		names = []string{h.raw.Name()}
	}
	return &runtime.ImageInfo{
		Names:     names,
		Digest:    d,
		Size:      size,
		CreatedAt: h.raw.Metadata().CreatedAt,
	}, nil
}

func (h *imageHandle) Config(ctx context.Context) (*runtime.ImageConfigInfo, error) {
	h.ensureMeta(ctx)
	if h.metaErr != nil {
		return nil, h.metaErr
	}
	m := h.meta
	targetKind, schema := describeImageTarget(m.target.MediaType)

	info := &runtime.ImageConfigInfo{
		TargetMediaType: m.target.MediaType,
		TargetKind:      targetKind,
		Schema:          schema,
		StorageBackend:  runtime.ImageBackendContainerd,
	}

	// IndexPath is set only when the target is an index (multi-platform).
	if !images.IsManifestType(m.target.MediaType) {
		info.IndexPath = contentPath(h.rt.paths.Root, m.target.Digest)
	}

	// Build the current-platform manifest.
	info.Manifest = h.buildCurrentManifest(ctx, m)

	// If the target is an index, enumerate all platform manifests.
	info.Manifests = h.buildAllManifests(ctx, m)

	return info, nil
}

func (h *imageHandle) buildCurrentManifest(_ context.Context, m *imageMeta) *runtime.ImageManifest {
	// Use manifestDesc (the per-platform manifest descriptor), not target,
	// so that Manifest always describes the current platform's manifest
	// rather than the index.
	platform := describePlatform(m.manifestDesc.Platform)
	if platform == "" {
		platform = m.platform
	}
	return &runtime.ImageManifest{
		Digest:     m.manifestDesc.Digest.String(),
		Platform:   platform,
		Path:       contentPath(h.rt.paths.Root, m.manifestDesc.Digest),
		ConfigPath: contentPath(h.rt.paths.Root, m.configDesc.Digest),
	}
}

func (h *imageHandle) buildAllManifests(ctx context.Context, m *imageMeta) []*runtime.ImageManifest {
	cs := h.rt.client.ContentStore()
	target := m.target

	// Only index-type targets contain multiple platform manifests.
	if images.IsManifestType(target.MediaType) {
		// Single manifest — Manifests mirrors the current Manifest.
		return []*runtime.ImageManifest{h.buildCurrentManifest(ctx, m)}
	}

	data, err := content.ReadBlob(ctx, cs, target)
	if err != nil {
		return nil
	}
	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return nil
	}

	result := make([]*runtime.ImageManifest, 0, len(index.Manifests))
	for _, desc := range index.Manifests {
		if !images.IsManifestType(desc.MediaType) {
			continue
		}
		platformStr := describePlatform(desc.Platform)
		manifest := &runtime.ImageManifest{
			Digest:   desc.Digest.String(),
			Platform: platformStr,
			Path:     contentPath(h.rt.paths.Root, desc.Digest),
		}

		// Resolve config path by reading this platform's manifest.
		if platManifest, err := images.Manifest(ctx, cs, desc, nil); err == nil {
			manifest.ConfigPath = contentPath(h.rt.paths.Root, platManifest.Config.Digest)
			if manifest.Platform == "" {
				manifest.Platform = resolveConfigPlatform(ctx, cs, platManifest.Config)
			}
		}

		result = append(result, manifest)
	}
	return result
}

func describePlatform(p *ocispec.Platform) string {
	if p == nil {
		return ""
	}
	s := p.OS + "/" + p.Architecture
	if p.Variant != "" {
		s += "/" + p.Variant
	}
	return s
}

func (h *imageHandle) Layers(ctx context.Context, query runtime.LayerQuery) ([]*runtime.ImageLayer, error) {
	h.ensureMeta(ctx)
	if h.metaErr != nil {
		return nil, h.metaErr
	}
	m := h.meta
	if len(m.manifest.Layers) != len(m.diffIDs) {
		return nil, fmt.Errorf("layer count mismatch: manifest=%d diffIDs=%d",
			len(m.manifest.Layers), len(m.diffIDs))
	}
	return h.buildLayers(ctx, m.manifest, m.diffIDs, query)
}

// ---------------------------------------------------------------------------
// Layer construction
// ---------------------------------------------------------------------------

func (h *imageHandle) buildLayers(ctx context.Context, manifest ocispec.Manifest, diffIDs []digest.Digest, query runtime.LayerQuery) ([]*runtime.ImageLayer, error) {
	snapshotterName := query.Snapshotter
	if snapshotterName == "" {
		snapshotterName = "overlayfs"
	}
	snapshotter := h.rt.client.SnapshotService(snapshotterName)
	chainIDs := calculateChainIDs(diffIDs)
	layerCount := len(manifest.Layers)

	// Resolve RO layer paths. Primary: from the active container's RW layer
	// mount (fast, no side-effects). Fallback: create a temporary view of the
	// topmost committed snapshot via the containerd API — avoids opening
	// metadata.db directly and works while containerd holds its BoltDB lock.
	var roPaths []string
	if query.RWSnapshotKey != "" {
		roPaths = readOnlyLayerPathsFromMounts(ctx, snapshotter, query.RWSnapshotKey)
	} else if snapshotter != nil && layerCount > 0 {
		roPaths = readOnlyLayerPathsViaView(ctx, snapshotter, chainIDs[layerCount-1].String())
	}

	layers := make([]*runtime.ImageLayer, layerCount)
	for i := 0; i < layerCount; i++ {
		chainID := chainIDs[i].String()
		layer := &runtime.ImageLayer{
			Index:              i,
			CompressedDigest:   manifest.Layers[i].Digest.String(),
			UncompressedDigest: diffIDs[i].String(),
			Size:               manifest.Layers[i].Size,
			CompressionType:    compressionType(manifest.Layers[i].MediaType),
			Containerd: &runtime.ImageContainerdLayer{
				ContentPath: contentPath(h.rt.paths.Root, manifest.Layers[i].Digest),
				SnapshotKey: chainID,
			},
		}

		if snapshotter != nil {
			h.populateSnapshotInfo(ctx, snapshotter, layer, chainID, roPaths, layerCount, i)
		}

		layers[i] = layer
	}
	return layers, nil
}

func (h *imageHandle) populateSnapshotInfo(ctx context.Context, snapshotter snapshots.Snapshotter, layer *runtime.ImageLayer, chainID string, roPaths []string, layerCount, i int) {
	if _, err := snapshotter.Stat(ctx, chainID); err != nil {
		return
	}
	// Map RO layer path: roPaths order is [top..base], layers order is [base(0)..top(n-1)].
	if len(roPaths) > 0 && i < len(roPaths) {
		roIndex := len(roPaths) - 1 - i
		if roIndex >= 0 && roIndex < len(roPaths) {
			layer.Path = roPaths[roIndex]
		}
	}
	if usage, err := snapshotter.Usage(ctx, chainID); err == nil {
		layer.UsageSize = usage.Size
		layer.UsageInodes = usage.Inodes
	}
}

// ---------------------------------------------------------------------------
// Image helpers
// ---------------------------------------------------------------------------

func getManifestForConfig(ctx context.Context, cs content.Store, target ocispec.Descriptor, configDigest digest.Digest) (ocispec.Descriptor, ocispec.Manifest, error) {
	if images.IsManifestType(target.MediaType) {
		manifest, err := images.Manifest(ctx, cs, target, nil)
		if err != nil {
			return ocispec.Descriptor{}, ocispec.Manifest{}, err
		}
		if manifest.Config.Digest != configDigest {
			return ocispec.Descriptor{}, ocispec.Manifest{}, fmt.Errorf("manifest config digest mismatch")
		}
		return target, manifest, nil
	}

	data, err := content.ReadBlob(ctx, cs, target)
	if err != nil {
		return ocispec.Descriptor{}, ocispec.Manifest{}, fmt.Errorf("failed to read image index: %w", err)
	}
	var index ocispec.Index
	if err := json.Unmarshal(data, &index); err != nil {
		return ocispec.Descriptor{}, ocispec.Manifest{}, fmt.Errorf("failed to unmarshal image index: %w", err)
	}
	for _, desc := range index.Manifests {
		if !images.IsManifestType(desc.MediaType) {
			continue
		}
		manifest, err := images.Manifest(ctx, cs, desc, nil)
		if err != nil {
			continue
		}
		if manifest.Config.Digest == configDigest {
			return desc, manifest, nil
		}
	}
	return ocispec.Descriptor{}, ocispec.Manifest{}, fmt.Errorf("no manifest found with config digest %s", configDigest)
}

type imageConfigMeta struct {
	platform string
	diffIDs  []digest.Digest
}

func parseImageConfigMeta(ctx context.Context, cs content.Store, configDesc ocispec.Descriptor) (imageConfigMeta, error) {
	data, err := content.ReadBlob(ctx, cs, configDesc)
	if err != nil {
		return imageConfigMeta{}, fmt.Errorf("failed to read config: %w", err)
	}
	var config struct {
		Architecture string `json:"architecture"`
		OS           string `json:"os"`
		Variant      string `json:"variant"`
		RootFS       struct {
			DiffIDs []digest.Digest `json:"diff_ids"`
		} `json:"rootfs"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		return imageConfigMeta{}, err
	}
	return imageConfigMeta{
		platform: runtime.FormatPlatform(config.OS, config.Architecture, config.Variant),
		diffIDs:  config.RootFS.DiffIDs,
	}, nil
}

func resolveConfigPlatform(ctx context.Context, cs content.Store, configDesc ocispec.Descriptor) string {
	meta, err := parseImageConfigMeta(ctx, cs, configDesc)
	if err != nil {
		return ""
	}
	return meta.platform
}

func calculateChainIDs(diffIDs []digest.Digest) []digest.Digest {
	chainIDs := make([]digest.Digest, len(diffIDs))
	for i, diffID := range diffIDs {
		if i == 0 {
			chainIDs[i] = diffID
		} else {
			chainIDs[i] = digest.FromString(chainIDs[i-1].String() + " " + diffID.String())
		}
	}
	return chainIDs
}

func contentPath(rootDir string, dgst digest.Digest) string {
	return fmt.Sprintf("%s/io.containerd.content.v1.content/blobs/%s/%s",
		rootDir, dgst.Algorithm(), dgst.Encoded())
}

func compressionType(mediaType string) string {
	switch mediaType {
	case images.MediaTypeDockerSchema2LayerGzip,
		images.MediaTypeDockerSchema2LayerForeignGzip,
		ocispec.MediaTypeImageLayerGzip:
		return "gzip"
	case images.MediaTypeDockerSchema2LayerZstd, ocispec.MediaTypeImageLayerZstd:
		return "zstd"
	case images.MediaTypeDockerSchema2Layer,
		images.MediaTypeDockerSchema2LayerForeign,
		ocispec.MediaTypeImageLayer:
		return ""
	default:
		if strings.Contains(mediaType, "+gzip") {
			return "gzip"
		}
		if strings.Contains(mediaType, "+zstd") {
			return "zstd"
		}
		return ""
	}
}

func describeImageTarget(mediaType string) (string, string) {
	kind := "Unknown"
	schema := "Unknown"

	if images.IsManifestType(mediaType) {
		kind = "Manifest"
	} else {
		switch mediaType {
		case images.MediaTypeDockerSchema2ManifestList, ocispec.MediaTypeImageIndex:
			kind = "Index"
		}
	}

	switch {
	case strings.Contains(mediaType, "docker"):
		schema = "Docker"
	case strings.Contains(mediaType, "oci"):
		schema = "OCI"
	}

	return kind, schema
}

// Compile-time interface check.
var _ runtime.Image = (*imageHandle)(nil)
