package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	dockertypes "github.com/docker/docker/api/types"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/icebergu/c-ray/pkg/runtime"
)

// imageHandle implements runtime.Image for the Docker classic image store.
type imageHandle struct {
	rt  *Runtime
	ref string

	inspectOnce sync.Once
	inspect     *dockertypes.ImageInspect
	inspectRaw  []byte
	inspectErr  error
}

func (h *imageHandle) ensureInspect(ctx context.Context) {
	h.inspectOnce.Do(func() {
		resp, raw, err := h.rt.dockerClient.ImageInspectWithRaw(ctx, h.ref)
		if err != nil {
			h.inspectErr = err
			return
		}
		h.inspect = &resp
		h.inspectRaw = raw
	})
}

func (h *imageHandle) Ref() string { return h.ref }

func (h *imageHandle) Info(ctx context.Context) (*runtime.ImageInfo, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}
	i := h.inspect

	info := &runtime.ImageInfo{
		Size:   i.Size,
		Digest: i.ID,
	}
	info.Names = append(info.Names, i.RepoTags...)
	info.Names = append(info.Names, i.RepoDigests...)
	if len(info.Names) == 0 {
		info.Names = []string{i.ID}
	}
	if ct, err := time.Parse(time.RFC3339Nano, i.Created); err == nil {
		info.CreatedAt = ct
	}

	return info, nil
}

func (h *imageHandle) Config(ctx context.Context) (*runtime.ImageConfigInfo, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}
	i := h.inspect
	desc := dockerInspectDescriptor(h.inspectRaw)

	info := &runtime.ImageConfigInfo{
		StorageBackend: runtime.ImageBackendDockerClassic,
	}
	if desc.MediaType != "" {
		info.TargetMediaType = desc.MediaType
		info.TargetKind, info.Schema = dockerDescribeImageTarget(desc.MediaType)
	} else {
		// Older Docker daemons (<24) do not return Descriptor in inspect.
		// Derive Kind/Schema from the actual image config blob contents
		// rather than guessing from descriptor mediaType.
		info.TargetKind, info.Schema = dockerDeriveTargetFromConfigBlob(dockerClassicConfigPath(h.rt, i.ID))
	}

	manifest := &runtime.ImageManifest{
		Platform:   runtime.FormatPlatform(i.Os, i.Architecture, i.Variant),
		ConfigPath: dockerClassicConfigPath(h.rt, i.ID),
	}

	// Descriptor digest refers to the target object returned by the daemon.
	// For single-platform images this is the manifest digest; for indexes it is
	// the index digest and should not be assigned to the current-platform manifest.
	switch info.TargetKind {
	case "Index":
		if desc.Digest != "" {
			info.IndexPath = dockerClassicBlobPath(h.rt, desc.Digest)
		}
	case "Manifest", "":
		// Prefer the descriptor digest (modern daemons). When absent (older
		// daemons, or fallback derivation from the config blob), recover the
		// per-platform manifest digest from RepoDigests, which the daemon
		// always populates with @<algo>:<digest> entries when an image was
		// pulled or pushed by digest. This is the same value containerd would
		// return for the platform manifest.
		switch {
		case desc.Digest != "":
			manifest.Digest = desc.Digest
			manifest.Path = dockerClassicBlobPath(h.rt, desc.Digest)
		default:
			for _, rd := range i.RepoDigests {
				if _, d, ok := strings.Cut(rd, "@"); ok && d != "" {
					manifest.Digest = d
					manifest.Path = dockerClassicBlobPath(h.rt, d)
					break
				}
			}
		}
	}

	info.Manifest = manifest
	if info.TargetKind != "Index" {
		info.Manifests = []*runtime.ImageManifest{manifest}
	}
	return info, nil
}

type dockerRawImageInspect struct {
	Descriptor dockerRawDescriptor `json:"Descriptor"`
}

type dockerRawDescriptor struct {
	MediaType string `json:"mediaType"`
	Digest    string `json:"digest"`
}

func dockerInspectDescriptor(raw []byte) dockerRawDescriptor {
	if len(raw) == 0 {
		return dockerRawDescriptor{}
	}
	var inspect dockerRawImageInspect
	if err := json.Unmarshal(raw, &inspect); err != nil {
		return dockerRawDescriptor{}
	}
	return inspect.Descriptor
}

func dockerDescribeImageTarget(mediaType string) (string, string) {
	kind := ""
	schema := ""
	switch {
	case strings.Contains(mediaType, "manifest.list") || strings.Contains(mediaType, "image.index"):
		kind = "Index"
	case strings.Contains(mediaType, "manifest"):
		kind = "Manifest"
	}
	switch {
	case strings.Contains(mediaType, ".oci."):
		schema = "OCI"
	case strings.Contains(mediaType, ".docker."):
		schema = "Docker"
	}
	return kind, schema
}

// dockerDeriveTargetFromConfigBlob inspects the image config blob to determine
// (TargetKind, Schema) when the daemon's inspect response lacks a Descriptor.
//
// Classic Docker stores ONE image per image ID per platform — multi-arch images
// are split into separate image entries — so the structural Kind is always
// "Manifest" when a config blob exists. Schema is derived from authoritative
// fields in the config JSON:
//
//   - "docker_version" present: written exclusively by the Docker engine →
//     Schema = "Docker"
//   - "os.features" present: OCI 1.x extension field, never used by Docker →
//     Schema = "OCI"
//
// If the config blob is unreadable, or both/neither markers exist, Schema is
// left empty rather than guessed.
func dockerDeriveTargetFromConfigBlob(configPath string) (string, string) {
	if configPath == "" {
		return "", ""
	}
	raw, err := os.ReadFile(configPath)
	if err != nil {
		return "", ""
	}
	var probe struct {
		DockerVersion string          `json:"docker_version"`
		OSFeatures    json.RawMessage `json:"os.features"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return "", ""
	}
	hasDocker := strings.TrimSpace(probe.DockerVersion) != ""
	hasOCI := len(probe.OSFeatures) > 0 && string(probe.OSFeatures) != "null"

	schema := ""
	switch {
	case hasDocker && !hasOCI:
		schema = "Docker"
	case hasOCI && !hasDocker:
		schema = "OCI"
	}
	return "Manifest", schema
}

func dockerClassicConfigPath(rt *Runtime, imageID string) string {
	if rt == nil || rt.daemonInfo == nil {
		return ""
	}
	rootDir := strings.TrimSpace(rt.daemonInfo.DockerRootDir)
	driver := strings.TrimSpace(rt.daemonInfo.Driver)
	if rootDir == "" || driver == "" {
		return ""
	}
	algorithm, encoded, ok := strings.Cut(strings.TrimSpace(imageID), ":")
	if !ok || algorithm == "" || encoded == "" {
		return ""
	}
	return filepath.Join(rootDir, "image", driver, "imagedb", "content", algorithm, encoded)
}

// dockerClassicBlobPath returns the path of a manifest/index blob in Docker's
// unified content store (<DockerRootDir>/content/data/blobs/<algo>/<encoded>),
// but only when the file actually exists on disk.
//
// This blob layout is the containerd content store; Docker persists manifests
// and indexes there ONLY when the containerd image store is enabled. In
// classic graphdriver mode (the only mode in which the surrounding code runs —
// containerd-snapshotter mode is delegated to the containerd runtime), Docker
// stores only the image config blob and DiffIDs; manifests and indexes are
// never written to disk. Returning a constructed-but-nonexistent path would
// surface a fabricated address in the UI and cause downstream consumers
// (e.g. the Platforms view) to mis-detect the manifest as missing.
//
// We therefore probe the filesystem and return "" whenever the blob is not
// present, leaving the caller free to render a clear "-" instead of a lie.
func dockerClassicBlobPath(rt *Runtime, digest string) string {
	if rt == nil || rt.daemonInfo == nil {
		return ""
	}
	rootDir := strings.TrimSpace(rt.daemonInfo.DockerRootDir)
	if rootDir == "" {
		return ""
	}
	algorithm, encoded, ok := strings.Cut(strings.TrimSpace(digest), ":")
	if !ok || algorithm == "" || encoded == "" {
		return ""
	}
	path := filepath.Join(rootDir, "content", "data", "blobs", algorithm, encoded)
	if _, err := os.Stat(path); err != nil {
		return ""
	}
	return path
}

func (h *imageHandle) Layers(ctx context.Context, query runtime.LayerQuery) ([]*runtime.ImageLayer, error) {
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, h.inspectErr
	}

	rootFS := h.inspect.RootFS
	if rootFS.Type != "layers" || len(rootFS.Layers) == 0 {
		return nil, nil
	}

	gd := h.inspect.GraphDriver

	switch gd.Name {
	case "overlay2":
		if layers, ok := h.resolveOverlay2Layers(rootFS.Layers, gd); ok {
			return layers, nil
		}
		return h.summariseLayers(rootFS.Layers, gd.Name), nil
	default:
		// TODO: Add deep layer resolution for storage drivers other than overlay2.
		// For now, return summary-only layers without path information.
		return h.summariseLayers(rootFS.Layers, gd.Name), nil
	}
}

// resolveOverlay2Layers maps RootFS diff IDs to overlay2 layer directories.
func (h *imageHandle) resolveOverlay2Layers(diffIDs []string, gd dockertypes.GraphDriverData) ([]*runtime.ImageLayer, bool) {
	// Overlay2 GraphDriver.Data typically contains:
	//   UpperDir:  top layer diff directory
	//   LowerDir:  lower layer diffs, colon-separated (top-first)
	dirs, ok := resolveOverlay2LayerDirs(gd, len(diffIDs))
	if !ok {
		return nil, false
	}

	layers := make([]*runtime.ImageLayer, 0, len(diffIDs))
	for i, diffID := range diffIDs {
		l := &runtime.ImageLayer{
			Index:              i,
			UncompressedDigest: diffID,
			Docker: &runtime.ImageDockerLayer{
				GraphDriver: "overlay2",
			},
		}

		// diffIDs are bottom-up; dirs are top-down.
		// diffIDs[0] (base) → dirs[len(dirs)-1] (last), diffIDs[N-1] (top) → dirs[0] (first).
		dirIdx := len(dirs) - 1 - i
		if dirIdx >= 0 && dirIdx < len(dirs) {
			layerDir := dirs[dirIdx]
			l.Path = layerDir.path
			l.Docker.CacheID = overlay2CacheID(layerDir.path)
			l.Docker.ShortLinkID = layerDir.shortLinkID
			l.Docker.ShortLinkPath = layerDir.shortLinkPath

			l.UsageSize, l.UsageInodes = runtime.DirDiskUsage(layerDir.path)
		}

		layers = append(layers, l)
	}

	return layers, true
}

type overlay2LayerDir struct {
	path          string
	shortLinkID   string
	shortLinkPath string
}

func resolveOverlay2LayerDirs(gd dockertypes.GraphDriverData, want int) ([]overlay2LayerDir, bool) {
	upper := strings.TrimSpace(gd.Data["UpperDir"])
	lower := strings.TrimSpace(gd.Data["LowerDir"])

	var rawDirs []string
	if upper != "" {
		rawDirs = append(rawDirs, upper)
	}
	if lower != "" {
		for _, part := range strings.Split(lower, ":") {
			part = strings.TrimSpace(part)
			if part != "" {
				rawDirs = append(rawDirs, part)
			}
		}
	}

	if len(rawDirs) == 0 || len(rawDirs) != want {
		return nil, false
	}

	dirs := make([]overlay2LayerDir, 0, len(rawDirs))
	for _, dir := range rawDirs {
		resolved, ok := normalizeOverlay2LayerDir(dir)
		if !ok {
			return nil, false
		}
		dirs = append(dirs, resolved)
	}

	return dirs, true
}

func normalizeOverlay2LayerDir(dir string) (overlay2LayerDir, bool) {
	cleaned := filepath.Clean(dir)
	result := overlay2LayerDir{path: cleaned}
	if shortLinkID, shortLinkPath := overlay2ShortLink(cleaned); shortLinkID != "" {
		result.shortLinkID = shortLinkID
		result.shortLinkPath = shortLinkPath
	}
	info, err := os.Lstat(cleaned)
	if err != nil {
		return overlay2LayerDir{}, false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		resolved, err := filepath.EvalSymlinks(cleaned)
		if err != nil {
			return overlay2LayerDir{}, false
		}
		result.path = filepath.Clean(resolved)
	}
	// When the input dir was not itself a short-link path (e.g. UpperDir is
	// always the absolute <cache_id>/diff, and on newer Docker LowerDir entries
	// are also pre-resolved), fall back to the authoritative source: the
	// "<cache_id>/link" file maintained by the overlay2 driver. Its single-line
	// content is the short link ID; the corresponding "l/<id>" symlink is
	// derived from it.
	if result.shortLinkID == "" {
		if id, linkPath := overlay2ReadLinkFile(result.path); id != "" {
			result.shortLinkID = id
			result.shortLinkPath = linkPath
		}
	}
	return result, true
}

// overlay2ReadLinkFile reads "<cache_dir>/link" relative to a resolved diff
// path (".../overlay2/<cache_id>/diff"). It returns the short link ID and the
// "l/<id>" symlink path under the same overlay2 root, or empty strings when
// the layout does not match.
func overlay2ReadLinkFile(diffPath string) (string, string) {
	cleaned := filepath.Clean(diffPath)
	if filepath.Base(cleaned) != "diff" {
		return "", ""
	}
	cacheDir := filepath.Dir(cleaned)
	overlayRoot := filepath.Dir(cacheDir)
	if filepath.Base(overlayRoot) != "overlay2" {
		return "", ""
	}
	data, err := os.ReadFile(filepath.Join(cacheDir, "link"))
	if err != nil {
		return "", ""
	}
	id := strings.TrimSpace(string(data))
	if id == "" {
		return "", ""
	}
	return id, filepath.Join(overlayRoot, "l", id)
}

func overlay2ShortLink(path string) (string, string) {
	cleaned := filepath.Clean(path)
	parts := strings.Split(cleaned, string(filepath.Separator))
	for i, part := range parts {
		if part == "l" && i > 0 && parts[i-1] == "overlay2" && i+1 < len(parts) {
			id := parts[i+1]
			if id != "" {
				return id, cleaned
			}
		}
	}
	return "", ""
}

func overlay2CacheID(layerDir string) string {
	cleaned := filepath.Clean(layerDir)
	if filepath.Base(cleaned) == "diff" {
		return filepath.Base(filepath.Dir(cleaned))
	}

	parts := strings.Split(cleaned, string(filepath.Separator))
	for j, part := range parts {
		if part == "overlay2" && j+1 < len(parts) {
			return parts[j+1]
		}
	}

	return ""
}

// summariseLayers returns summary layers without path resolution.
func (h *imageHandle) summariseLayers(diffIDs []string, driverName string) []*runtime.ImageLayer {
	layers := make([]*runtime.ImageLayer, 0, len(diffIDs))
	for i, diffID := range diffIDs {
		layers = append(layers, &runtime.ImageLayer{
			Index:              i,
			UncompressedDigest: diffID,
			Docker: &runtime.ImageDockerLayer{
				GraphDriver: driverName,
			},
		})
	}
	return layers
}

// ---------------------------------------------------------------------------
// Runtime methods for classic image listing
// ---------------------------------------------------------------------------

func (r *Runtime) listClassicImages(ctx context.Context) ([]runtime.Image, error) {
	summaries, err := r.dockerClient.ImageList(ctx, imagetypes.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("docker image list: %w", err)
	}

	images := make([]runtime.Image, 0, len(summaries))
	for _, s := range summaries {
		ref := s.ID
		if len(s.RepoTags) > 0 {
			ref = s.RepoTags[0]
		}
		images = append(images, &imageHandle{rt: r, ref: ref})
	}
	return images, nil
}

func (r *Runtime) getClassicImage(ctx context.Context, ref string) (runtime.Image, error) {
	h := &imageHandle{rt: r, ref: ref}
	// Eagerly validate the reference resolves.
	h.ensureInspect(ctx)
	if h.inspectErr != nil {
		return nil, fmt.Errorf("get image %q: %w", ref, h.inspectErr)
	}
	return h, nil
}

// Compile-time interface check.
var _ runtime.Image = (*imageHandle)(nil)
