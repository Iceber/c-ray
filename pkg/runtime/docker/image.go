package docker

import (
	"context"
	"fmt"
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
	inspectErr  error
}

func (h *imageHandle) ensureInspect(ctx context.Context) {
	h.inspectOnce.Do(func() {
		resp, _, err := h.rt.dockerClient.ImageInspectWithRaw(ctx, h.ref)
		if err != nil {
			h.inspectErr = err
			return
		}
		h.inspect = &resp
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
	if len(i.RepoTags) > 0 {
		info.Name = i.RepoTags[0]
	} else if len(i.RepoDigests) > 0 {
		info.Name = i.RepoDigests[0]
	} else {
		info.Name = i.ID
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
	return &runtime.ImageConfigInfo{
		StorageBackend: runtime.ImageBackendDockerClassic,
	}, nil
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
		return h.resolveOverlay2Layers(rootFS.Layers, gd)
	default:
		// TODO: Add deep layer resolution for storage drivers other than overlay2.
		// For now, return summary-only layers without path information.
		return h.summariseLayers(rootFS.Layers, gd.Name), nil
	}
}

// resolveOverlay2Layers maps RootFS diff IDs to overlay2 layer directories.
func (h *imageHandle) resolveOverlay2Layers(diffIDs []string, gd dockertypes.GraphDriverData) ([]*runtime.ImageLayer, error) {
	// Overlay2 GraphDriver.Data typically contains:
	//   UpperDir:  top layer diff directory
	//   LowerDir:  lower layer diffs, colon-separated (top-first)
	upper := gd.Data["UpperDir"]
	lower := gd.Data["LowerDir"]

	// Collect all layer directories from top to bottom.
	var dirs []string
	if upper != "" {
		dirs = append(dirs, upper)
	}
	if lower != "" {
		dirs = append(dirs, strings.Split(lower, ":")...)
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
			l.Path = layerDir

			// Extract cache ID from the overlay2 path.
			// Path is typically /var/lib/docker/overlay2/<cache-id>/diff
			parts := strings.Split(filepath.Clean(layerDir), string(filepath.Separator))
			for j, part := range parts {
				if part == "overlay2" && j+1 < len(parts) {
					l.Docker.CacheID = parts[j+1]
					break
				}
			}

			l.UsageSize, l.UsageInodes = dirDiskUsage(layerDir)
		}

		layers = append(layers, l)
	}

	return layers, nil
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
