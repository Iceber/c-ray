package containerd

import (
	"testing"

	"github.com/opencontainers/go-digest"
)

func TestContentPathUsesContainerdContentStoreLayout(t *testing.T) {
	dgst := digest.Digest("sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	got := contentPath("/var/lib/containerd", dgst)
	want := "/var/lib/containerd/io.containerd.content.v1.content/blobs/sha256/0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if got != want {
		t.Fatalf("contentPath() = %s, want %s", got, want)
	}
}
