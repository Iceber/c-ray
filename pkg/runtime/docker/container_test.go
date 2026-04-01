package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	pkgruntime "github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

func TestContainerConfigFallsBackToLiveMountPaths(t *testing.T) {
	h, upperDir, baseLayer := newTestDockerContainerHandle(t)

	cfg, err := h.Config(context.Background())
	if err != nil {
		t.Fatalf("Config() error: %v", err)
	}
	if cfg.Snapshotter != "overlayfs" {
		t.Fatalf("Snapshotter = %s, want overlayfs", cfg.Snapshotter)
	}
	if cfg.WritableLayerPath != upperDir {
		t.Fatalf("WritableLayerPath = %s, want %s", cfg.WritableLayerPath, upperDir)
	}
	if cfg.ReadOnlyLayerPath != baseLayer {
		t.Fatalf("ReadOnlyLayerPath = %s, want %s", cfg.ReadOnlyLayerPath, baseLayer)
	}
}

func TestContainerStorageFallsBackToLiveMountRWPath(t *testing.T) {
	h, upperDir, _ := newTestDockerContainerHandle(t)

	storage, err := h.Storage(context.Background())
	if err != nil {
		t.Fatalf("Storage() error: %v", err)
	}
	if storage.GraphDriver != "overlayfs" {
		t.Fatalf("GraphDriver = %s, want overlayfs", storage.GraphDriver)
	}
	if storage.RWLayerPath != upperDir {
		t.Fatalf("RWLayerPath = %s, want %s", storage.RWLayerPath, upperDir)
	}
}

func TestContainerRuntimeFallsBackToLiveRootFSPath(t *testing.T) {
	h, upperDir, _ := newTestDockerContainerHandle(t)

	profile, err := h.Runtime(context.Background())
	if err != nil {
		t.Fatalf("Runtime() error: %v", err)
	}
	if profile.RootFSPath != upperDir {
		t.Fatalf("RootFSPath = %s, want %s", profile.RootFSPath, upperDir)
	}
	if profile.OCI == nil || profile.OCI.RuntimeName != "runc" {
		t.Fatalf("RuntimeName = %v, want runc", profile.OCI)
	}
}

func TestContainerRWLayerStatsFallsBackToLiveMountPath(t *testing.T) {
	h, upperDir, _ := newTestDockerContainerHandle(t)
	content := []byte("hello rw layer\n")
	if err := os.WriteFile(filepath.Join(upperDir, "file.txt"), content, 0o644); err != nil {
		t.Fatalf("failed to seed writable layer: %v", err)
	}

	stats, err := h.RWLayerStats(context.Background())
	if err != nil {
		t.Fatalf("RWLayerStats() error: %v", err)
	}
	if stats.RWLayerUsage < int64(len(content)) {
		t.Fatalf("RWLayerUsage = %d, want at least %d", stats.RWLayerUsage, len(content))
	}
	if stats.RWLayerInodes == 0 {
		t.Fatal("RWLayerInodes = 0, want > 0")
	}
}

func newTestDockerContainerHandle(t *testing.T) (*containerHandle, string, string) {
	t.Helper()

	tmp := t.TempDir()
	upperDir := filepath.Join(tmp, "snapshots", "1102", "fs")
	workDir := filepath.Join(tmp, "snapshots", "1102", "work")
	lowerTop := filepath.Join(tmp, "snapshots", "1101", "fs")
	baseLayer := filepath.Join(tmp, "snapshots", "887", "fs")
	for _, path := range []string{upperDir, workDir, lowerTop, baseLayer} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("failed to create %s: %v", path, err)
		}
	}

	procRoot := filepath.Join(tmp, "proc")
	pidDir := filepath.Join(procRoot, "123")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc dir: %v", err)
	}
	mountinfo := "1746 1246 0:259 / / rw,relatime - overlay overlay rw,lowerdir=" + lowerTop + ":" + baseLayer + ",upperdir=" + upperDir + ",workdir=" + workDir + "\n"
	if err := os.WriteFile(filepath.Join(pidDir, "mountinfo"), []byte(mountinfo), 0o644); err != nil {
		t.Fatalf("failed to write mountinfo: %v", err)
	}

	rt := &Runtime{
		config:      &pkgruntime.Config{},
		mountReader: sysinfo.NewMountReaderWithRoot(procRoot),
	}
	inspect := &dockertypes.ContainerJSON{
		ContainerJSONBase: &dockertypes.ContainerJSONBase{
			ID:     "container-id",
			Image:  "sha256:test-image",
			Driver: "overlayfs",
			State: &dockertypes.ContainerState{
				Pid:     123,
				Status:  "running",
				Running: true,
			},
			HostConfig: &containertypes.HostConfig{Runtime: "runc"},
			GraphDriver: dockertypes.GraphDriverData{
				Data: map[string]string{},
			},
		},
		Config: &containertypes.Config{
			Image: "alpine:3.22",
			Env:   []string{"PATH=/usr/bin"},
		},
	}

	return rt.newContainerHandleFromInspect(inspect), upperDir, baseLayer
}