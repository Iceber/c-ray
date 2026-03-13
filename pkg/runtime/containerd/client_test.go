package containerd

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	runcoptions "github.com/containerd/containerd/api/types/runc/options"
	"github.com/containerd/containerd/v2/core/containers"
	"github.com/containerd/typeurl/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	runtimecri "github.com/icebergu/c-ray/pkg/runtime/cri"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	ocispec "github.com/opencontainers/runtime-spec/specs-go"
)

type stubCRIMetadataClient struct {
	mounts     *runtimecri.ContainerMounts
	podNetwork *runtimecri.PodSandboxNetwork
	container  *runtimecri.ContainerStatusInfo
	err        error
}

func (s stubCRIMetadataClient) InspectContainerMounts(ctx context.Context, containerID string) (*runtimecri.ContainerMounts, error) {
	return s.mounts, s.err
}

func (s stubCRIMetadataClient) InspectPodSandboxNetwork(ctx context.Context, sandboxID string) (*runtimecri.PodSandboxNetwork, error) {
	return s.podNetwork, s.err
}

func (s stubCRIMetadataClient) InspectContainerStatus(ctx context.Context, containerID string) (*runtimecri.ContainerStatusInfo, error) {
	return s.container, s.err
}

func TestNewContainerdRuntime(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	if rt == nil {
		t.Fatal("NewContainerdRuntime returned nil")
	}

	if rt.config != config {
		t.Error("Config not set correctly")
	}
}

func TestConvertStatus(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"created", "created"},
		{"running", "running"},
		{"paused", "paused"},
		{"stopped", "stopped"},
		{"unknown", "unknown"},
		{"invalid", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := convertStatus(tt.input)
			if string(result) != tt.expected {
				t.Errorf("convertStatus(%s) = %s, want %s", tt.input, result, tt.expected)
			}
		})
	}
}

func TestConnect_NotConnected(t *testing.T) {
	t.Skip("Skipping test - requires containerd to be running")

	config := &runtime.Config{
		SocketPath: "/nonexistent/socket.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	ctx := context.Background()

	// This should fail because the socket doesn't exist
	err := rt.Connect(ctx)
	if err == nil {
		t.Error("Expected error when connecting to nonexistent socket")
		rt.Close()
	} else {
		t.Logf("Got expected error: %v", err)
	}
}

func TestClose_NilClient(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)

	// Should not panic when client is nil
	err := rt.Close()
	if err != nil {
		t.Errorf("Close() returned error: %v", err)
	}
}

func TestGetShimPID(t *testing.T) {
	procRoot := t.TempDir()
	writeProcStat(t, procRoot, 123, "runc:[2:INIT]", "S", 456)

	rt := &ContainerdRuntime{
		procReader: sysinfo.NewProcReaderWithRoot(procRoot),
	}

	shimPID := rt.getShimPID(123)
	if shimPID != 456 {
		t.Fatalf("getShimPID() = %d, want 456", shimPID)
	}
}

func TestGetShimPIDReadError(t *testing.T) {
	rt := &ContainerdRuntime{
		procReader: sysinfo.NewProcReaderWithRoot(t.TempDir()),
	}

	shimPID := rt.getShimPID(123)
	if shimPID != 0 {
		t.Fatalf("getShimPID() = %d, want 0", shimPID)
	}
}

func TestGetShimProcessInfo(t *testing.T) {
	procRoot := t.TempDir()
	writeProcStat(t, procRoot, 123, "task", "S", 456)
	writeProcStat(t, procRoot, 456, "containerd-shim-runc-v2", "S", 1)
	writeProcCmdline(t, procRoot, 456, "/usr/bin/containerd-shim-runc-v2", "-namespace", "k8s.io")
	writeProcLink(t, procRoot, 456, "exe", "/usr/bin/containerd-shim-runc-v2")
	writeProcLink(t, procRoot, 456, "cwd", "/run/containerd/io.containerd.runtime.v2.task/k8s.io/test")

	rt := &ContainerdRuntime{procReader: sysinfo.NewProcReaderWithRoot(procRoot)}
	shim := rt.getShimProcessInfo(123)
	if shim == nil {
		t.Fatal("getShimProcessInfo() = nil")
	}
	if shim.pid != 456 {
		t.Fatalf("getShimProcessInfo().pid = %d, want 456", shim.pid)
	}
	if shim.binaryPath != "/usr/bin/containerd-shim-runc-v2" {
		t.Fatalf("getShimProcessInfo().binaryPath = %s", shim.binaryPath)
	}
}

func TestResolveShimSocketAddressFromBundle(t *testing.T) {
	bundleDir := filepath.Join(t.TempDir(), "bundle")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("failed to create bundle dir: %v", err)
	}
	bootstrap := `{"version":2,"address":"unix:///run/containerd/s/abc","protocol":"ttrpc"}`
	if err := os.WriteFile(filepath.Join(bundleDir, "bootstrap.json"), []byte(bootstrap), 0o644); err != nil {
		t.Fatalf("failed to write bootstrap.json: %v", err)
	}

	rt := &ContainerdRuntime{config: &runtime.Config{Namespace: "k8s.io"}}
	address, sandboxID, sandboxBundleDir, source := rt.resolveShimSocketAddress(bundleDir, "container-id", "")
	if address != "ttrpc+unix:///run/containerd/s/abc" {
		t.Fatalf("resolveShimSocketAddress() address = %s", address)
	}
	if sandboxID != "" || sandboxBundleDir != "" {
		t.Fatalf("resolveShimSocketAddress() unexpected sandbox fallback")
	}
	if source != "bundle" {
		t.Fatalf("resolveShimSocketAddress() source = %s", source)
	}
}

func TestResolveShimSocketAddressFromSandboxBundle(t *testing.T) {
	parentDir := t.TempDir()
	bundleDir := filepath.Join(parentDir, "container-id")
	sandboxBundleDir := filepath.Join(parentDir, "sandbox-id")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("failed to create bundle dir: %v", err)
	}
	if err := os.MkdirAll(sandboxBundleDir, 0o755); err != nil {
		t.Fatalf("failed to create sandbox bundle dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bundleDir, "sandbox"), []byte("sandbox-id\n"), 0o644); err != nil {
		t.Fatalf("failed to write sandbox file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sandboxBundleDir, "address"), []byte("unix:///run/containerd/s/fallback\n"), 0o644); err != nil {
		t.Fatalf("failed to write address file: %v", err)
	}

	rt := &ContainerdRuntime{config: &runtime.Config{Namespace: "k8s.io"}}
	address, sandboxID, resolvedSandboxBundleDir, source := rt.resolveShimSocketAddress(bundleDir, "container-id", "")
	if address != "unix:///run/containerd/s/fallback" {
		t.Fatalf("resolveShimSocketAddress() address = %s", address)
	}
	if sandboxID != "sandbox-id" {
		t.Fatalf("resolveShimSocketAddress() sandboxID = %s", sandboxID)
	}
	if resolvedSandboxBundleDir != sandboxBundleDir {
		t.Fatalf("resolveShimSocketAddress() sandboxBundleDir = %s", resolvedSandboxBundleDir)
	}
	if source != "sandbox-bundle" {
		t.Fatalf("resolveShimSocketAddress() source = %s", source)
	}
}

func TestResolveShimSocketAddressUsesSandboxIDHint(t *testing.T) {
	parentDir := t.TempDir()
	bundleDir := filepath.Join(parentDir, "container-id")
	sandboxBundleDir := filepath.Join(parentDir, "sandbox-id")
	if err := os.MkdirAll(bundleDir, 0o755); err != nil {
		t.Fatalf("failed to create bundle dir: %v", err)
	}
	if err := os.MkdirAll(sandboxBundleDir, 0o755); err != nil {
		t.Fatalf("failed to create sandbox bundle dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(sandboxBundleDir, "address"), []byte("unix:///run/containerd/s/from-hint\n"), 0o644); err != nil {
		t.Fatalf("failed to write address file: %v", err)
	}

	rt := &ContainerdRuntime{config: &runtime.Config{Namespace: "k8s.io"}}
	address, sandboxID, resolvedSandboxBundleDir, source := rt.resolveShimSocketAddress(bundleDir, "container-id", "sandbox-id")
	if address != "unix:///run/containerd/s/from-hint" {
		t.Fatalf("resolveShimSocketAddress() address = %s", address)
	}
	if sandboxID != "sandbox-id" {
		t.Fatalf("resolveShimSocketAddress() sandboxID = %s", sandboxID)
	}
	if resolvedSandboxBundleDir != sandboxBundleDir {
		t.Fatalf("resolveShimSocketAddress() sandboxBundleDir = %s", resolvedSandboxBundleDir)
	}
	if source != "sandbox-bundle" {
		t.Fatalf("resolveShimSocketAddress() source = %s", source)
	}
}

func TestResolveOCIBundleDirUsesTaskStatePath(t *testing.T) {
	rt := &ContainerdRuntime{}
	resolved, source := rt.resolveOCIBundleDir("k8s.io", "container-id")
	expected := filepath.Join(runtimeV2StateBase, "k8s.io", "container-id")
	if resolved != expected {
		t.Fatalf("resolveOCIBundleDir() = %s, want %s", resolved, expected)
	}
	if source != "convention" {
		t.Fatalf("resolveOCIBundleDir() source = %s", source)
	}
}

func TestResolveOCIStateDirUsesRuntimeOptionsRoot(t *testing.T) {
	optionsAny, err := typeurl.MarshalAny(&runcoptions.Options{Root: "/run/custom-runc"})
	if err != nil {
		t.Fatalf("MarshalAny() error = %v", err)
	}

	rt := &ContainerdRuntime{}
	info := containers.Container{
		ID: "container-id",
		Runtime: containers.RuntimeInfo{
			Name:    "io.containerd.runc.v2",
			Options: optionsAny,
		},
	}
	resolved, source := rt.resolveOCIStateDir(info, "k8s.io")
	expected := filepath.Join("/run/custom-runc", "k8s.io", "container-id")
	if resolved != expected {
		t.Fatalf("resolveOCIStateDir() = %s, want %s", resolved, expected)
	}
	if source != "runtime-options" {
		t.Fatalf("resolveOCIStateDir() source = %s", source)
	}
}

func TestResolveOCIStateDirUsesDefaultRuncRoot(t *testing.T) {
	rt := &ContainerdRuntime{}
	info := containers.Container{
		ID: "container-id",
		Runtime: containers.RuntimeInfo{
			Name: "io.containerd.runc.v2",
		},
	}
	resolved, source := rt.resolveOCIStateDir(info, "k8s.io")
	expected := filepath.Join(defaultRuncRoot, "k8s.io", "container-id")
	if resolved != expected {
		t.Fatalf("resolveOCIStateDir() = %s, want %s", resolved, expected)
	}
	if source != "runtime-default" {
		t.Fatalf("resolveOCIStateDir() source = %s", source)
	}
}

func TestResolveRuntimeBinaryPathPrefersRuntimeOptionsBinaryName(t *testing.T) {
	optionsAny, err := typeurl.MarshalAny(&runcoptions.Options{BinaryName: "/usr/bin/runc"})
	if err != nil {
		t.Fatalf("MarshalAny() error = %v", err)
	}

	bundleDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundleDir, "shim-binary-path"), []byte("/usr/bin/containerd-shim-runc-v2\n"), 0o644); err != nil {
		t.Fatalf("failed to write shim-binary-path: %v", err)
	}

	rt := &ContainerdRuntime{}
	runtimeBinary, source := rt.resolveRuntimeBinaryPath(bundleDir, containers.RuntimeInfo{
		Name:    "io.containerd.runc.v2",
		Options: optionsAny,
	}, nil)
	if runtimeBinary != "/usr/bin/runc" {
		t.Fatalf("resolveRuntimeBinaryPath() = %s", runtimeBinary)
	}
	if source != "runtime-options" {
		t.Fatalf("resolveRuntimeBinaryPath() source = %s", source)
	}
}

func TestBuildContainerFromInfoShortIDFallback(t *testing.T) {
	rt := &ContainerdRuntime{}
	container := rt.buildContainerFromInfo(containers.Container{
		ID:    "short-id",
		Image: "example:v1",
		Labels: map[string]string{
			"io.kubernetes.pod.name":      "pod-a",
			"io.kubernetes.pod.namespace": "default",
		},
	})

	if container.Name != "short-id" {
		t.Fatalf("buildContainerFromInfo().Name = %s, want short-id", container.Name)
	}
	if container.PodName != "pod-a" || container.PodNamespace != "default" {
		t.Fatalf("buildContainerFromInfo() pod labels not preserved: %+v", container)
	}
}

func TestBuildMountsFromSpec(t *testing.T) {
	mounts := buildMountsFromSpec([]ocispec.Mount{{
		Destination: "/data",
		Source:      "/var/lib/data",
		Type:        "bind",
		Options:     []string{"rbind", "ro"},
	}})

	if len(mounts) != 1 {
		t.Fatalf("buildMountsFromSpec() len = %d, want 1", len(mounts))
	}
	if mounts[0].Destination != "/data" || mounts[0].Source != "/var/lib/data" || mounts[0].Type != "bind" {
		t.Fatalf("buildMountsFromSpec() mount = %+v", mounts[0])
	}

	mounts[0].Options[0] = "changed"
	original := buildMountsFromSpec([]ocispec.Mount{{Options: []string{"rbind"}}})
	if original[0].Options[0] != "rbind" {
		t.Fatal("buildMountsFromSpec() should copy mount options")
	}
}

func TestComputeShimSocketAddress(t *testing.T) {
	address := computeShimSocketAddress("k8s.io", "container-id")
	expectedHash := sha256.Sum256([]byte(filepath.Join(runtimeV2StateBase, "k8s.io", "container-id")))
	expected := "unix:///run/containerd/s/" + fmt.Sprintf("%x", expectedHash)
	if address != expected {
		t.Fatalf("computeShimSocketAddress() = %s, want %s", address, expected)
	}
}

func writeProcStat(t *testing.T, procRoot string, pid int, command string, state string, ppid int) {
	t.Helper()

	pidDir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}

	statContent := strconv.Itoa(pid) + " (" + command + ") " + state + " " + strconv.Itoa(ppid) + " 0 0 0 0 0 0 0 0 0 0 10 20 0 0 0 0 0\n"
	statPath := filepath.Join(pidDir, "stat")
	if err := os.WriteFile(statPath, []byte(statContent), 0o644); err != nil {
		t.Fatalf("failed to write stat file: %v", err)
	}
}

func writeProcCmdline(t *testing.T, procRoot string, pid int, args ...string) {
	t.Helper()

	pidDir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}
	content := strings.Join(args, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(pidDir, "cmdline"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write cmdline file: %v", err)
	}
}

func writeProcLink(t *testing.T, procRoot string, pid int, name string, target string) {
	t.Helper()

	pidDir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create proc directory: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(pidDir, name)); err != nil {
		t.Fatalf("failed to create symlink %s: %v", name, err)
	}
}

func TestListContainers_NotConnected(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	ctx := context.Background()

	// Should return error when not connected
	_, err := rt.ListContainers(ctx)
	if err == nil {
		t.Error("Expected error when listing containers without connection")
	}
}

func TestListImages_NotConnected(t *testing.T) {
	config := &runtime.Config{
		SocketPath: "/run/containerd/containerd.sock",
		Namespace:  "default",
		Timeout:    30,
	}

	rt := NewContainerdRuntime(config)
	ctx := context.Background()

	// Should return error when not connected
	_, err := rt.ListImages(ctx)
	if err == nil {
		t.Error("Expected error when listing images without connection")
	}
}

func TestBuildNamespaceMap(t *testing.T) {
	got := buildNamespaceMap(&ocispec.Spec{
		Linux: &ocispec.Linux{Namespaces: []ocispec.LinuxNamespace{{Type: ocispec.NetworkNamespace, Path: "/var/run/netns/pod"}, {Type: ocispec.PIDNamespace, Path: ""}}},
	})
	if got["network"] != "/var/run/netns/pod" {
		t.Fatalf("network namespace = %q", got["network"])
	}
	if _, ok := got["pid"]; !ok {
		t.Fatal("pid namespace missing from map")
	}
}

func TestPopulatePodNetwork(t *testing.T) {
	rt := &ContainerdRuntime{
		criClient: stubCRIMetadataClient{podNetwork: &runtimecri.PodSandboxNetwork{
			SandboxID:       "sandbox-1",
			SandboxState:    "SANDBOX_READY",
			PrimaryIP:       "10.244.0.12",
			AdditionalIPs:   []string{"fd00::12"},
			HostNetwork:     false,
			NamespaceMode:   "POD",
			NetNSPath:       "/var/run/netns/from-cri",
			Hostname:        "pod-a",
			RuntimeHandler:  "runc",
			StatusSource:    "cri-status",
			ConfigSource:    "cri-info",
			NamespaceSource: "cri-info-metadata",
			DNS: &runtimecri.DNSConfig{
				Servers: []string{"10.96.0.10"},
			},
			PortMappings: []*runtimecri.PortMapping{{
				HostIP:        "0.0.0.0",
				HostPort:      30080,
				ContainerPort: 8080,
				Protocol:      "tcp",
			}},
		}},
	}
	detail := &models.ContainerDetail{Namespaces: map[string]string{"network": "/var/run/netns/from-spec"}}
	spec := &ocispec.Spec{Linux: &ocispec.Linux{Namespaces: []ocispec.LinuxNamespace{{Type: ocispec.NetworkNamespace, Path: "/var/run/netns/from-spec"}}}}

	rt.populatePodNetwork(context.Background(), detail, containers.Container{SandboxID: "sandbox-1"}, spec)

	if detail.PodNetwork == nil {
		t.Fatal("PodNetwork = nil")
	}
	if detail.IPAddress != "10.244.0.12" {
		t.Fatalf("IPAddress = %s", detail.IPAddress)
	}
	if detail.PodNetwork.NetNSPath != "/var/run/netns/from-cri" {
		t.Fatalf("NetNSPath = %s", detail.PodNetwork.NetNSPath)
	}
	if detail.PodNetwork.NamespaceSource != "cri-info-metadata" {
		t.Fatalf("NamespaceSource = %s", detail.PodNetwork.NamespaceSource)
	}
	if len(detail.PodNetwork.Warnings) == 0 {
		t.Fatal("Warnings = 0, want spec/cri netns mismatch warning")
	}
	if detail.PodNetwork.DNS == nil || len(detail.PodNetwork.PortMappings) != 1 {
		t.Fatalf("PodNetwork DNS/ports not populated: %+v", detail.PodNetwork)
	}
}

func TestPopulatePodNetworkReadsObservedInterfacesFromProcfs(t *testing.T) {
	procRoot := t.TempDir()
	writeProcNetDev(t, procRoot, 123, "Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n  lo: 100 1 0 0 0 0 0 0 100 1 0 0 0 0 0 0\neth0: 2048 20 1 2 0 0 0 0 4096 40 3 4 0 0 0 0\n")

	rt := &ContainerdRuntime{
		criClient:  stubCRIMetadataClient{},
		procReader: sysinfo.NewProcReaderWithRoot(procRoot),
	}
	detail := &models.ContainerDetail{Container: models.Container{PID: 123}}
	spec := &ocispec.Spec{Linux: &ocispec.Linux{Namespaces: []ocispec.LinuxNamespace{{Type: ocispec.NetworkNamespace, Path: "/var/run/netns/from-spec"}}}}

	rt.populatePodNetwork(context.Background(), detail, containers.Container{SandboxID: "sandbox-1"}, spec)

	if detail.PodNetwork == nil {
		t.Fatal("PodNetwork = nil")
	}
	if len(detail.PodNetwork.ObservedInterfaces) != 1 {
		t.Fatalf("ObservedInterfaces len = %d, want 1", len(detail.PodNetwork.ObservedInterfaces))
	}
	if detail.PodNetwork.ObservedInterfaces[0].Interface != "eth0" {
		t.Fatalf("Observed interface = %+v", detail.PodNetwork.ObservedInterfaces[0])
	}
	if detail.PodNetwork.ObservedInterfaces[0].RxBytes != 2048 || detail.PodNetwork.ObservedInterfaces[0].TxBytes != 4096 {
		t.Fatalf("Observed interface counters = %+v", detail.PodNetwork.ObservedInterfaces[0])
	}
	if detail.PodNetwork.NetNSPath != "/var/run/netns/from-spec" {
		t.Fatalf("NetNSPath = %s", detail.PodNetwork.NetNSPath)
	}
}

func writeProcNetDev(t *testing.T, procRoot string, pid int, content string) {
	t.Helper()
	pidDir := filepath.Join(procRoot, strconv.Itoa(pid), "net")
	if err := os.MkdirAll(pidDir, 0o755); err != nil {
		t.Fatalf("failed to create net directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "dev"), []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write net/dev: %v", err)
	}
}
