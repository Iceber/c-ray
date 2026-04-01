package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	"github.com/icebergu/c-ray/pkg/runtime"
)

func TestImageLayersOverlay2ResolvesShortLinks(t *testing.T) {
	tmp := t.TempDir()
	overlayRoot := filepath.Join(tmp, "overlay2")
	for _, dir := range []string{
		filepath.Join(overlayRoot, "base-cache", "diff"),
		filepath.Join(overlayRoot, "mid-cache", "diff"),
		filepath.Join(overlayRoot, "top-cache", "diff"),
		filepath.Join(overlayRoot, "l"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s): %v", dir, err)
		}
	}

	baseDiff := filepath.Join(overlayRoot, "base-cache", "diff")
	midDiff := filepath.Join(overlayRoot, "mid-cache", "diff")
	topDiff := filepath.Join(overlayRoot, "top-cache", "diff")
	baseResolved := mustEvalSymlinks(t, baseDiff)
	midResolved := mustEvalSymlinks(t, midDiff)
	if err := os.WriteFile(filepath.Join(baseDiff, "base.txt"), []byte("base-layer"), 0o644); err != nil {
		t.Fatalf("write base file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(midDiff, "mid.txt"), []byte("mid-layer-data"), 0o644); err != nil {
		t.Fatalf("write mid file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(topDiff, "top.txt"), []byte("top-layer-data"), 0o644); err != nil {
		t.Fatalf("write top file: %v", err)
	}

	baseLink := filepath.Join(overlayRoot, "l", "BASE123")
	midLink := filepath.Join(overlayRoot, "l", "MID456")
	if err := os.Symlink(filepath.Join("..", "base-cache", "diff"), baseLink); err != nil {
		t.Fatalf("create base symlink: %v", err)
	}
	if err := os.Symlink(filepath.Join("..", "mid-cache", "diff"), midLink); err != nil {
		t.Fatalf("create mid symlink: %v", err)
	}

	h := newLoadedDockerImageHandle(&dockertypes.ImageInspect{
		RootFS: dockertypes.RootFS{
			Type:   "layers",
			Layers: []string{"sha256:base", "sha256:mid", "sha256:top"},
		},
		GraphDriver: dockertypes.GraphDriverData{
			Name: "overlay2",
			Data: map[string]string{
				"UpperDir": topDiff,
				"LowerDir": midLink + ":" + baseLink,
			},
		},
	})

	layers, err := h.Layers(context.Background(), runtime.LayerQuery{})
	if err != nil {
		t.Fatalf("Layers() error: %v", err)
	}
	if len(layers) != 3 {
		t.Fatalf("len(layers) = %d, want 3", len(layers))
	}

	assertLayer := func(idx int, wantDigest, wantPath, wantCacheID, wantShortLinkID, wantShortLinkPath string, minSize int64) {
		t.Helper()
		layer := layers[idx]
		if layer.UncompressedDigest != wantDigest {
			t.Fatalf("layer[%d].UncompressedDigest = %s, want %s", idx, layer.UncompressedDigest, wantDigest)
		}
		if layer.Path != wantPath {
			t.Fatalf("layer[%d].Path = %s, want %s", idx, layer.Path, wantPath)
		}
		if layer.Docker == nil || layer.Docker.CacheID != wantCacheID {
			t.Fatalf("layer[%d].Docker.CacheID = %v, want %s", idx, layer.Docker, wantCacheID)
		}
		if layer.Docker == nil || layer.Docker.ShortLinkID != wantShortLinkID {
			t.Fatalf("layer[%d].Docker.ShortLinkID = %v, want %s", idx, layer.Docker, wantShortLinkID)
		}
		if layer.Docker == nil || layer.Docker.ShortLinkPath != wantShortLinkPath {
			t.Fatalf("layer[%d].Docker.ShortLinkPath = %v, want %s", idx, layer.Docker, wantShortLinkPath)
		}
		if layer.UsageSize < minSize {
			t.Fatalf("layer[%d].UsageSize = %d, want at least %d", idx, layer.UsageSize, minSize)
		}
		if layer.UsageInodes == 0 {
			t.Fatalf("layer[%d].UsageInodes = 0, want > 0", idx)
		}
	}

	assertLayer(0, "sha256:base", baseResolved, "base-cache", "BASE123", baseLink, int64(len("base-layer")))
	assertLayer(1, "sha256:mid", midResolved, "mid-cache", "MID456", midLink, int64(len("mid-layer-data")))
	assertLayer(2, "sha256:top", topDiff, "top-cache", "", "", int64(len("top-layer-data")))
}

func TestImageLayersOverlay2FallsBackToSummaryOnDirCountMismatch(t *testing.T) {
	tmp := t.TempDir()
	topDiff := filepath.Join(tmp, "overlay2", "top-cache", "diff")
	if err := os.MkdirAll(topDiff, 0o755); err != nil {
		t.Fatalf("MkdirAll(%s): %v", topDiff, err)
	}

	h := newLoadedDockerImageHandle(&dockertypes.ImageInspect{
		RootFS: dockertypes.RootFS{
			Type:   "layers",
			Layers: []string{"sha256:base", "sha256:top"},
		},
		GraphDriver: dockertypes.GraphDriverData{
			Name: "overlay2",
			Data: map[string]string{
				"UpperDir": topDiff,
			},
		},
	})

	layers, err := h.Layers(context.Background(), runtime.LayerQuery{})
	if err != nil {
		t.Fatalf("Layers() error: %v", err)
	}
	if len(layers) != 2 {
		t.Fatalf("len(layers) = %d, want 2", len(layers))
	}
	for i, layer := range layers {
		if layer.Path != "" {
			t.Fatalf("layer[%d].Path = %s, want empty summary path", i, layer.Path)
		}
		if layer.UsageSize != 0 || layer.UsageInodes != 0 {
			t.Fatalf("layer[%d] usage = (%d, %d), want summary-only zero values", i, layer.UsageSize, layer.UsageInodes)
		}
		if layer.Docker == nil || layer.Docker.GraphDriver != "overlay2" {
			t.Fatalf("layer[%d].Docker = %+v, want overlay2 summary metadata", i, layer.Docker)
		}
	}
}

func newLoadedDockerImageHandle(inspect *dockertypes.ImageInspect) *imageHandle {
	h := &imageHandle{
		rt:      &Runtime{},
		ref:     "test-image",
		inspect: inspect,
	}
	h.inspectOnce.Do(func() {})
	return h
}

func mustEvalSymlinks(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("EvalSymlinks(%s): %v", path, err)
	}
	return resolved
}
