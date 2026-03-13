package views

import (
	"strings"
	"testing"

	"github.com/icebergu/c-ray/pkg/models"
	"github.com/rivo/tview"
)

func TestBuildRuntimeOCINodeOmitsSnapshotterAndCGroup(t *testing.T) {
	detail := &models.ContainerDetail{
		Container: models.Container{PID: 101},
		RuntimeProfile: &models.RuntimeProfile{
			OCI: &models.OCIInfo{
				RuntimeName:   "runc",
				RuntimeBinary: "/usr/bin/runc",
				ConfigPath:    "/bundle/config.json",
			},
			CGroup: &models.CGroupInfo{
				RelativePath: "/kubepods/test",
				AbsolutePath: "/sys/fs/cgroup/kubepods/test",
			},
		},
	}

	node := buildRuntimeOCINode(detail)
	text := joinNodeTexts(node)
	if strings.Contains(text, "Snapshotter:") {
		t.Fatalf("OCI node unexpectedly contains snapshotter: %s", text)
	}
	if strings.Contains(text, "CGroup Relative:") || strings.Contains(text, "CGroup Absolute:") {
		t.Fatalf("OCI node unexpectedly contains cgroup info: %s", text)
	}
	if !strings.Contains(text, "Runtime Name: runc") {
		t.Fatalf("OCI node missing runtime metadata: %s", text)
	}
}

func TestBuildRuntimeShimNodeOmitsBundleDir(t *testing.T) {
	detail := &models.ContainerDetail{
		Container: models.Container{PID: 101},
		ShimPID:   202,
		RuntimeProfile: &models.RuntimeProfile{
			Shim: &models.ShimInfo{
				BinaryPath:       "/usr/bin/containerd-shim-runc-v2",
				SocketAddress:    "/run/containerd/s/test.sock",
				Cmdline:          []string{"containerd-shim-runc-v2", "-namespace", "k8s.io"},
				BundleDir:        "/run/containerd/bundle/test",
				SandboxBundleDir: "/run/containerd/sandbox/test",
			},
		},
	}

	node := buildRuntimeShimNode(detail)
	text := joinNodeTexts(node)
	if strings.Contains(text, "Bundle Dir:") {
		t.Fatalf("shim node unexpectedly contains bundle dir: %s", text)
	}
	if !strings.Contains(text, "Sandbox Bundle:") {
		t.Fatalf("shim node missing sandbox bundle: %s", text)
	}
}

func TestBuildRuntimeNamespaceNodeOmitsSharedPIDNamespaceRow(t *testing.T) {
	detail := &models.ContainerDetail{
		SharedPID: boolPtr(true),
		Namespaces: map[string]string{
			"network": "/var/run/netns/pod",
			"pid":     "/proc/123/ns/pid",
		},
	}

	node := buildRuntimeNamespaceNode(detail)
	text := joinNodeTexts(node)
	if strings.Contains(text, "Shared PID Namespace") {
		t.Fatalf("namespace node unexpectedly contains shared pid row: %s", text)
	}
	if !strings.Contains(text, "network: [white]/var/run/netns/pod") && !strings.Contains(text, "network: /var/run/netns/pod") {
		t.Fatalf("namespace node missing namespace entries: %s", text)
	}
	if !strings.Contains(text, "pid: [white]/proc/123/ns/pid") && !strings.Contains(text, "pid: /proc/123/ns/pid") {
		t.Fatalf("namespace node missing pid entry: %s", text)
	}
}

func joinNodeTexts(node interface {
	GetChildren() []*tview.TreeNode
	GetText() string
}) string {
	rows := []string{}
	var walk func(current interface {
		GetChildren() []*tview.TreeNode
		GetText() string
	})
	walk = func(current interface {
		GetChildren() []*tview.TreeNode
		GetText() string
	}) {
		rows = append(rows, current.GetText())
		for _, child := range current.GetChildren() {
			walk(child)
		}
	}
	walk(node)
	return strings.Join(rows, "\n")
}

func boolPtr(value bool) *bool {
	return &value
}
