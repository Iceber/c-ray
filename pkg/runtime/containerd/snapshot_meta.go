//go:build linux

package containerd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/containerd/containerd/v2/core/snapshots/storage"
	"golang.org/x/sys/unix"
)

// snapshotMetaReader resolves snapshot filesystem paths by reading the
// snapshotter's local metadata.db directly. This is used as a fallback when no
// active (RW) snapshot key is available — e.g. for image-only queries where
// the image has been unpacked but no container is running on top of it.
type snapshotMetaReader struct {
	snapshotterRoot string // e.g. <containerd-root>/io.containerd.snapshotter.v1.overlayfs
	snapshotterName string // e.g. "overlayfs"
}

// newSnapshotMetaReader returns a reader for the given snapshotter, or nil if
// the metadata.db file does not exist at the expected path.
func newSnapshotMetaReader(containerdRoot, snapshotterName string) *snapshotMetaReader {
	root := filepath.Join(containerdRoot,
		fmt.Sprintf("io.containerd.snapshotter.v1.%s", snapshotterName))

	dbPath := filepath.Join(root, "metadata.db")
	if _, err := os.Stat(dbPath); err != nil {
		return nil
	}
	return &snapshotMetaReader{
		snapshotterRoot: root,
		snapshotterName: snapshotterName,
	}
}

// resolveLayerPaths maps a sequence of snapshot keys (typically chainIDs in
// layer order from base to top) to their on-disk paths. The returned slice has
// the same length as keys; entries that could not be resolved are empty strings.
func (r *snapshotMetaReader) resolveLayerPaths(ctx context.Context, keys []string) []string {
	if r == nil || len(keys) == 0 {
		return nil
	}

	dbPath := filepath.Join(r.snapshotterRoot, "metadata.db")

	// containerd holds flock(LOCK_EX) on metadata.db via bbolt. Opening the
	// same path with storage.NewMetaStore would also call flock(LOCK_EX) and
	// block indefinitely.
	//
	// memfd_create(2) creates an anonymous inode backed purely by kernel RAM —
	// no filesystem path, zero disk I/O. We read the db bytes with os.ReadFile
	// (read() syscalls bypass advisory flock entirely), write them into the
	// memfd, then open a MetaStore via /proc/self/fd/<n>. bbolt acquires flock
	// only on our private inode, with no contention from containerd.
	//
	// bbolt shadow-paging safety: bbolt never overwrites committed pages in
	// place; it writes new pages to fresh offsets and swaps the meta page
	// atomically. os.ReadFile therefore captures a consistent snapshot of the
	// last committed transaction even if a concurrent write is in progress.
	ms, cleanup, err := openMetaDBInMemfd(dbPath)
	if err != nil {
		return nil
	}
	defer cleanup()

	paths := make([]string, len(keys))
	for i, key := range keys {
		paths[i] = r.resolveOne(ctx, ms, key)
	}
	return paths
}

// resolveOne looks up a single snapshot key and returns its host path, or ""
// on any error.
func (r *snapshotMetaReader) resolveOne(ctx context.Context, ms *storage.MetaStore, key string) string {
	var id string
	err := ms.WithTransaction(ctx, false, func(ctx context.Context) error {
		sid, _, _, err := storage.GetInfo(ctx, key)
		if err != nil {
			return err
		}
		id = sid
		return nil
	})
	if err != nil || id == "" {
		return ""
	}
	return r.snapshotDir(id)
}

// snapshotDir returns the host directory for a snapshot with the given numeric
// ID, accounting for snapshotter-specific layout conventions.
func (r *snapshotMetaReader) snapshotDir(id string) string {
	base := filepath.Join(r.snapshotterRoot, "snapshots", id)
	switch r.snapshotterName {
	case "overlayfs":
		// overlayfs stores each layer's extracted content under <id>/fs.
		return base + "/fs"
	default:
		// native and most other snapshotters use <id> directly.
		return base
	}
}

// openMetaDBInMemfd reads src into a memfd-backed anonymous inode and opens a
// MetaStore on it. The returned cleanup func closes both the MetaStore and the
// underlying memfd, dropping the last inode reference and freeing the memory.
func openMetaDBInMemfd(src string) (*storage.MetaStore, func(), error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, nil, fmt.Errorf("read metadata.db: %w", err)
	}

	fd, err := unix.MemfdCreate("cray-snapshot-meta", unix.MFD_CLOEXEC)
	if err != nil {
		return nil, nil, fmt.Errorf("memfd_create: %w", err)
	}
	memfd := os.NewFile(uintptr(fd), "cray-snapshot-meta")

	if _, err := memfd.Write(data); err != nil {
		memfd.Close()
		return nil, nil, fmt.Errorf("write memfd: %w", err)
	}

	// /proc/self/fd/<n> is a kernel symlink to the anonymous inode.
	// storage.NewMetaStore opens a new fd to the same inode; bbolt calls
	// flock(LOCK_EX) only on that new fd, with no contention from containerd.
	path := fmt.Sprintf("/proc/self/fd/%d", fd)
	ms, err := storage.NewMetaStore(path)
	if err != nil {
		memfd.Close()
		return nil, nil, fmt.Errorf("open metastore on memfd: %w", err)
	}

	return ms, func() { ms.Close(); memfd.Close() }, nil
}