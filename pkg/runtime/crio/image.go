package crio

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/icebergu/c-ray/pkg/runtime"
	cstorage "go.podman.io/storage"
)

// imageHandle implements runtime.Image backed by containers/storage metadata.
type imageHandle struct {
	rt  *Runtime
	id  string
	ref string

	// from containers/storage (primary)
	names     []string
	digests   []string
	topLayer  string
	createdAt time.Time

	// lazy-loaded layer metadata from containers/storage
	layerOnce sync.Once
	layers    []*runtime.ImageLayer
	layerErr  error
}

func (r *Runtime) newImageHandle(img *cstorage.Image) *imageHandle {
	h := &imageHandle{
		rt:        r,
		id:        img.ID,
		topLayer:  img.TopLayer,
		names:     append([]string(nil), img.Names...),
		digests:   digestsToStrings(img.Digests),
		createdAt: img.Created,
	}
	if len(h.names) > 0 {
		h.ref = h.names[0]
	} else if len(h.digests) > 0 {
		h.ref = h.digests[0]
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
	if len(h.digests) > 0 {
		digest = h.digests[0]
	}
	name := h.ref
	if len(h.names) > 0 {
		name = h.names[0]
	}
	var size int64
	if store, err := h.rt.getStore(); err == nil {
		if s, err := store.ImageSize(h.id); err == nil {
			size = s
		}
	}
	return &runtime.ImageInfo{
		Name:      name,
		Digest:    digest,
		Size:      size,
		CreatedAt: h.createdAt,
	}, nil
}

func (h *imageHandle) Config(_ context.Context) (*runtime.ImageConfigInfo, error) {
	digest := ""
	if len(h.digests) > 0 {
		digest = h.digests[0]
	}
	return &runtime.ImageConfigInfo{
		TargetMediaType: "application/vnd.oci.image.manifest.v1+json",
		TargetKind:      "Manifest",
		Schema:          "OCI",
		ContentPath:     digest,
	}, nil
}

func (h *imageHandle) Layers(ctx context.Context, _ runtime.LayerQuery) ([]*runtime.ImageLayer, error) {
	h.layerOnce.Do(func() {
		h.layers, h.layerErr = h.loadLayers(ctx)
	})
	return h.layers, h.layerErr
}

// loadLayers reads layer metadata from containers/storage.
func (h *imageHandle) loadLayers(_ context.Context) ([]*runtime.ImageLayer, error) {
	store, err := h.rt.getStore()
	if err != nil {
		return nil, err
	}

	img, err := h.lookupStorageImage(store)
	if err != nil {
		return nil, err
	}
	if img.TopLayer == "" {
		return nil, nil
	}

	var chain []*runtime.ImageLayer
	for layerID := img.TopLayer; layerID != ""; {
		layer, err := store.Layer(layerID)
		if err != nil {
			return nil, fmt.Errorf("lookup layer %s: %w", layerID, err)
		}
		rtLayer := h.rt.convertStorageLayerToRuntime(store, layer)
		if rtLayer == nil {
			break
		}
		chain = append(chain, rtLayer)
		layerID = layer.Parent
	}

	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	for i := range chain {
		chain[i].Index = i
	}
	return chain, nil
}

// Compile-time interface check.
var _ runtime.Image = (*imageHandle)(nil)
