package docker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	dockertypes "github.com/docker/docker/api/types"
	containertypes "github.com/docker/docker/api/types/container"
	mounttypes "github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/system"
	pkgruntime "github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/sysinfo"
)

func TestContainerConfigFallsBackToLiveMountPaths(t *testing.T) {
	h, upperDir, baseLayer := newTestDockerContainerHandle(t)

	cfg, err := h.Config(context.Background())
	if err != nil {
		t.Fatalf("Config() error: %v", err)
	}
	if cfg.Backend == nil {
		t.Fatal("Backend = nil, want non-nil")
	}
	if cfg.Backend.Kind != pkgruntime.LayerBackendDockerGraphDriver {
		t.Fatalf("Backend.Kind = %s, want %s", cfg.Backend.Kind, pkgruntime.LayerBackendDockerGraphDriver)
	}
	if cfg.Backend.Name != "overlayfs" {
		t.Fatalf("Backend.Name = %s, want overlayfs", cfg.Backend.Name)
	}
	if cfg.SnapshotKey != "1102" {
		t.Fatalf("SnapshotKey = %s, want 1102", cfg.SnapshotKey)
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
	if storage.Backend == nil {
		t.Fatal("Backend = nil, want non-nil")
	}
	if storage.Backend.Kind != pkgruntime.LayerBackendDockerGraphDriver {
		t.Fatalf("Backend.Kind = %s, want %s", storage.Backend.Kind, pkgruntime.LayerBackendDockerGraphDriver)
	}
	if storage.Backend.Name != "overlayfs" {
		t.Fatalf("Backend.Name = %s, want overlayfs", storage.Backend.Name)
	}
	if storage.Docker == nil {
		t.Fatal("Docker storage details = nil, want non-nil")
	}
	if storage.Docker.GraphDriver != "overlayfs" {
		t.Fatalf("Docker.GraphDriver = %s, want overlayfs", storage.Docker.GraphDriver)
	}
	if storage.Docker.RWLayerID != "1102" {
		t.Fatalf("Docker.RWLayerID = %s, want 1102", storage.Docker.RWLayerID)
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

func TestContainerMountsIncludesLiveOnlyNvidiaRuntimeMounts(t *testing.T) {
	h, _, _ := newTestDockerContainerHandle(t)
	h.inspect.Mounts = []dockertypes.MountPoint{
		{
			Type:        mounttypes.TypeBind,
			Source:      "/host/data",
			Destination: "/data",
			RW:          true,
		},
	}

	mounts, err := h.Mounts(context.Background())
	if err != nil {
		t.Fatalf("Mounts() error: %v", err)
	}

	var dataMount, nvidiaMount *pkgruntime.Mount
	for _, m := range mounts {
		switch m.Destination {
		case "/data":
			dataMount = m
		case "/usr/bin/nvidia-smi":
			nvidiaMount = m
		}
	}

	if dataMount == nil {
		t.Fatal("Docker inspect mount /data not found")
	}
	if dataMount.Origin != pkgruntime.MountOriginUser {
		t.Fatalf("/data Origin = %s, want %s", dataMount.Origin, pkgruntime.MountOriginUser)
	}
	if nvidiaMount == nil {
		t.Fatal("live NVIDIA mount /usr/bin/nvidia-smi not found")
	}
	if nvidiaMount.Origin != pkgruntime.MountOriginLiveExtra {
		t.Fatalf("nvidia mount Origin = %s, want %s", nvidiaMount.Origin, pkgruntime.MountOriginLiveExtra)
	}
	if nvidiaMount.State != pkgruntime.MountStateLiveOnly {
		t.Fatalf("nvidia mount State = %s, want %s", nvidiaMount.State, pkgruntime.MountStateLiveOnly)
	}
	if nvidiaMount.LiveSource != "/usr/bin/nvidia-smi" {
		t.Fatalf("nvidia mount LiveSource = %s, want /usr/bin/nvidia-smi", nvidiaMount.LiveSource)
	}
}

// TestContainerMountsClassifiesRuntimeDefaultsFromBundleSpec verifies that
// mounts injected by runc / dockerd (procfs, sysfs, /etc/resolv.conf, ...) and
// declared in the bundle's config.json — but absent from `docker inspect`'s
// Mounts list — are surfaced as MountOriginRuntimeDefault rather than
// MountOriginLiveExtra.
func TestContainerMountsClassifiesRuntimeDefaultsFromBundleSpec(t *testing.T) {
	h, _, _ := newTestDockerContainerHandle(t)

	// Set up a fake containerd state dir so dockerBundleDir() can resolve.
	stateDir := filepath.Join(t.TempDir(), "containerd")
	bundleDir := filepath.Join(stateDir, "io.containerd.runtime.v2.task", "moby", h.inspect.ID)
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("mkdir bundle: %v", err)
	}
	specJSON := `{
        "mounts": [
            {"destination": "/proc", "type": "proc", "source": "proc"},
            {"destination": "/sys", "type": "sysfs", "source": "sysfs", "options": ["ro"]},
            {"destination": "/etc/resolv.conf", "type": "bind", "source": "/var/lib/docker/containers/x/resolv.conf", "options": ["bind"]},
            {"destination": "/data", "type": "bind", "source": "/host/data", "options": ["rbind"]}
        ]
    }`
	if err := os.WriteFile(filepath.Join(bundleDir, "config.json"), []byte(specJSON), 0o644); err != nil {
		t.Fatalf("write config.json: %v", err)
	}
	h.rt.daemonInfo = &daemonInfo{
		ContainerdAddr: filepath.Join(stateDir, "containerd.sock"),
		ContainerdNS:   map[string]string{"containers": "moby"},
	}
	h.inspect.Mounts = []dockertypes.MountPoint{
		{
			Type:        mounttypes.TypeBind,
			Source:      "/host/data",
			Destination: "/data",
			RW:          true,
		},
	}

	mounts, err := h.Mounts(context.Background())
	if err != nil {
		t.Fatalf("Mounts() error: %v", err)
	}

	got := make(map[string]*pkgruntime.Mount, len(mounts))
	for _, m := range mounts {
		got[m.Destination] = m
	}

	if dm := got["/data"]; dm == nil {
		t.Fatal("/data mount missing")
	} else if dm.Origin != pkgruntime.MountOriginUser {
		t.Fatalf("/data Origin = %s, want %s", dm.Origin, pkgruntime.MountOriginUser)
	}
	for _, dest := range []string{"/proc", "/sys", "/etc/resolv.conf"} {
		m := got[dest]
		if m == nil {
			t.Fatalf("expected runtime-default mount %s missing", dest)
		}
		if m.Origin != pkgruntime.MountOriginRuntimeDefault {
			t.Fatalf("%s Origin = %s, want %s", dest, m.Origin, pkgruntime.MountOriginRuntimeDefault)
		}
	}
}

func TestReadOnlyLayerQueryUsesSnapshotContextInDockerContainerdMode(t *testing.T) {
	h, _, _ := newTestDockerContainerHandle(t)
	h.rt.imageStoreMode = ImageStoreModeContainerd

	query := h.readOnlyLayerQuery(&pkgruntime.ContainerStorage{
		Backend: &pkgruntime.LayerBackend{
			Kind: pkgruntime.LayerBackendContainerdSnapshotter,
			Name: "overlayfs",
		},
		Docker: &pkgruntime.DockerContainerStorage{
			Snapshotter:   "overlayfs",
			RWSnapshotKey: "1102",
		},
	})

	if query.Snapshotter != "overlayfs" {
		t.Fatalf("Snapshotter = %s, want overlayfs", query.Snapshotter)
	}
	if query.RWSnapshotKey != "1102" {
		t.Fatalf("RWSnapshotKey = %s, want 1102", query.RWSnapshotKey)
	}
}

func TestReadOnlyLayerQueryIsEmptyOutsideDockerContainerdMode(t *testing.T) {
	h, _, _ := newTestDockerContainerHandle(t)

	query := h.readOnlyLayerQuery(&pkgruntime.ContainerStorage{
		Backend: &pkgruntime.LayerBackend{
			Kind: pkgruntime.LayerBackendContainerdSnapshotter,
			Name: "overlayfs",
		},
		Docker: &pkgruntime.DockerContainerStorage{
			Snapshotter:   "overlayfs",
			RWSnapshotKey: "1102",
		},
	})

	if query.Snapshotter != "" || query.RWSnapshotKey != "" {
		t.Fatalf("query = %+v, want empty query outside docker-containerd mode", query)
	}
}

func TestContainerConfigDerivesSnapshotKeyFromGraphDriverUpperDir(t *testing.T) {
	h, upperDir, baseLayer := newTestDockerContainerHandleWithoutProc(t)

	cfg, err := h.Config(context.Background())
	if err != nil {
		t.Fatalf("Config() error: %v", err)
	}
	if cfg.SnapshotKey != "1102" {
		t.Fatalf("SnapshotKey = %s, want 1102", cfg.SnapshotKey)
	}
	if cfg.WritableLayerPath != upperDir {
		t.Fatalf("WritableLayerPath = %s, want %s", cfg.WritableLayerPath, upperDir)
	}
	if cfg.ReadOnlyLayerPath != baseLayer {
		t.Fatalf("ReadOnlyLayerPath = %s, want %s", cfg.ReadOnlyLayerPath, baseLayer)
	}
}

func TestContainerStorageDerivesSnapshotKeyFromGraphDriverUpperDir(t *testing.T) {
	h, upperDir, _ := newTestDockerContainerHandleWithoutProc(t)
	h.rt.imageStoreMode = ImageStoreModeContainerd

	storage, err := h.Storage(context.Background())
	if err != nil {
		t.Fatalf("Storage() error: %v", err)
	}
	if storage.Backend == nil {
		t.Fatal("Backend = nil, want non-nil")
	}
	if storage.Backend.Kind != pkgruntime.LayerBackendContainerdSnapshotter {
		t.Fatalf("Backend.Kind = %s, want %s", storage.Backend.Kind, pkgruntime.LayerBackendContainerdSnapshotter)
	}
	if storage.Backend.Name != "overlayfs" {
		t.Fatalf("Backend.Name = %s, want overlayfs", storage.Backend.Name)
	}
	if storage.Docker == nil {
		t.Fatal("Docker storage details = nil, want non-nil")
	}
	if storage.Docker.Snapshotter != "overlayfs" {
		t.Fatalf("Docker.Snapshotter = %s, want overlayfs", storage.Docker.Snapshotter)
	}
	if storage.Docker.RWSnapshotKey != "1102" {
		t.Fatalf("Docker.RWSnapshotKey = %s, want 1102", storage.Docker.RWSnapshotKey)
	}
	if storage.RWLayerPath != upperDir {
		t.Fatalf("RWLayerPath = %s, want %s", storage.RWLayerPath, upperDir)
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
	mountinfo := "" +
		"1746 1246 0:259 / / rw,relatime - overlay overlay rw,lowerdir=" + lowerTop + ":" + baseLayer + ",upperdir=" + upperDir + ",workdir=" + workDir + "\n" +
		"1750 1746 8:1 /usr/bin/nvidia-smi /usr/bin/nvidia-smi ro,relatime - ext4 /usr/bin/nvidia-smi ro\n"
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

func newTestDockerContainerHandleWithoutProc(t *testing.T) (*containerHandle, string, string) {
	t.Helper()

	tmp := t.TempDir()
	upperDir := filepath.Join(tmp, "snapshots", "1102", "fs")
	lowerTop := filepath.Join(tmp, "snapshots", "1101", "fs")
	baseLayer := filepath.Join(tmp, "snapshots", "887", "fs")
	for _, path := range []string{upperDir, lowerTop, baseLayer} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("failed to create %s: %v", path, err)
		}
	}

	rt := &Runtime{config: &pkgruntime.Config{}}
	inspect := &dockertypes.ContainerJSON{
		ContainerJSONBase: &dockertypes.ContainerJSONBase{
			ID:     "container-id",
			Image:  "sha256:test-image",
			Driver: "overlayfs",
			State: &dockertypes.ContainerState{
				Pid:     0,
				Status:  "running",
				Running: true,
			},
			HostConfig: &containertypes.HostConfig{Runtime: "runc"},
			GraphDriver: dockertypes.GraphDriverData{
				Data: map[string]string{
					"UpperDir": upperDir,
					"LowerDir": lowerTop + ":" + baseLayer,
				},
			},
		},
		Config: &containertypes.Config{
			Image: "alpine:3.22",
			Env:   []string{"PATH=/usr/bin"},
		},
	}

	return rt.newContainerHandleFromInspect(inspect), upperDir, baseLayer
}

func TestDockerExecRootInferredFromBundledContainerdSocket(t *testing.T) {
	di := &daemonInfo{ContainerdAddr: "/var/run/docker/containerd/containerd.sock"}
	if got, want := dockerExecRoot(di), "/var/run/docker"; got != want {
		t.Fatalf("dockerExecRoot = %q, want %q", got, want)
	}
}

func TestDockerExecRootFallsBackToDefaultForExternalContainerd(t *testing.T) {
	di := &daemonInfo{ContainerdAddr: "/run/containerd/containerd.sock"}
	if got, want := dockerExecRoot(di), "/var/run/docker"; got != want {
		t.Fatalf("dockerExecRoot = %q, want %q", got, want)
	}
}

func TestDockerRuntimeStateDirUsesExecRootRuntimeNameLayout(t *testing.T) {
	di := &daemonInfo{ContainerdAddr: "/var/run/docker/containerd/containerd.sock"}
	const id = "67bf778b988444fa78d4cf02e120bf97359bd72ce797e660122d97da3fdb948f"

	got := dockerRuntimeStateDir(di, "runc", "moby", id)
	want := "/var/run/docker/runtime-runc/moby/" + id
	if got != want {
		t.Fatalf("dockerRuntimeStateDir = %q, want %q", got, want)
	}
}

func TestDockerRuntimeStateDirHonoursRuntimeArgsRoot(t *testing.T) {
	di := &daemonInfo{
		ContainerdAddr: "/var/run/docker/containerd/containerd.sock",
		Runtimes: map[string]system.RuntimeWithStatus{
			"crun": {Runtime: system.Runtime{Args: []string{"--root", "/run/crun-state"}}},
		},
	}
	got := dockerRuntimeStateDir(di, "crun", "moby", "abc")
	want := "/run/crun-state/moby/abc"
	if got != want {
		t.Fatalf("dockerRuntimeStateDir = %q, want %q", got, want)
	}
}

func TestDockerRuntimeStateDirHonoursRuntimeArgsRootEqualsForm(t *testing.T) {
	di := &daemonInfo{
		Runtimes: map[string]system.RuntimeWithStatus{
			"crun": {Runtime: system.Runtime{Args: []string{"--root=/data/crun"}}},
		},
	}
	got := dockerRuntimeStateDir(di, "crun", "moby", "abc")
	want := "/data/crun/moby/abc"
	if got != want {
		t.Fatalf("dockerRuntimeStateDir = %q, want %q", got, want)
	}
}
