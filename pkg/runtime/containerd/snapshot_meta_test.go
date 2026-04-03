//go:build linux

package containerd

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/snapshots"
	"github.com/containerd/containerd/v2/core/snapshots/storage"
)

// --------------------------------------------------------------------------
// snapshotMetaReader unit tests
// --------------------------------------------------------------------------

// setupTestMetaDB creates a temporary snapshotter root with a real metadata.db
// and commits the given keys. Returns the root dir and a cleanup function.
func setupTestMetaDB(t *testing.T, snapshotterName string, keys []string) string {
	t.Helper()
	root := t.TempDir()
	snRoot := filepath.Join(root, "io.containerd.snapshotter.v1."+snapshotterName)
	if err := os.MkdirAll(filepath.Join(snRoot, "snapshots"), 0o755); err != nil {
		t.Fatal(err)
	}

	dbPath := filepath.Join(snRoot, "metadata.db")
	ms, err := storage.NewMetaStore(dbPath)
	if err != nil {
		t.Fatalf("NewMetaStore: %v", err)
	}
	defer ms.Close()

	// Populate: for each key, create an active snapshot then commit it.
	// The keys are created in order so they form a parent chain.
	ctx := context.Background()
	var parentKey string
	for i, key := range keys {
		activeKey := key + "-active"
		err := ms.WithTransaction(ctx, true, func(ctx context.Context) error {
			_, err := storage.CreateSnapshot(ctx, snapshots.KindActive, activeKey, parentKey)
			if err != nil {
				return err
			}
			_, err = storage.CommitActive(ctx, activeKey, key, snapshots.Usage{
				Size:   int64((i + 1) * 1024),
				Inodes: int64(i + 1),
			})
			return err
		})
		if err != nil {
			t.Fatalf("populate key %q: %v", key, err)
		}
		parentKey = key
	}

	return root
}

func TestResolveLayerPaths_Overlayfs(t *testing.T) {
	keys := []string{"sha256:aaa", "sha256:bbb", "sha256:ccc"}
	root := setupTestMetaDB(t, "overlayfs", keys)

	reader := newSnapshotMetaReader(root, "overlayfs")
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	paths := reader.resolveLayerPaths(context.Background(), keys)
	if len(paths) != 3 {
		t.Fatalf("expected 3 paths, got %d", len(paths))
	}

	snRoot := filepath.Join(root, "io.containerd.snapshotter.v1.overlayfs")
	for i, p := range paths {
		if p == "" {
			t.Errorf("path[%d] is empty", i)
			continue
		}
		// Each path should be <snRoot>/snapshots/<id>/fs.
		// The IDs are numeric; we just verify the structure.
		rel, err := filepath.Rel(snRoot, p)
		if err != nil {
			t.Errorf("path[%d] not under snapshot root: %v", i, err)
			continue
		}
		dir, base := filepath.Split(rel)
		if base != "fs" {
			t.Errorf("path[%d] should end with /fs, got %q", i, p)
		}
		if !matchesSnapshotDir(dir) {
			t.Errorf("path[%d] unexpected dir structure: %q", i, dir)
		}
	}
}

func TestResolveLayerPaths_Native(t *testing.T) {
	keys := []string{"sha256:x1", "sha256:x2"}
	root := setupTestMetaDB(t, "native", keys)

	reader := newSnapshotMetaReader(root, "native")
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	paths := reader.resolveLayerPaths(context.Background(), keys)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}

	snRoot := filepath.Join(root, "io.containerd.snapshotter.v1.native")
	for i, p := range paths {
		if p == "" {
			t.Errorf("path[%d] is empty", i)
			continue
		}
		rel, err := filepath.Rel(snRoot, p)
		if err != nil {
			t.Errorf("path[%d] not under snapshot root: %v", i, err)
			continue
		}
		// native should NOT end with /fs.
		if filepath.Base(rel) == "fs" {
			t.Errorf("path[%d] should not end with /fs for native, got %q", i, p)
		}
	}
}

func TestResolveLayerPaths_MissingKey(t *testing.T) {
	keys := []string{"sha256:exist"}
	root := setupTestMetaDB(t, "overlayfs", keys)

	reader := newSnapshotMetaReader(root, "overlayfs")
	if reader == nil {
		t.Fatal("expected non-nil reader")
	}

	// Query with one valid key and one missing key.
	query := []string{"sha256:exist", "sha256:missing"}
	paths := reader.resolveLayerPaths(context.Background(), query)
	if len(paths) != 2 {
		t.Fatalf("expected 2 paths, got %d", len(paths))
	}
	if paths[0] == "" {
		t.Error("expected path[0] to be resolved")
	}
	if paths[1] != "" {
		t.Errorf("expected path[1] to be empty for missing key, got %q", paths[1])
	}
}

func TestResolveLayerPaths_NilReader(t *testing.T) {
	var reader *snapshotMetaReader
	paths := reader.resolveLayerPaths(context.Background(), []string{"sha256:any"})
	if paths != nil {
		t.Errorf("expected nil paths from nil reader, got %v", paths)
	}
}

func TestNewSnapshotMetaReader_NoMetadataDB(t *testing.T) {
	root := t.TempDir()
	reader := newSnapshotMetaReader(root, "overlayfs")
	if reader != nil {
		t.Error("expected nil reader when metadata.db does not exist")
	}
}

// matchesSnapshotDir checks that a relative path looks like "snapshots/<id>/".
func matchesSnapshotDir(dir string) bool {
	// dir is e.g. "snapshots/1/"
	parts := filepath.SplitList(dir)
	if len(parts) == 0 {
		// filepath.SplitList uses os.PathListSeparator, use Split manually
		clean := filepath.Clean(dir)
		first := filepath.Dir(clean)
		return filepath.Base(first) == "snapshots"
	}
	return true
}
