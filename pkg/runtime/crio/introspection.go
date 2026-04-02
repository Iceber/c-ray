package crio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/icebergu/c-ray/pkg/runtime"
	digest "github.com/opencontainers/go-digest"
	cstorage "go.podman.io/storage"
	"go.podman.io/storage/pkg/idtools"
	storagetypes "go.podman.io/storage/types"
)

// StoreIntrospector exposes CRI-O store-level data for CLI/test consumers.
type StoreIntrospector interface {
	CRIOStoreInfo(ctx context.Context) (*StoreInfo, error)
}

// ContainerIntrospector exposes CRI-O-specific container storage details.
type ContainerIntrospector interface {
	CRIOContainerInfo(ctx context.Context) (*ContainerInfo, error)
}

// ImageIntrospector exposes CRI-O-specific image storage details.
type ImageIntrospector interface {
	CRIOImageInfo(ctx context.Context) (*ImageInfo, error)
}

// IDMapEntry is a serializable view of user namespace mappings.
type IDMapEntry struct {
	ContainerID int
	HostID      int
	Size        int
}

// BigDataItem summarizes a storage big-data entry.
type BigDataItem struct {
	Name   string
	Size   int64
	Digest string
}

// DescriptorInfo captures OCI/docker descriptor-level information.
type DescriptorInfo struct {
	MediaType    string
	Digest       string
	Size         int64
	Annotations  map[string]string
	Architecture string
	OS           string
	Variant      string
}

// ManifestInfo captures one manifest or index blob stored in image big-data.
type ManifestInfo struct {
	Name          string
	Digest        string
	Size          int64
	SchemaVersion int
	MediaType     string
	Kind          string
	Config        *DescriptorInfo
	LinkedConfig  string
	ConfigMatch   string
	Layers        []DescriptorInfo
	Manifests     []DescriptorInfo
}

// ManifestRefInfo captures one manifest that references a parsed config blob.
type ManifestRefInfo struct {
	Name   string
	Digest string
}

// ConfigInfo captures one parsed image config blob.
type ConfigInfo struct {
	Name         string
	Digest       string
	Size         int64
	Created      string
	Author       string
	Architecture string
	OS           string
	Variant      string
	RootFSType   string
	DiffIDs      []string
	Env          []string
	Cmd          []string
	Entrypoint   []string
	WorkingDir   string
	User         string
	ExposedPorts []string
	Labels       map[string]string
	Volumes      []string
	StopSignal   string
	Healthcheck  []string
	HistoryCount int
	LayerCount   int
	Annotations  map[string]string
	ReferencedBy []ManifestRefInfo
}

// SignatureInfo captures one signature-like big-data blob.
type SignatureInfo struct {
	Name        string
	Digest      string
	Size        int64
	Format      string
	Entries     int
	Preview     string
	MediaType   string
	Annotations map[string]string
}

type manifestPlatformRaw struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
	Variant      string `json:"variant"`
}

type manifestDescriptorRaw struct {
	MediaType   string               `json:"mediaType"`
	Digest      string               `json:"digest"`
	Size        int64                `json:"size"`
	Annotations map[string]string    `json:"annotations"`
	Platform    *manifestPlatformRaw `json:"platform"`
}

// StoreInfo captures containers/storage configuration and driver diagnostics.
type StoreInfo struct {
	GraphRoot             string
	RunRoot               string
	ImageStore            string
	GraphDriverName       string
	GraphOptions          []string
	PullOptions           map[string]string
	TransientStore        bool
	AdditionalImageStores []string
	AdditionalLayerStores []string
	DriverStatus          map[string]string
}

// LayerInfo captures CRI-O layer data sourced from containers/storage.
type LayerInfo struct {
	ID                 string
	Parent             string
	Names              []string
	Metadata           string
	MountLabel         string
	MountPoint         string
	MountCount         int
	CreatedAt          time.Time
	CompressedDigest   string
	CompressedSize     int64
	UncompressedDigest string
	UncompressedSize   int64
	TOCDigest          string
	CompressionType    string
	UsedUIDs           []uint32
	UsedGIDs           []uint32
	UIDMap             []IDMapEntry
	GIDMap             []IDMapEntry
	Flags              map[string]any
	ReadOnly           bool
	BigDataNames       []string
	DriverMetadata     map[string]string
	Path               string
	OverlayLinkID      string
}

// ContainerInfo captures CRI-O container storage object details.
type ContainerInfo struct {
	Store            *StoreInfo
	ID               string
	Names            []string
	ImageID          string
	LayerID          string
	Metadata         string
	CreatedAt        time.Time
	UIDMap           []IDMapEntry
	GIDMap           []IDMapEntry
	Flags            map[string]any
	BigData          []BigDataItem
	MountLabel       string
	MountOptions     []string
	Directory        string
	RunDirectory     string
	Size             int64
	ParentOwnerUIDs  []int
	ParentOwnerGIDs  []int
	DriverMetadata   map[string]string
	RWLayer          *LayerInfo
	BundleDir        string
	WritableLayerDir string
}

// ImageInfo captures CRI-O image storage object details.
type ImageInfo struct {
	Store               *StoreInfo
	ID                  string
	Names               []string
	NamesHistory        []string
	Digest              string
	Digests             []string
	TopLayer            string
	MappedTopLayers     []string
	Metadata            string
	CreatedAt           time.Time
	Flags               map[string]any
	BigData             []BigDataItem
	Directory           string
	RunDirectory        string
	ReadOnly            bool
	Size                int64
	TopLayerDriverMeta  map[string]string
	TopLayerPath        string
	AdditionalTopLayers []string
	Manifests           []ManifestInfo
	Configs             []ConfigInfo
	Signatures          []SignatureInfo
	OtherBigData        []BigDataItem
}

func (r *Runtime) getStore() (cstorage.Store, error) {
	r.storeOnce.Do(func() {
		opts, err := storagetypes.DefaultStoreOptions()
		if err != nil {
			r.storeErr = fmt.Errorf("load containers/storage options: %w", err)
			return
		}

		if r.storageRoot != "" {
			opts.GraphRoot = r.storageRoot
		}
		if r.storageRunRoot != "" {
			opts.RunRoot = r.storageRunRoot
		}

		store, err := cstorage.GetStore(opts)
		if err != nil {
			r.storeErr = fmt.Errorf("open containers/storage store: %w", err)
			return
		}
		r.store = store
	})
	return r.store, r.storeErr
}

func (r *Runtime) CRIOStoreInfo(_ context.Context) (*StoreInfo, error) {
	store, err := r.getStore()
	if err != nil {
		return nil, err
	}

	info := &StoreInfo{
		GraphRoot:       store.GraphRoot(),
		RunRoot:         store.RunRoot(),
		ImageStore:      store.ImageStore(),
		GraphDriverName: store.GraphDriverName(),
		GraphOptions:    append([]string(nil), store.GraphOptions()...),
		PullOptions:     cloneStringMap(store.PullOptions()),
		TransientStore:  store.TransientStore(),
	}

	if driver, err := store.GraphDriver(); err == nil {
		info.AdditionalImageStores = dedupeStrings(driver.AdditionalImageStores())
	}
	info.AdditionalLayerStores = parseDriverOptionPaths(info.GraphOptions, "additionallayerstore")

	status, err := store.Status()
	if err == nil {
		info.DriverStatus = keyValuePairsToMap(status)
	}

	return info, nil
}

func (h *containerHandle) CRIOContainerInfo(ctx context.Context) (*ContainerInfo, error) {
	store, err := h.rt.getStore()
	if err != nil {
		return nil, err
	}

	ctr, err := store.Container(h.id)
	if err != nil {
		return nil, fmt.Errorf("lookup storage container %s: %w", h.id, err)
	}

	profile := &ContainerInfo{
		ID:           ctr.ID,
		Names:        append([]string(nil), ctr.Names...),
		ImageID:      ctr.ImageID,
		LayerID:      ctr.LayerID,
		Metadata:     ctr.Metadata,
		CreatedAt:    ctr.Created,
		UIDMap:       convertIDMappings(ctr.UIDMap),
		GIDMap:       convertIDMappings(ctr.GIDMap),
		Flags:        cloneAnyMap(ctr.Flags),
		BigData:      buildBigDataItems(ctr.BigDataNames, ctr.BigDataSizes, ctr.BigDataDigests),
		MountLabel:   ctr.MountLabel(),
		MountOptions: append([]string(nil), ctr.MountOpts()...),
		BundleDir:    crioContainerBundleDir(h.rt.storageRunRoot, h.id),
	}

	if info, err := h.rt.CRIOStoreInfo(ctx); err == nil {
		profile.Store = info
	}
	if dir, err := store.ContainerDirectory(ctr.ID); err == nil {
		profile.Directory = dir
	}
	if dir, err := store.ContainerRunDirectory(ctr.ID); err == nil {
		profile.RunDirectory = dir
	}
	if size, err := store.ContainerSize(ctr.ID); err == nil {
		profile.Size = size
	}
	if uids, gids, err := store.ContainerParentOwners(ctr.ID); err == nil {
		profile.ParentOwnerUIDs = append([]int(nil), uids...)
		profile.ParentOwnerGIDs = append([]int(nil), gids...)
	}

	h.ensureSpec(ctx)
	if profile.WritableLayerDir == "" {
		if cfg, err := h.Config(ctx); err == nil {
			profile.WritableLayerDir = cfg.WritableLayerPath
		}
	}

	if ctr.LayerID != "" {
		layer, err := store.Layer(ctr.LayerID)
		if err == nil {
			profile.RWLayer = h.rt.convertStorageLayer(store, layer)
			profile.DriverMetadata = cloneStringMap(profile.RWLayer.DriverMetadata)
			if profile.WritableLayerDir == "" {
				profile.WritableLayerDir = profile.RWLayer.Path
			}
		}
	}

	return profile, nil
}

func (h *imageHandle) CRIOImageInfo(ctx context.Context) (*ImageInfo, error) {
	store, err := h.rt.getStore()
	if err != nil {
		return nil, err
	}

	img, err := h.lookupStorageImage(store)
	if err != nil {
		return nil, err
	}

	profile := &ImageInfo{
		ID:                  img.ID,
		Names:               append([]string(nil), img.Names...),
		NamesHistory:        append([]string(nil), img.NamesHistory...),
		Digest:              digestToString(img.Digest),
		Digests:             digestsToStrings(img.Digests),
		TopLayer:            img.TopLayer,
		MappedTopLayers:     append([]string(nil), img.MappedTopLayers...),
		Metadata:            img.Metadata,
		CreatedAt:           img.Created,
		Flags:               cloneAnyMap(img.Flags),
		BigData:             buildBigDataItems(img.BigDataNames, img.BigDataSizes, img.BigDataDigests),
		ReadOnly:            img.ReadOnly,
		AdditionalTopLayers: append([]string(nil), img.MappedTopLayers...),
	}

	if info, err := h.rt.CRIOStoreInfo(ctx); err == nil {
		profile.Store = info
	}
	manifests, configs, signatures, otherBigData := h.loadImageBigDataDetails(store, img, profile.BigData)
	manifests, configs = linkManifestConfigs(manifests, configs)
	profile.Manifests = manifests
	profile.Configs = configs
	profile.Signatures = signatures
	profile.OtherBigData = otherBigData
	if dir, err := store.ImageDirectory(img.ID); err == nil {
		profile.Directory = dir
	}
	if dir, err := store.ImageRunDirectory(img.ID); err == nil {
		profile.RunDirectory = dir
	}
	if size, err := store.ImageSize(img.ID); err == nil {
		profile.Size = size
	}
	if img.TopLayer != "" {
		if driver, err := store.GraphDriver(); err == nil {
			if meta, err := driver.Metadata(img.TopLayer); err == nil {
				profile.TopLayerDriverMeta = cloneStringMap(meta)
				profile.TopLayerPath = bestPathFromDriverMetadata(meta, store.GraphRoot(), store.GraphDriverName(), img.TopLayer)
			}
		}
	}

	return profile, nil
}

func (h *imageHandle) loadImageBigDataDetails(store cstorage.Store, img *cstorage.Image, items []BigDataItem) ([]ManifestInfo, []ConfigInfo, []SignatureInfo, []BigDataItem) {
	if img == nil || len(items) == 0 {
		return nil, nil, nil, nil
	}

	// Pre-load all big-data content to avoid repeated store reads.
	dataByName := make(map[string][]byte, len(items))
	for _, item := range items {
		data, err := store.ImageBigData(img.ID, item.Name)
		if err != nil || len(data) == 0 {
			continue
		}
		dataByName[item.Name] = data
	}

	var manifests []ManifestInfo
	var configs []ConfigInfo
	var signatures []SignatureInfo
	var others []BigDataItem

	// Pass 1: parse manifest-keyed items ("manifest" and "manifest-sha256:*").
	// Some manifest-sha256:* entries may be indices (manifest lists).
	manifestKeys := make(map[string]struct{})
	configDigests := make(map[string]struct{}) // config digests referenced by manifests
	for _, item := range items {
		if !isManifestBigDataName(item.Name) {
			continue
		}
		data, ok := dataByName[item.Name]
		if !ok {
			continue
		}
		manifest, ok := parseManifestInfo(item, data)
		if !ok {
			continue
		}
		manifests = append(manifests, manifest)
		manifestKeys[item.Name] = struct{}{}
		if manifest.Config != nil {
			if d := normalizeDigest(manifest.Config.Digest); d != "" {
				configDigests[d] = struct{}{}
			}
		}
	}

	// Pass 2: classify remaining items.
	// Config keys in CRI-O are typically "sha256:<hash>" (no prefix).
	// Match by: key name == config digest from manifest, OR item digest == config digest,
	// AND content must parse as a valid image config (arch, os, rootfs, etc.).
	for _, item := range items {
		if _, done := manifestKeys[item.Name]; done {
			continue
		}
		data, ok := dataByName[item.Name]
		if !ok {
			others = append(others, item)
			continue
		}

		if isSignatureBigDataName(item.Name) {
			signatures = append(signatures, parseSignatureInfo(item, data))
			continue
		}

		// Config detection: match key name or item digest against manifest config digests.
		keyNorm := normalizeDigest(item.Name)
		itemDigestNorm := normalizeDigest(item.Digest)
		_, keyMatch := configDigests[keyNorm]
		_, digestMatch := configDigests[itemDigestNorm]
		if keyMatch || digestMatch {
			if config, ok := parseConfigInfo(item, data); ok {
				configs = append(configs, config)
				continue
			}
		}

		// Fallback: content-based detection for non-standard layouts.
		if config, ok := parseConfigInfo(item, data); ok {
			configs = append(configs, config)
			continue
		}
		if manifest, ok := parseManifestInfo(item, data); ok {
			manifests = append(manifests, manifest)
			continue
		}
		others = append(others, item)
	}

	return manifests, configs, signatures, others
}

func (h *imageHandle) lookupStorageImage(store cstorage.Store) (*cstorage.Image, error) {
	// Direct ID lookup (primary — imageHandle is now storage-sourced).
	if img, err := store.Image(h.id); err == nil {
		return img, nil
	}
	// Fallback to ref / names.
	for _, candidate := range append([]string{h.ref}, h.names...) {
		if candidate != "" && candidate != h.id {
			if img, err := store.Image(candidate); err == nil {
				return img, nil
			}
		}
	}
	return nil, fmt.Errorf("storage image not found for %s", h.id)
}

func (r *Runtime) convertStorageLayer(store cstorage.Store, layer *cstorage.Layer) *LayerInfo {
	if layer == nil {
		return nil
	}
	info := &LayerInfo{
		ID:                 layer.ID,
		Parent:             layer.Parent,
		Names:              append([]string(nil), layer.Names...),
		Metadata:           layer.Metadata,
		MountLabel:         layer.MountLabel,
		MountPoint:         layer.MountPoint,
		MountCount:         layer.MountCount,
		CreatedAt:          layer.Created,
		CompressedDigest:   digestToString(layer.CompressedDigest),
		CompressedSize:     layer.CompressedSize,
		UncompressedDigest: digestToString(layer.UncompressedDigest),
		UncompressedSize:   layer.UncompressedSize,
		TOCDigest:          digestToString(layer.TOCDigest),
		CompressionType:    layer.CompressionType.Extension(),
		UsedUIDs:           append([]uint32(nil), layer.UIDs...),
		UsedGIDs:           append([]uint32(nil), layer.GIDs...),
		UIDMap:             convertIDMappings(layer.UIDMap),
		GIDMap:             convertIDMappings(layer.GIDMap),
		Flags:              cloneAnyMap(layer.Flags),
		ReadOnly:           layer.ReadOnly,
		BigDataNames:       append([]string(nil), layer.BigDataNames...),
	}
	if driver, err := store.GraphDriver(); err == nil {
		if meta, err := driver.Metadata(layer.ID); err == nil {
			info.DriverMetadata = cloneStringMap(meta)
			info.Path = bestPathFromDriverMetadata(meta, store.GraphRoot(), store.GraphDriverName(), layer.ID)
		}
	}
	if info.Path == "" && store.GraphDriverName() == defaultGraphDriver {
		info.Path = filepath.Join(store.GraphRoot(), store.GraphDriverName(), layer.ID, "diff")
	}
	// Read the short-link name from the overlay "link" file.
	// The file lives at <layerDir>/link, one level above the diff directory.
	if store.GraphDriverName() == defaultGraphDriver && info.Path != "" {
		layerDir := filepath.Dir(info.Path)
		if linkData, err := os.ReadFile(filepath.Join(layerDir, "link")); err == nil {
			info.OverlayLinkID = strings.TrimSpace(string(linkData))
		}
	}
	return info
}

func convertIDMappings(entries []idtools.IDMap) []IDMapEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]IDMapEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, IDMapEntry{
			ContainerID: int(entry.ContainerID),
			HostID:      int(entry.HostID),
			Size:        int(entry.Size),
		})
	}
	return out
}

func buildBigDataItems(names []string, sizes map[string]int64, digests map[string]digest.Digest) []BigDataItem {
	if len(names) == 0 {
		return nil
	}
	out := make([]BigDataItem, 0, len(names))
	for _, name := range names {
		item := BigDataItem{Name: name, Size: -1}
		if size, ok := sizes[name]; ok {
			item.Size = size
		}
		if d, ok := digests[name]; ok {
			item.Digest = d.String()
		}
		out = append(out, item)
	}
	return out
}

func isManifestBigDataName(name string) bool {
	return name == "manifest" || strings.HasPrefix(name, "manifest-")
}

func isSignatureBigDataName(name string) bool {
	return strings.HasPrefix(name, "signature")
}

func parseManifestInfo(item BigDataItem, data []byte) (ManifestInfo, bool) {
	type manifestBlob struct {
		SchemaVersion int                     `json:"schemaVersion"`
		MediaType     string                  `json:"mediaType"`
		Config        *manifestDescriptorRaw  `json:"config"`
		Layers        []manifestDescriptorRaw `json:"layers"`
		Manifests     []manifestDescriptorRaw `json:"manifests"`
	}

	var blob manifestBlob
	if err := json.Unmarshal(data, &blob); err != nil {
		return ManifestInfo{}, false
	}
	if blob.SchemaVersion == 0 && blob.MediaType == "" && blob.Config == nil && len(blob.Layers) == 0 && len(blob.Manifests) == 0 {
		return ManifestInfo{}, false
	}

	info := ManifestInfo{
		Name:          item.Name,
		Digest:        item.Digest,
		Size:          item.Size,
		SchemaVersion: blob.SchemaVersion,
		MediaType:     blob.MediaType,
		Kind:          "manifest",
		Layers:        make([]DescriptorInfo, 0, len(blob.Layers)),
		Manifests:     make([]DescriptorInfo, 0, len(blob.Manifests)),
	}
	if len(blob.Manifests) > 0 {
		info.Kind = "index"
	}
	if blob.Config != nil {
		cfg := descriptorToInfo(blob.Config)
		info.Config = &cfg
	}
	for _, layer := range blob.Layers {
		layerCopy := layer
		info.Layers = append(info.Layers, descriptorToInfo(&layerCopy))
	}
	for _, manifest := range blob.Manifests {
		manifestCopy := manifest
		info.Manifests = append(info.Manifests, descriptorToInfo(&manifestCopy))
	}
	return info, true
}

func parseConfigInfo(item BigDataItem, data []byte) (ConfigInfo, bool) {
	type imageConfigSection struct {
		Env          []string          `json:"Env"`
		Cmd          []string          `json:"Cmd"`
		Entrypoint   []string          `json:"Entrypoint"`
		WorkingDir   string            `json:"WorkingDir"`
		User         string            `json:"User"`
		ExposedPorts map[string]any    `json:"ExposedPorts"`
		Labels       map[string]string `json:"Labels"`
		Volumes      map[string]any    `json:"Volumes"`
		StopSignal   string            `json:"StopSignal"`
		Healthcheck  *struct {
			Test []string `json:"Test"`
		} `json:"Healthcheck"`
	}
	type imageRootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	}
	type historyEntry struct {
		CreatedBy string `json:"created_by"`
	}
	type imageConfigBlob struct {
		Created      string             `json:"created"`
		Author       string             `json:"author"`
		Architecture string             `json:"architecture"`
		OS           string             `json:"os"`
		Variant      string             `json:"variant"`
		RootFS       imageRootFS        `json:"rootfs"`
		Config       imageConfigSection `json:"config"`
		History      []historyEntry     `json:"history"`
		Annotations  map[string]string  `json:"annotations"`
	}

	var blob imageConfigBlob
	if err := json.Unmarshal(data, &blob); err != nil {
		return ConfigInfo{}, false
	}
	if blob.Architecture == "" && blob.OS == "" && blob.RootFS.Type == "" && len(blob.RootFS.DiffIDs) == 0 && len(blob.Config.Env) == 0 && len(blob.Config.Cmd) == 0 && len(blob.Config.Entrypoint) == 0 {
		return ConfigInfo{}, false
	}

	info := ConfigInfo{
		Name:         item.Name,
		Digest:       item.Digest,
		Size:         item.Size,
		Created:      blob.Created,
		Author:       blob.Author,
		Architecture: blob.Architecture,
		OS:           blob.OS,
		Variant:      blob.Variant,
		RootFSType:   blob.RootFS.Type,
		DiffIDs:      append([]string(nil), blob.RootFS.DiffIDs...),
		Env:          append([]string(nil), blob.Config.Env...),
		Cmd:          append([]string(nil), blob.Config.Cmd...),
		Entrypoint:   append([]string(nil), blob.Config.Entrypoint...),
		WorkingDir:   blob.Config.WorkingDir,
		User:         blob.Config.User,
		Labels:       cloneStringMap(blob.Config.Labels),
		StopSignal:   blob.Config.StopSignal,
		HistoryCount: len(blob.History),
		LayerCount:   len(blob.RootFS.DiffIDs),
		Annotations:  cloneStringMap(blob.Annotations),
	}
	for port := range blob.Config.ExposedPorts {
		info.ExposedPorts = append(info.ExposedPorts, port)
	}
	slices.Sort(info.ExposedPorts)
	for volume := range blob.Config.Volumes {
		info.Volumes = append(info.Volumes, volume)
	}
	slices.Sort(info.Volumes)
	if blob.Config.Healthcheck != nil {
		info.Healthcheck = append([]string(nil), blob.Config.Healthcheck.Test...)
	}
	return info, true
}

func parseSignatureInfo(item BigDataItem, data []byte) SignatureInfo {
	info := SignatureInfo{
		Name:   item.Name,
		Digest: item.Digest,
		Size:   item.Size,
		Format: "raw",
	}

	var array []json.RawMessage
	if err := json.Unmarshal(data, &array); err == nil {
		info.Format = "json-array"
		info.Entries = len(array)
		info.Preview = previewJSON(data)
		return info
	}

	var object map[string]any
	if err := json.Unmarshal(data, &object); err == nil {
		info.Format = "json-object"
		info.Entries = len(object)
		if mediaType, ok := object["mediaType"].(string); ok {
			info.MediaType = mediaType
		}
		if annotations := extractStringMap(object["annotations"]); len(annotations) > 0 {
			info.Annotations = annotations
		}
		info.Preview = previewJSON(data)
		return info
	}

	info.Preview = previewText(data)
	return info
}

func linkManifestConfigs(manifests []ManifestInfo, configs []ConfigInfo) ([]ManifestInfo, []ConfigInfo) {
	if len(manifests) == 0 || len(configs) == 0 {
		return manifests, configs
	}

	configByDigest := make(map[string]int, len(configs))
	configByName := make(map[string]int, len(configs))
	for i := range configs {
		if digest := normalizeDigest(configs[i].Digest); digest != "" {
			if _, exists := configByDigest[digest]; !exists {
				configByDigest[digest] = i
			}
		}
		if configs[i].Name != "" {
			if _, exists := configByName[configs[i].Name]; !exists {
				configByName[configs[i].Name] = i
			}
		}
	}

	for i := range manifests {
		cfg := manifests[i].Config
		if cfg == nil {
			continue
		}

		matchIndex := -1
		matchType := ""
		if digest := normalizeDigest(cfg.Digest); digest != "" {
			if idx, ok := configByDigest[digest]; ok {
				matchIndex = idx
				matchType = "digest"
			}
		}
		if matchIndex == -1 {
			for _, candidate := range candidateConfigNames(manifests[i].Name) {
				if idx, ok := configByName[candidate]; ok {
					matchIndex = idx
					matchType = "name"
					break
				}
			}
		}
		if matchIndex == -1 {
			continue
		}

		manifests[i].LinkedConfig = configs[matchIndex].Name
		manifests[i].ConfigMatch = matchType
		configs[matchIndex].ReferencedBy = append(configs[matchIndex].ReferencedBy, ManifestRefInfo{
			Name:   manifests[i].Name,
			Digest: manifests[i].Digest,
		})
	}

	for i := range configs {
		if len(configs[i].ReferencedBy) == 0 {
			continue
		}
		slices.SortFunc(configs[i].ReferencedBy, func(a, b ManifestRefInfo) int {
			if a.Name != b.Name {
				return strings.Compare(a.Name, b.Name)
			}
			return strings.Compare(a.Digest, b.Digest)
		})
	}

	return manifests, configs
}

func descriptorToInfo(in *manifestDescriptorRaw) DescriptorInfo {
	if in == nil {
		return DescriptorInfo{}
	}
	info := DescriptorInfo{
		MediaType:   in.MediaType,
		Digest:      in.Digest,
		Size:        in.Size,
		Annotations: cloneStringMap(in.Annotations),
	}
	if in.Platform != nil {
		info.Architecture = in.Platform.Architecture
		info.OS = in.Platform.OS
		info.Variant = in.Platform.Variant
	}
	return info
}

func previewJSON(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, data); err != nil {
		return previewText(data)
	}
	return truncateString(compact.String(), 160)
}

func previewText(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	text := string(data)
	if !utf8.ValidString(text) {
		return "<binary>"
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "<empty>"
	}
	if !looksTextual(text) {
		return "<binary>"
	}
	return truncateString(text, 160)
}

func looksTextual(value string) bool {
	for _, ch := range value {
		if ch < 0x20 && ch != '\n' && ch != '\r' && ch != '\t' {
			return false
		}
	}
	return true
}

func truncateString(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "..."
}

func normalizeDigest(value string) string {
	return strings.TrimSpace(strings.ToLower(value))
}

func candidateConfigNames(manifestName string) []string {
	if manifestName == "" {
		return nil
	}
	if manifestName == "manifest" {
		return []string{"config"}
	}
	if strings.HasPrefix(manifestName, "manifest-") {
		suffix := strings.TrimPrefix(manifestName, "manifest-")
		if suffix != "" {
			return []string{"config-" + suffix, suffix}
		}
	}
	return nil
}

func extractStringMap(value any) map[string]string {
	items, ok := value.(map[string]any)
	if !ok || len(items) == 0 {
		return nil
	}
	out := make(map[string]string, len(items))
	for key, item := range items {
		out[key] = fmt.Sprint(item)
	}
	return out
}

func keyValuePairsToMap(entries [][2]string) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	out := make(map[string]string, len(entries))
	for _, entry := range entries {
		if len(entry) < 2 {
			continue
		}
		out[entry[0]] = entry[1]
	}
	return out
}

func cloneAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func parseDriverOptionPaths(options []string, suffix string) []string {
	var paths []string
	for _, option := range options {
		key, value, found := strings.Cut(option, "=")
		if !found || value == "" {
			continue
		}
		if strings.HasSuffix(key, "."+suffix) {
			paths = append(paths, value)
		}
	}
	return dedupeStrings(paths)
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || slices.Contains(out, value) {
			continue
		}
		out = append(out, value)
	}
	return out
}

func digestsToStrings(values []digest.Digest) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out = append(out, value.String())
	}
	return out
}

func digestToString(value digest.Digest) string {
	if value == "" {
		return ""
	}
	return value.String()
}

func bestPathFromDriverMetadata(metadata map[string]string, graphRoot, driverName, layerID string) string {
	if len(metadata) == 0 {
		return ""
	}
	for _, key := range []string{"UpperDir", "Upperdir", "MergedDir", "Merged", "WorkDir", "Workdir", "LowerDir", "Lowerdir", "Dir"} {
		if metadata[key] != "" {
			return metadata[key]
		}
	}
	if driverName == defaultGraphDriver && graphRoot != "" && layerID != "" {
		return filepath.Join(graphRoot, driverName, layerID, "diff")
	}
	return ""
}

func (r *Runtime) convertStorageLayerToRuntime(store cstorage.Store, layer *cstorage.Layer) *runtime.ImageLayer {
	info := r.convertStorageLayer(store, layer)
	if info == nil {
		return nil
	}
	return &runtime.ImageLayer{
		CompressionType:    info.CompressionType,
		CompressedDigest:   info.CompressedDigest,
		Size:               info.CompressedSize,
		UncompressedDigest: info.UncompressedDigest,
		UsageSize:          info.UncompressedSize,
		Path:               info.Path,
		Crio: &runtime.ImageCRIOLayer{
			ID:            info.ID,
			Names:         append([]string(nil), info.Names...),
			Metadata:      info.DriverMetadata,
			OverlayLinkID: info.OverlayLinkID,
		},
	}
}

var _ StoreIntrospector = (*Runtime)(nil)
var _ ContainerIntrospector = (*containerHandle)(nil)
var _ ImageIntrospector = (*imageHandle)(nil)
