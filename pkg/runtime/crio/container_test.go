package crio

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	pkgruntime "github.com/icebergu/c-ray/pkg/runtime"
	runtimespec "github.com/opencontainers/runtime-spec/specs-go"
)

func TestContainerConfigLoadsNamespacesFromOCISpec(t *testing.T) {
	runRoot := t.TempDir()
	storageRoot := t.TempDir()
	rt := New(&pkgruntime.Config{
		SocketPath:     filepath.Join(runRoot, "missing.sock"),
		StorageRoot:    storageRoot,
		StorageRunRoot: runRoot,
	})
	rt.storeOnce.Do(func() {
		rt.storeErr = errors.New("test: disable storage")
	})

	h := &containerHandle{
		rt:      rt,
		id:      "test-container",
		spoofed: true,
	}

	spec := &runtimespec.Spec{
		Process: &runtimespec.Process{
			Env: []string{"FOO=bar"},
		},
		Linux: &runtimespec.Linux{
			CgroupsPath: "kubepods.slice/pod123/container123",
			Namespaces: []runtimespec.LinuxNamespace{
				{Type: runtimespec.MountNamespace},
				{Type: runtimespec.NetworkNamespace, Path: "/var/run/netns/podns"},
				{Type: runtimespec.PIDNamespace, Path: "/proc/123/ns/pid"},
			},
		},
	}

	if err := writeTestOCISpec(runRoot, h.id, spec); err != nil {
		t.Fatalf("write test OCI spec: %v", err)
	}

	cfg, err := h.Config(context.Background())
	if err != nil {
		t.Fatalf("Config() error: %v", err)
	}
	if cfg.Namespaces == nil {
		t.Fatal("Namespaces = nil, want non-nil")
	}
	if got := cfg.Namespaces["mount"]; got != "" {
		t.Fatalf("Namespaces[mount] = %q, want empty string for private namespace", got)
	}
	if got := cfg.Namespaces["network"]; got != "/var/run/netns/podns" {
		t.Fatalf("Namespaces[network] = %q, want /var/run/netns/podns", got)
	}
	if got := cfg.Namespaces["pid"]; got != "/proc/123/ns/pid" {
		t.Fatalf("Namespaces[pid] = %q, want /proc/123/ns/pid", got)
	}
	if cfg.CGroupPath != "kubepods.slice/pod123/container123" {
		t.Fatalf("CGroupPath = %q, want kubepods.slice/pod123/container123", cfg.CGroupPath)
	}
	if len(cfg.Environment) != 1 || cfg.Environment[0].Key != "FOO" || cfg.Environment[0].Value != "bar" {
		t.Fatalf("Environment = %+v, want FOO=bar", cfg.Environment)
	}
}

func writeTestOCISpec(runRoot, containerID string, spec *runtimespec.Spec) error {
	bundleDir := crioContainerBundleDir(runRoot, containerID)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(spec)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(bundleDir, "config.json"), data, 0o644)
}
