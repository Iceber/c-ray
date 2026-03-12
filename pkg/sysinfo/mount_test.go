package sysinfo

import (
	"os"
	"testing"

	"github.com/icebergu/c-ray/pkg/models"
)

func TestNewMountReader(t *testing.T) {
	reader := NewMountReader()
	if reader == nil {
		t.Fatal("MountReader is nil")
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
	mount := &models.Mount{
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

func TestGetOverlayLayers(t *testing.T) {
	reader := NewMountReader()

	mount := &models.Mount{
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
