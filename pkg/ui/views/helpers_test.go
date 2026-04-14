package views

import (
	"strings"
	"testing"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
)

func TestImagePlatformHelpers(t *testing.T) {
	info := &runtime.ImageConfigInfo{
		Manifest: &runtime.ImageManifest{Platform: "linux/arm64/v8"},
		Manifests: []*runtime.ImageManifest{
			{Platform: "linux/amd64"},
			{Platform: "linux/arm64/v8"},
		},
	}

	if got := imageCurrentPlatform(info); got != "linux/arm64/v8" {
		t.Fatalf("imageCurrentPlatform() = %q, want linux/arm64/v8", got)
	}
	if got := imagePlatformCount(info); got != 2 {
		t.Fatalf("imagePlatformCount() = %d, want 2", got)
	}
	if got := imagePlatformsSummary(info); got != "2 manifests" {
		t.Fatalf("imagePlatformsSummary() = %q, want 2 manifests", got)
	}

	single := &runtime.ImageConfigInfo{Manifest: &runtime.ImageManifest{}}
	if got := imagePlatformCount(single); got != 1 {
		t.Fatalf("single imagePlatformCount() = %d, want 1", got)
	}
	if got := imagePlatformsSummary(single); got != "1 manifest" {
		t.Fatalf("single imagePlatformsSummary() = %q, want 1 manifest", got)
	}

	if got := imagePlatformsSummary(nil); got != "" {
		t.Fatalf("nil imagePlatformsSummary() = %q, want empty", got)
	}
}

func TestImageSupportedPlatformsSummaryHighlightsCurrent(t *testing.T) {
	info := &runtime.ImageConfigInfo{
		Manifest: &runtime.ImageManifest{Platform: "linux/arm64/v8"},
		Manifests: []*runtime.ImageManifest{
			{Platform: "linux/amd64"},
			{Platform: "linux/arm64/v8"},
		},
	}

	line := imageSupportedPlatformsSummary(info)
	accent := "[" + components.ColorName(components.ColorFgAccentAlt) + "::b]linux/arm64/v8[-:-:-]"
	if !strings.Contains(line, accent) {
		t.Fatalf("current platform summary = %q, want accent-highlighted current manifest", line)
	}
	if !strings.Contains(line, "(current)") {
		t.Fatalf("current platform summary = %q, want current marker", line)
	}
	if !strings.Contains(line, "linux/amd64") {
		t.Fatalf("platform summary = %q, want linux/amd64", line)
	}
	if strings.Count(line, ",") != 1 {
		t.Fatalf("platform summary = %q, want single-line comma-separated output", line)
	}
}

func TestImageDetailOtherNamesExcludesPrimary(t *testing.T) {
	config := &runtime.ContainerConfig{ImageRef: "repo:v1"}
	info := &runtime.ImageInfo{Names: []string{"repo:v1", "repo:latest", "repo@sha256:deadbeef", "repo:latest"}}

	got := imageDetailOtherNames(config, info)
	want := "repo:latest, repo@sha256:deadbeef"
	if got != want {
		t.Fatalf("imageDetailOtherNames() = %q, want %q", got, want)
	}
}

func TestImageKindSchemaSummary(t *testing.T) {
	info := &runtime.ImageConfigInfo{TargetKind: "Manifest", Schema: "OCIv1"}
	if got := imageKindSchemaSummary(info); got != "Manifest / OCIv1" {
		t.Fatalf("imageKindSchemaSummary() = %q, want Manifest / OCIv1", got)
	}
}

func TestWrapImageValueWrapsLongName(t *testing.T) {
	wrapped := wrapImageValue("docker.io/library/alpine:3.22", 10)
	if len(wrapped) < 2 {
		t.Fatalf("wrapImageValue() lines = %v, want wrapped output", wrapped)
	}
	for _, line := range wrapped[:len(wrapped)-1] {
		if len([]rune(line)) != 10 {
			t.Fatalf("wrapImageValue() line %q len = %d, want 10", line, len([]rune(line)))
		}
	}
}
