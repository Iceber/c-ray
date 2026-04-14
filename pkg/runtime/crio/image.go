package crio

import (
	"context"
	"encoding/base64"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
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
	names := append([]string(nil), h.names...)
	if len(names) == 0 {
		names = []string{h.ref}
	}
	var size int64
	if store, err := h.rt.getStore(); err == nil {
		if s, err := store.ImageSize(h.id); err == nil {
			size = s
		}
	}
	return &runtime.ImageInfo{
		Names:     names,
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

	info := &runtime.ImageConfigInfo{
		StorageBackend: runtime.ImageBackendCRIO,
	}

	if store, err := h.rt.getStore(); err == nil {
		if img, err := h.lookupStorageImage(store); err == nil {
			resolved := resolveImageConfigInfo(store, img)
			info.TargetMediaType = resolved.mediaType
			info.TargetKind = resolved.kind
			info.Schema = resolved.schema
			info.IndexPath = resolved.indexPath
			info.Manifest = &runtime.ImageManifest{
				Digest:     resolved.manifestDigest,
				Platform:   resolved.platform,
				Path:       resolved.manifestPath,
				ConfigPath: resolved.contentPath,
			}
			info.Manifests = resolved.manifests
		}
	}
	if info.Manifest == nil {
		info.Manifest = &runtime.ImageManifest{ConfigPath: digest}
	}

	return info, nil
}

// imageConfigResolution holds the results of resolving image config from big-data.
type imageConfigResolution struct {
	contentPath string
	platform    string
	mediaType   string
	kind        string
	schema      string

	// indexPath is the on-disk path of the image index blob (empty for single-platform).
	indexPath string
	// manifestDigest is the digest of the current platform manifest blob.
	manifestDigest string
	// manifestPath is the on-disk path of the current platform manifest blob.
	manifestPath string
	// manifests lists all platform manifests when the image is an index.
	manifests []*runtime.ImageManifest
}

func resolveImageConfigInfo(store cstorage.Store, img *cstorage.Image) imageConfigResolution {
	if store == nil || img == nil {
		return imageConfigResolution{}
	}

	imageDir := imageBigDataDir(store, img.ID)

	// CRI-O big-data layout:
	//   "manifest"                  → current platform's manifest (always has Config + Layers)
	//   "manifest-sha256:<hash>"    → index or other platform manifests
	//   "sha256:<hash>"             → config blob (key = config digest from manifest)
	//
	// Since "manifest" is always the current platform's manifest, it reliably
	// provides the config digest for path resolution.

	var indexManifest *ManifestInfo
	var platformManifest *ManifestInfo
	configDigests := make(map[string]struct{})

	for _, name := range img.BigDataNames {
		if !isManifestBigDataName(name) {
			continue
		}
		data, err := store.ImageBigData(img.ID, name)
		if err != nil || len(data) == 0 {
			continue
		}
		item := makeBigDataItem(img, name)
		manifest, ok := parseManifestInfo(item, data)
		if !ok {
			continue
		}
		if manifest.Kind == "index" {
			if indexManifest == nil {
				indexManifest = &manifest
			}
			continue
		}
		if manifest.Config != nil {
			if d := normalizeDigest(manifest.Config.Digest); d != "" {
				configDigests[d] = struct{}{}
			}
			// "manifest" (no suffix) is the current platform's manifest.
			if name == "manifest" || platformManifest == nil {
				platformManifest = &manifest
			}
		}
	}
	res := imageConfigResolution{}
	if indexManifest != nil {
		res.mediaType = indexManifest.MediaType
		res.kind = "Index"
	} else if platformManifest != nil {
		res.mediaType = platformManifest.MediaType
		res.kind = "Manifest"
	}
	res.schema = deriveSchema(res.mediaType)

	// Resolve index on-disk path.
	if indexManifest != nil && imageDir != "" {
		indexBlobPath := filepath.Join(imageDir, imageBigDataBaseName(indexManifest.Name))
		if runtime.ExistingPath(indexBlobPath) != "" {
			res.indexPath = indexBlobPath
		}
	}

	// Resolve platform manifest digest and on-disk path.
	if platformManifest != nil {
		res.manifestDigest = platformManifest.Digest
		if imageDir != "" {
			manifestBlobPath := filepath.Join(imageDir, imageBigDataBaseName(platformManifest.Name))
			if runtime.ExistingPath(manifestBlobPath) != "" {
				res.manifestPath = manifestBlobPath
			}
		}
	}

	// Build all platform manifests from index descriptors.
	if indexManifest != nil && len(indexManifest.Manifests) > 0 {
		for _, desc := range indexManifest.Manifests {
			platform := runtime.FormatPlatform(desc.OS, desc.Architecture, desc.Variant)
			res.manifests = append(res.manifests, &runtime.ImageManifest{
				Digest:   desc.Digest,
				Platform: platform,
			})
		}
	}

	if len(configDigests) == 0 {
		return res
	}

	// Find the config key by matching key name or item digest against
	// config digests extracted from the platform manifest.
	keys := append([]string(nil), img.BigDataNames...)
	sort.Strings(keys)
	for _, name := range keys {
		if isManifestBigDataName(name) || isSignatureBigDataName(name) {
			continue
		}
		keyNorm := normalizeDigest(name)
		itemDigestNorm := ""
		if d, ok := img.BigDataDigests[name]; ok {
			itemDigestNorm = normalizeDigest(d.String())
		}
		_, keyMatch := configDigests[keyNorm]
		_, digestMatch := configDigests[itemDigestNorm]
		if !keyMatch && !digestMatch {
			continue
		}
		configInfo, path, ok := resolveConfigMetadata(store, img, name, imageDir)
		if !ok {
			continue
		}
		if res.platform == "" {
			res.platform = runtime.FormatPlatform(configInfo.OS, configInfo.Architecture, configInfo.Variant)
		}
		if path != "" {
			res.contentPath = path
			return res
		}
	}

	return res
}

// resolveConfigMetadata checks if a big-data key is a valid image config and returns
// the parsed config plus its on-disk path when available.
func resolveConfigMetadata(store cstorage.Store, img *cstorage.Image, name, imageDir string) (ConfigInfo, string, bool) {
	data, err := store.ImageBigData(img.ID, name)
	if err != nil || len(data) == 0 {
		return ConfigInfo{}, "", false
	}
	item := makeBigDataItem(img, name)
	configInfo, ok := parseConfigInfo(item, data)
	if !ok {
		return ConfigInfo{}, "", false
	}
	if imageDir == "" {
		return configInfo, "", true
	}
	path := filepath.Join(imageDir, imageBigDataBaseName(name))
	if runtime.ExistingPath(path) != "" {
		return configInfo, path, true
	}
	return configInfo, "", true
}

func imageBigDataDir(store cstorage.Store, imageID string) string {
	if store == nil || imageID == "" {
		return ""
	}
	imageDir, err := store.ImageDirectory(imageID)
	if err != nil || imageDir == "" {
		return ""
	}
	return filepath.Dir(imageDir)
}

// deriveSchema maps a manifest mediaType to a schema label.
func deriveSchema(mediaType string) string {
	switch {
	case strings.Contains(mediaType, "docker"):
		return "Docker"
	case strings.Contains(mediaType, "oci"):
		return "OCI"
	default:
		return ""
	}
}

// makeBigDataItem builds a BigDataItem from image metadata for a given key.
func makeBigDataItem(img *cstorage.Image, name string) BigDataItem {
	item := BigDataItem{Name: name, Size: -1}
	if size, ok := img.BigDataSizes[name]; ok {
		item.Size = size
	}
	if dgst, ok := img.BigDataDigests[name]; ok {
		item.Digest = dgst.String()
	}
	return item
}

func imageBigDataBaseName(key string) string {
	for _, ch := range key {
		if ch == '.' || (ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'z') {
			continue
		}
		return "=" + base64.StdEncoding.EncodeToString([]byte(key))
	}
	return key
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
