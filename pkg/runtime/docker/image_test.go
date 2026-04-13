package docker

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
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

func TestImageInfoReturnsAllNames(t *testing.T) {
	h := newLoadedDockerImageHandle(&dockertypes.ImageInspect{
		ID:          "sha256:imageid",
		Size:        42,
		Created:     "2024-01-02T03:04:05.123456789Z",
		RepoTags:    []string{"repo:v1", "repo:latest"},
		RepoDigests: []string{"repo@sha256:deadbeef"},
	})

	info, err := h.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}

	want := []string{"repo:v1", "repo:latest", "repo@sha256:deadbeef"}
	if !reflect.DeepEqual(info.Names, want) {
		t.Fatalf("Info().Names = %#v, want %#v", info.Names, want)
	}
}

func TestImageConfigReturnsPlatform(t *testing.T) {
	h := newLoadedDockerImageHandle(&dockertypes.ImageInspect{
		ID:           "sha256:11223344556677889900aabbccddeeff",
		Architecture: "arm64",
		Variant:      "v8",
		Os:           "linux",
		RepoDigests:  []string{"alpine@sha256:abcdef1234567890"},
	})
	h.rt.daemonInfo = &daemonInfo{
		DockerRootDir: "/var/lib/docker",
		Driver:        "overlay2",
	}
	h.inspectRaw = []byte(`{"Descriptor":{"mediaType":"application/vnd.docker.distribution.manifest.v2+json","digest":"sha256:abcdef1234567890"}}`)

	info, err := h.Config(context.Background())
	if err != nil {
		t.Fatalf("Config() error: %v", err)
	}
	if info.Manifest == nil {
		t.Fatal("Config().Manifest = nil, want non-nil")
	}
	if info.Manifest.Platform != "linux/arm64/v8" {
		t.Fatalf("Config().Manifest.Platform = %q, want linux/arm64/v8", info.Manifest.Platform)
	}
	if info.TargetKind != "Manifest" {
		t.Fatalf("Config().TargetKind = %q, want Manifest", info.TargetKind)
	}
	if info.Schema != "Docker" {
		t.Fatalf("Config().Schema = %q, want Docker", info.Schema)
	}
	if info.Manifest.Digest != "sha256:abcdef1234567890" {
		t.Fatalf("Config().Manifest.Digest = %q, want sha256:abcdef1234567890", info.Manifest.Digest)
	}
	if info.Manifest.Path != "/var/lib/docker/content/data/blobs/sha256/abcdef1234567890" {
		t.Fatalf("Config().Manifest.Path = %q, want docker classic manifest content path", info.Manifest.Path)
	}
	if info.Manifest.ConfigPath != "/var/lib/docker/image/overlay2/imagedb/content/sha256/11223344556677889900aabbccddeeff" {
		t.Fatalf("Config().Manifest.ConfigPath = %q, want docker classic config path", info.Manifest.ConfigPath)
	}
	if len(info.Manifests) != 1 {
		t.Fatalf("len(Config().Manifests) = %d, want 1", len(info.Manifests))
	}
}

func TestImageConfigDescriptorIndexDoesNotMasqueradeAsPlatformManifest(t *testing.T) {
	h := newLoadedDockerImageHandle(&dockertypes.ImageInspect{
		ID:           "sha256:11223344556677889900aabbccddeeff",
		Architecture: "arm64",
		Variant:      "v8",
		Os:           "linux",
		RepoDigests:  []string{"alpine@sha256:55ae5d250caebc548793f321534bc6a8ef1d116f334f18f4ada1b2daad3251b2"},
	})
	h.rt.daemonInfo = &daemonInfo{
		DockerRootDir: "/var/lib/docker",
		Driver:        "overlay2",
	}
	h.inspectRaw = []byte(`{"Descriptor":{"mediaType":"application/vnd.oci.image.index.v1+json","digest":"sha256:55ae5d250caebc548793f321534bc6a8ef1d116f334f18f4ada1b2daad3251b2"}}`)

	info, err := h.Config(context.Background())
	if err != nil {
		t.Fatalf("Config() error: %v", err)
	}
	if info.TargetKind != "Index" {
		t.Fatalf("Config().TargetKind = %q, want Index", info.TargetKind)
	}
	if info.Schema != "OCI" {
		t.Fatalf("Config().Schema = %q, want OCI", info.Schema)
	}
	if info.Manifest == nil {
		t.Fatal("Config().Manifest = nil, want non-nil")
	}
	if info.Manifest.Digest != "" {
		t.Fatalf("Config().Manifest.Digest = %q, want empty for index target", info.Manifest.Digest)
	}
	if info.IndexPath != "/var/lib/docker/content/data/blobs/sha256/55ae5d250caebc548793f321534bc6a8ef1d116f334f18f4ada1b2daad3251b2" {
		t.Fatalf("Config().IndexPath = %q, want docker classic index content path", info.IndexPath)
	}
	if len(info.Manifests) != 0 {
		t.Fatalf("len(Config().Manifests) = %d, want 0 when Docker API only exposes index descriptor", len(info.Manifests))
	}
}

func TestDockerClassicConfigPathRequiresDaemonRootAndDigestID(t *testing.T) {
	if got := dockerClassicConfigPath(nil, "sha256:abc"); got != "" {
		t.Fatalf("dockerClassicConfigPath(nil) = %q, want empty", got)
	}
	if got := dockerClassicConfigPath(&Runtime{daemonInfo: &daemonInfo{DockerRootDir: "/var/lib/docker", Driver: "overlay2"}}, "not-a-digest"); got != "" {
		t.Fatalf("dockerClassicConfigPath(non-digest) = %q, want empty", got)
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
