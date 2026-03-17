package crio

import (
	"context"
	"sync"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
	runtimeapi "k8s.io/cri-api/pkg/apis/runtime/v1"
)

// imageHandle implements runtime.Image backed by CRI image data and
// filesystem-based containers/storage layer inspection.
type imageHandle struct {
	rt  *Runtime
	id  string
	ref string

	// from CRI ListImages / ImageStatus (available immediately)
	repoTags    []string
	repoDigests []string
	size        uint64

	// lazy-loaded layer metadata from containers/storage
	layerOnce sync.Once
	layers    []*runtime.ImageLayer
	layerErr  error
}

func (r *Runtime) newImageHandle(img *runtimeapi.Image) *imageHandle {
	h := &imageHandle{
		rt:          r,
		id:          img.GetId(),
		repoTags:    img.GetRepoTags(),
		repoDigests: img.GetRepoDigests(),
		size:        img.GetSize(),
	}
	if len(h.repoTags) > 0 {
		h.ref = h.repoTags[0]
	} else if len(h.repoDigests) > 0 {
		h.ref = h.repoDigests[0]
	} else {
		h.ref = h.id
	}
	return h
}

// ---------------------------------------------------------------------------
// runtime.Image
// ---------------------------------------------------------------------------

func (h *imageHandle) Ref() string { return h.ref }

func (h *imageHandle) Info(_ context.Context) (*runtime.ImageInfo, error) {
	digest := ""
	if len(h.repoDigests) > 0 {
		digest = h.repoDigests[0]
	}
	name := h.ref
	if len(h.repoTags) > 0 {
		name = h.repoTags[0]
	}
	return &runtime.ImageInfo{
		Name:      name,
		Digest:    digest,
		Size:      int64(h.size),
		CreatedAt: time.Time{}, // CRI ListImages doesn't expose creation time
	}, nil
}

func (h *imageHandle) Config(_ context.Context) (*runtime.ImageConfigInfo, error) {
	ociDigest := ""
	if len(h.repoDigests) > 0 {
		ociDigest = h.repoDigests[0]
	}
	return &runtime.ImageConfigInfo{
		TargetMediaType: "application/vnd.oci.image.manifest.v1+json",
		TargetKind:      "Manifest",
		Schema:          "OCI",
		ContentPath:     ociDigest,
	}, nil
}

func (h *imageHandle) Layers(ctx context.Context, _ runtime.LayerQuery) ([]*runtime.ImageLayer, error) {
	h.layerOnce.Do(func() {
		h.layers, h.layerErr = h.loadLayers(ctx)
	})
	return h.layers, h.layerErr
}

// loadLayers reads layer metadata from containers/storage overlay metadata files.
func (h *imageHandle) loadLayers(_ context.Context) ([]*runtime.ImageLayer, error) {
	reader := newStorageReader(h.rt.storageRoot, defaultGraphDriver)

	images, err := reader.readImageRecords()
	if err != nil {
		return nil, err
	}

	// Find the matching image by ID.
	var topLayerID string
	for _, img := range images {
		if img.ID == h.id || matchesRef(img, h.ref) {
			topLayerID = img.TopLayer
			break
		}
	}
	if topLayerID == "" {
		return nil, nil
	}

	allLayers, err := reader.readLayerRecords()
	if err != nil {
		return nil, err
	}

	layerIndex := make(map[string]*storageLayerRecord, len(allLayers))
	for i := range allLayers {
		layerIndex[allLayers[i].ID] = &allLayers[i]
	}

	// Walk the layer chain from top to base.
	var chain []*storageLayerRecord
	for id := topLayerID; id != ""; {
		lr, ok := layerIndex[id]
		if !ok {
			break
		}
		chain = append(chain, lr)
		id = lr.Parent
	}

	// Reverse so chain[0] is the base layer (matches OCI convention: index 0 = bottom).
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}

	layers := make([]*runtime.ImageLayer, len(chain))
	for i, lr := range chain {
		layers[i] = &runtime.ImageLayer{
			Index:              i,
			CompressedDigest:   lr.CompressedDigest,
			UncompressedDigest: lr.DiffDigest,
			Size:               lr.DiffSize,
			CompressionType:    compressionName(lr.Compression),
			Path:               reader.layerDiffPath(lr.ID),
			ImageCRIOLayer: runtime.ImageCRIOLayer{
				StorageID: lr.ID,
			},
		}
	}
	return layers, nil
}

// matchesRef checks if an image record matches the given reference.
func matchesRef(img storageImageRecord, ref string) bool {
	for _, name := range img.Names {
		if name == ref {
			return true
		}
	}
	return false
}

func compressionName(compression int) string {
	switch compression {
	case 1:
		return "gzip"
	case 2:
		return "zstd"
	default:
		return ""
	}
}

// Compile-time interface check.
var _ runtime.Image = (*imageHandle)(nil)
