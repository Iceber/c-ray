package sysinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewMountReader(t *testing.T) {
	reader := NewMountReader()
	if reader == nil {
		t.Fatal("MountReader is nil")
	}
	if reader.procRoot != "/proc" {
		t.Fatalf("expected procRoot=/proc, got %s", reader.procRoot)
	}
}

func TestNewMountReaderWithRoot(t *testing.T) {
	reader := NewMountReaderWithRoot("/tmp/proc")
	if reader == nil {
		t.Fatal("MountReader is nil")
	}
	if reader.procRoot != "/tmp/proc" {
		t.Fatalf("expected procRoot=/tmp/proc, got %s", reader.procRoot)
	}
}

func TestReadMounts(t *testing.T) {
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping test - /proc not available (not Linux)")
	}

	reader := NewMountReader()

	// Read mounts for current process
	pid := os.Getpid()
	mounts, err := reader.ReadMounts(pid)
	if err != nil {
		t.Fatalf("ReadMounts(%d) error: %v", pid, err)
	}

	if len(mounts) == 0 {
		t.Error("Expected at least one mount")
	}

	t.Logf("Found %d mounts", len(mounts))

	// Check for root mount
	rootMount := reader.FindRootMount(mounts)
	if rootMount == nil {
		t.Error("Expected to find root mount")
	} else {
		t.Logf("Root mount: Type=%s, Source=%s", rootMount.Type, rootMount.Source)
	}
}

func TestFilterMountsByType(t *testing.T) {
	if _, err := os.Stat("/proc"); os.IsNotExist(err) {
		t.Skip("Skipping test - /proc not available (not Linux)")
	}

	reader := NewMountReader()

	pid := os.Getpid()
	mounts, err := reader.ReadMounts(pid)
	if err != nil {
		t.Skipf("Cannot read mounts: %v", err)
	}

	// Filter by tmpfs
	tmpfsMounts := reader.FilterMountsByType(mounts, "tmpfs")
	t.Logf("Found %d tmpfs mounts", len(tmpfsMounts))

	for _, mount := range tmpfsMounts {
		if mount.Type != "tmpfs" {
			t.Errorf("Expected type tmpfs, got %s", mount.Type)
		}
	}
}

func TestParseOverlayFS(t *testing.T) {
	reader := NewMountReader()

	// Create a mock overlay mount
	mount := &Mount{
		Type: "overlay",
		Options: []string{
			"rw",
			"lowerdir=/lower1:/lower2",
			"upperdir=/upper",
			"workdir=/work",
		},
	}

	lowerdir, upperdir, workdir := reader.ParseOverlayFS(mount)

	if lowerdir != "/lower1:/lower2" {
		t.Errorf("Expected lowerdir=/lower1:/lower2, got %s", lowerdir)
	}

	if upperdir != "/upper" {
		t.Errorf("Expected upperdir=/upper, got %s", upperdir)
	}

	if workdir != "/work" {
		t.Errorf("Expected workdir=/work, got %s", workdir)
	}
}

func TestReadMountsIncludesOverlaySuperOptions(t *testing.T) {
	procRoot := t.TempDir()
	pidDir := filepath.Join(procRoot, "123")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}
	content := "1746 1246 0:259 / / rw,relatime - overlay overlay rw,lowerdir=/snapshots/1101/fs:/snapshots/887/fs,upperdir=/snapshots/1102/fs,workdir=/snapshots/1102/work\n"
	if err := os.WriteFile(filepath.Join(pidDir, "mountinfo"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write mountinfo: %v", err)
	}

	reader := NewMountReaderWithRoot(procRoot)
	mounts, err := reader.ReadMounts(123)
	if err != nil {
		t.Fatalf("ReadMounts() error: %v", err)
	}
	if len(mounts) != 1 {
		t.Fatalf("ReadMounts() len = %d, want 1", len(mounts))
	}

	rootMount := reader.FindRootMount(mounts)
	if rootMount == nil {
		t.Fatal("expected root mount")
	}
	lowerdir, upperdir, workdir := reader.ParseOverlayFS(rootMount)
	if lowerdir != "/snapshots/1101/fs:/snapshots/887/fs" {
		t.Fatalf("lowerdir = %s", lowerdir)
	}
	if upperdir != "/snapshots/1102/fs" {
		t.Fatalf("upperdir = %s", upperdir)
	}
	if workdir != "/snapshots/1102/work" {
		t.Fatalf("workdir = %s", workdir)
	}
}

func TestGetOverlayLayers(t *testing.T) {
	reader := NewMountReader()

	mount := &Mount{
		Type: "overlay",
		Options: []string{
			"lowerdir=/layer1:/layer2:/layer3",
		},
	}

	layers := reader.GetOverlayLayers(mount)

	if len(layers) != 3 {
		t.Errorf("Expected 3 layers, got %d", len(layers))
	}

	expected := []string{"/layer1", "/layer2", "/layer3"}
	for i, layer := range layers {
		if layer != expected[i] {
			t.Errorf("Layer %d: expected %s, got %s", i, expected[i], layer)
		}
	}
}
