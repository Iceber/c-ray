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
	}

	manifest := &runtime.ImageManifest{
		Platform:   formatPlatform(i.Os, i.Architecture, i.Variant),
		ConfigPath: dockerClassicConfigPath(h.rt, i.ID),
	}

	// Descriptor digest refers to the target object returned by the daemon.
	// For single-platform images this is the manifest digest; for indexes it is
	// the index digest and should not be assigned to the current-platform manifest.
	if info.TargetKind == "Manifest" && desc.Digest != "" {
		manifest.Digest = desc.Digest
	} else if info.TargetKind == "" {
		for _, rd := range i.RepoDigests {
			if _, d, ok := strings.Cut(rd, "@"); ok && d != "" {
				manifest.Digest = d
				break
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

func formatPlatform(osName, arch, variant string) string {
	if osName == "" || arch == "" {
		return ""
	}
	platform := osName + "/" + arch
	if variant != "" {
		platform += "/" + variant
	}
	return platform
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

			l.UsageSize, l.UsageInodes = dirDiskUsage(layerDir.path)
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
	return result, true
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
