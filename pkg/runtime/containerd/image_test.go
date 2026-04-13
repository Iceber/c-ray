package containerd

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/containerd/containerd/v2/client"
	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/core/images"
	"github.com/containerd/platforms"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestContentPathUsesContainerdContentStoreLayout(t *testing.T) {
	dgst := digest.Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	got := contentPath("/var/lib/containerd", dgst)
	want := "/var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got != want {
		t.Fatalf("contentPath() = %s, want %s", got, want)
	}
}

func TestGroupImagesByDigestMergesAliases(t *testing.T) {
	sharedDigest := digest.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111")
	imgs := []fakeImage{
		{name: "repo:v2", target: ocispec.Descriptor{Digest: sharedDigest}},
		{name: "repo:latest", target: ocispec.Descriptor{Digest: sharedDigest}},
		{name: "other:v1", target: ocispec.Descriptor{Digest: digest.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222")}},
	}

	grouped := groupImagesByDigest(fakeImagesToClientImages(imgs))
	if len(grouped) != 2 {
		t.Fatalf("len(grouped) = %d, want 2", len(grouped))
	}
	if grouped[0].digest != sharedDigest.String() {
		t.Fatalf("grouped[0].digest = %s, want %s", grouped[0].digest, sharedDigest)
	}
	wantNames := []string{"repo:latest", "repo:v2"}
	if len(grouped[0].names) != len(wantNames) {
		t.Fatalf("grouped[0].names = %#v, want %#v", grouped[0].names, wantNames)
	}
	for i, want := range wantNames {
		if grouped[0].names[i] != want {
			t.Fatalf("grouped[0].names[%d] = %s, want %s", i, grouped[0].names[i], want)
		}
	}
	if grouped[0].raw.Name() != "repo:v2" {
		t.Fatalf("grouped[0].raw.Name() = %s, want repo:v2", grouped[0].raw.Name())
	}
	if grouped[1].names[0] != "other:v1" {
		t.Fatalf("grouped[1].names = %#v, want [other:v1]", grouped[1].names)
	}
	if handle := newGroupedImageHandle(&Runtime{}, grouped[0].raw, grouped[0].names); handle.Ref() != "repo:latest" {
		t.Fatalf("newGroupedImageHandle().Ref() = %s, want repo:latest", handle.Ref())
	}
}

type fakeImage struct {
	name   string
	target ocispec.Descriptor
	meta   images.Image
}

func (f fakeImage) Name() string                                              { return f.name }
func (f fakeImage) Target() ocispec.Descriptor                                { return f.target }
func (f fakeImage) Labels() map[string]string                                 { return nil }
func (f fakeImage) Unpack(context.Context, string, ...client.UnpackOpt) error { return nil }
func (f fakeImage) RootFS(context.Context) ([]digest.Digest, error)           { return nil, nil }
func (f fakeImage) Size(context.Context) (int64, error)                       { return 0, nil }
func (f fakeImage) Usage(context.Context, ...client.UsageOpt) (int64, error)  { return 0, nil }
func (f fakeImage) Config(context.Context) (ocispec.Descriptor, error) {
	return ocispec.Descriptor{}, nil
}
func (f fakeImage) IsUnpacked(context.Context, string) (bool, error) { return false, nil }
func (f fakeImage) ContentStore() content.Store                      { return nil }
func (f fakeImage) Metadata() images.Image                           { return f.meta }
func (f fakeImage) Platform() platforms.MatchComparer                { return nil }
func (f fakeImage) Spec(context.Context) (ocispec.Image, error)      { return ocispec.Image{}, nil }

func fakeImagesToClientImages(imgs []fakeImage) []client.Image {
	result := make([]client.Image, 0, len(imgs))
	for _, img := range imgs {
		result = append(result, img)
	}
	return result
}

// ---------------------------------------------------------------------------
// fakeImageService implements imageServiceAPI for GetImage unit tests.
// ---------------------------------------------------------------------------

type fakeImageService struct {
	// images stored by name
	byName map[string]fakeImage
	// error to return from ListImages, if non-nil
	listErr error
}

func newFakeImageService(imgs ...fakeImage) *fakeImageService {
	s := &fakeImageService{byName: make(map[string]fakeImage, len(imgs))}
	for _, img := range imgs {
		s.byName[img.name] = img
	}
	return s
}

func (s *fakeImageService) GetImage(_ context.Context, ref string) (client.Image, error) {
	img, ok := s.byName[ref]
	if !ok {
		return nil, fmt.Errorf("image not found: %s", ref)
	}
	return img, nil
}

func (s *fakeImageService) ListImages(_ context.Context, filters ...string) ([]client.Image, error) {
	if s.listErr != nil {
		return nil, s.listErr
	}
	// filter: "target.digest==<digest>" — return all images whose target.Digest matches.
	var result []client.Image
	for _, f := range filters {
		const prefix = "target.digest=="
		if !strings.HasPrefix(f, prefix) {
			continue
		}
		want := strings.TrimPrefix(f, prefix)
		for _, img := range s.byName {
			if img.target.Digest.String() == want {
				result = append(result, img)
			}
		}
	}
	return result, nil
}

// ---------------------------------------------------------------------------
// resolveImageWithAliases tests
// ---------------------------------------------------------------------------

func TestResolveImageWithAliases_ReturnsAllSiblingNames(t *testing.T) {
	dgst := digest.Digest("sha256:aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111aaaa1111")
	svc := newFakeImageService(
		fakeImage{name: "repo:v1", target: ocispec.Descriptor{Digest: dgst}},
		fakeImage{name: "repo:latest", target: ocispec.Descriptor{Digest: dgst}},
		fakeImage{name: "mirror:v1", target: ocispec.Descriptor{Digest: dgst}},
	)

	img, err := resolveImageWithAliases(context.Background(), svc, &Runtime{}, "repo:v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := img.Info(context.Background())
	if err != nil {
		t.Fatalf("Info() error: %v", err)
	}

	// Names must be sorted and contain all three aliases.
	wantNames := []string{"mirror:v1", "repo:latest", "repo:v1"}
	if len(info.Names) != len(wantNames) {
		t.Fatalf("Names = %#v, want %#v", info.Names, wantNames)
	}
	for i, want := range wantNames {
		if info.Names[i] != want {
			t.Fatalf("Names[%d] = %s, want %s", i, info.Names[i], want)
		}
	}
}

func TestResolveImageWithAliases_RefUsesFirstSortedName(t *testing.T) {
	dgst := digest.Digest("sha256:bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222bbbb2222")
	svc := newFakeImageService(
		fakeImage{name: "zzz:tag", target: ocispec.Descriptor{Digest: dgst}},
		fakeImage{name: "aaa:tag", target: ocispec.Descriptor{Digest: dgst}},
	)

	img, err := resolveImageWithAliases(context.Background(), svc, &Runtime{}, "zzz:tag")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img.Ref() != "aaa:tag" {
		t.Fatalf("Ref() = %s, want aaa:tag (first sorted alias)", img.Ref())
	}
}

func TestResolveImageWithAliases_SingleName(t *testing.T) {
	dgst := digest.Digest("sha256:cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333cccc3333")
	svc := newFakeImageService(
		fakeImage{name: "solo:v1", target: ocispec.Descriptor{Digest: dgst}},
	)

	img, err := resolveImageWithAliases(context.Background(), svc, &Runtime{}, "solo:v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, _ := img.Info(context.Background())
	if len(info.Names) != 1 || info.Names[0] != "solo:v1" {
		t.Fatalf("Names = %#v, want [solo:v1]", info.Names)
	}
}

func TestResolveImageWithAliases_EmptyDigestSkipsAliasLookup(t *testing.T) {
	// image has no target digest → should return a single-name handle without
	// calling ListImages (which would panic on listErr in this test).
	svc := newFakeImageService(
		fakeImage{name: "no-digest:v1", target: ocispec.Descriptor{Digest: ""}},
	)
	svc.listErr = fmt.Errorf("ListImages must not be called")

	img, err := resolveImageWithAliases(context.Background(), svc, &Runtime{}, "no-digest:v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if img.Ref() != "no-digest:v1" {
		t.Fatalf("Ref() = %s, want no-digest:v1", img.Ref())
	}
}

func TestResolveImageWithAliases_GetImageNotFound(t *testing.T) {
	svc := newFakeImageService() // empty store

	_, err := resolveImageWithAliases(context.Background(), svc, &Runtime{}, "missing:v1")
	if err == nil {
		t.Fatal("expected error for missing image, got nil")
	}
	if !strings.Contains(err.Error(), "missing:v1") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBuildCurrentManifestFallsBackToConfigPlatform(t *testing.T) {
	h := &imageHandle{rt: &Runtime{}}
	meta := &imageMeta{
		manifestDesc: ocispec.Descriptor{
			Digest: digest.Digest("sha256:1111111111111111111111111111111111111111111111111111111111111111"),
		},
		configDesc: ocispec.Descriptor{
			Digest: digest.Digest("sha256:2222222222222222222222222222222222222222222222222222222222222222"),
		},
		platform: "linux/arm64/v8",
	}

	manifest := h.buildCurrentManifest(context.Background(), meta)
	if manifest.Platform != "linux/arm64/v8" {
		t.Fatalf("Manifest.Platform = %q, want linux/arm64/v8", manifest.Platform)
	}
}

func TestResolveImageWithAliases_ListImagesError(t *testing.T) {
	dgst := digest.Digest("sha256:dddd4444dddd4444dddd4444dddd4444dddd4444dddd4444dddd4444dddd4444")
	svc := newFakeImageService(
		fakeImage{name: "img:v1", target: ocispec.Descriptor{Digest: dgst}},
	)
	svc.listErr = fmt.Errorf("storage unavailable")

	_, err := resolveImageWithAliases(context.Background(), svc, &Runtime{}, "img:v1")
	if err == nil {
		t.Fatal("expected error when ListImages fails, got nil")
	}
	if !strings.Contains(err.Error(), "storage unavailable") {
		t.Fatalf("error %q should contain the root cause", err.Error())
	}
}

func TestResolveImageWithAliases_IsolatesByDigest(t *testing.T) {
	// Two images with different digests: querying one must not return the other's name.
	dgst1 := digest.Digest("sha256:eeee5555eeee5555eeee5555eeee5555eeee5555eeee5555eeee5555eeee5555")
	dgst2 := digest.Digest("sha256:ffff6666ffff6666ffff6666ffff6666ffff6666ffff6666ffff6666ffff6666")
	svc := newFakeImageService(
		fakeImage{name: "group1:v1", target: ocispec.Descriptor{Digest: dgst1}},
		fakeImage{name: "group1:latest", target: ocispec.Descriptor{Digest: dgst1}},
		fakeImage{name: "group2:v1", target: ocispec.Descriptor{Digest: dgst2}},
	)

	img, err := resolveImageWithAliases(context.Background(), svc, &Runtime{}, "group1:v1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	info, _ := img.Info(context.Background())
	for _, name := range info.Names {
		if name == "group2:v1" {
			t.Fatalf("Names contains group2:v1 which has a different digest: %#v", info.Names)
		}
	}
	if len(info.Names) != 2 {
		t.Fatalf("Names = %#v, want exactly 2 entries for group1", info.Names)
	}
}
