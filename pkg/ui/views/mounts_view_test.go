package views

import (
	"context"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

func TestMountsViewToggleGroupWithE(t *testing.T) {
	view := NewMountsView(nil, nil, nil)
	view.mounts = []*models.Mount{
		{Destination: "/", Source: "overlay", Type: "overlay", Origin: models.MountOriginLiveExtra, State: models.MountStateLiveOnly},
		{Destination: "/etc/hosts", Source: "tmpfs", Type: "tmpfs", Origin: models.MountOriginRuntimeDefault, State: models.MountStateDeclaredLive},
		{Destination: "/run", Source: "tmpfs", Type: "tmpfs", Origin: models.MountOriginRuntimeDefault, State: models.MountStateDeclaredOnly},
	}

	view.render()

	runtimeNode := findTreeNode(view.tree.GetRoot(), "Runtime Mounts")
	if runtimeNode == nil {
		t.Fatal("expected Runtime Mounts group to exist")
	}
	if runtimeNode.IsExpanded() {
		t.Fatal("expected Runtime Mounts to start collapsed")
	}

	view.tree.SetCurrentNode(runtimeNode)
	if handled := view.HandleInput(tcell.NewEventKey(tcell.KeyRune, 'e', 0)); handled != nil {
		t.Fatal("expected e to be consumed when a group is selected")
	}

	if !runtimeNode.IsExpanded() {
		t.Fatal("expected Runtime Mounts group to be expanded")
	}
	if view.detailView.GetText(false) == "" {
		t.Fatal("expected detail panel text to remain available after toggle")
	}
}

func TestPreferredMountSourcePrefersHostPath(t *testing.T) {
	mount := &models.Mount{
		Source:     "overlay",
		HostPath:   "/var/lib/kubelet/pods/test/volumes/projected/data",
		LiveSource: "/dev/something",
	}

	got := preferredMountSource(mount)
	if got != "/var/lib/kubelet/pods/test/volumes/projected/data" {
		t.Fatalf("preferredMountSource() = %q, want host path", got)
	}
}

func TestDisplayMountSourceUsesRootfsPathForRootMount(t *testing.T) {
	mount := &models.Mount{
		Destination: "/",
		Source:      "overlay",
		HostPath:    "/var/lib/containerd/rootfs/merged",
	}
	detail := &models.ContainerDetail{
		WritableLayerPath: "/var/lib/containerd/io.containerd.snapshotter.v1.overlayfs/snapshots/42/fs",
		RuntimeProfile: &models.RuntimeProfile{
			RootFS: &models.RootFSInfo{
				MountRootFSPath: "/run/containerd/io.containerd.runtime.v2.task/k8s.io/test/rootfs",
			},
		},
	}

	got := displayMountSource(mount, detail)
	if got != "/run/containerd/io.containerd.runtime.v2.task/k8s.io/test/rootfs" {
		t.Fatalf("displayMountSource() = %q, want rootfs path", got)
	}
}

func TestStorageViewSwitchToMountsUsesRuntimeRootfsPath(t *testing.T) {
	view := NewStorageView(nil, storageTestRuntime{}, context.Background())
	view.SetContainer("container-1")
	view.activeMode = StorageModeMounts
	if err := view.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}

	root := view.mountsView.tree.GetRoot()
	if root == nil || len(root.GetChildren()) == 0 {
		t.Fatal("expected mounts tree to have root entry")
	}

	first := root.GetChildren()[0]
	if !strings.Contains(view.mountsView.detailView.GetText(false), "/run/containerd/io.containerd.runtime.v2.task/k8s.io/test/rootfs") {
		t.Fatalf("root mount text = %q, detail = %q, want rootfs path", first.GetText(), view.mountsView.detailView.GetText(false))
	}
}

type storageTestRuntime struct{}

func (storageTestRuntime) Connect(context.Context) error { return nil }

func (storageTestRuntime) Close() error { return nil }

func (storageTestRuntime) ListContainers(context.Context) ([]*models.Container, error) {
	return nil, nil
}

func (storageTestRuntime) GetContainer(context.Context, string) (*models.Container, error) {
	return nil, nil
}

func (storageTestRuntime) GetContainerDetail(context.Context, string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{}, nil
}

func (storageTestRuntime) GetContainerRuntimeInfo(context.Context, string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{}, nil
}

func (storageTestRuntime) GetContainerStorageInfo(context.Context, string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{
		RuntimeProfile: &models.RuntimeProfile{
			RootFS: &models.RootFSInfo{
				MountRootFSPath: "/run/containerd/io.containerd.runtime.v2.task/k8s.io/test/rootfs",
			},
		},
	}, nil
}

func (storageTestRuntime) GetContainerNetworkInfo(context.Context, string) (*models.ContainerDetail, error) {
	return &models.ContainerDetail{}, nil
}

func (storageTestRuntime) ListImages(context.Context) ([]*models.Image, error) { return nil, nil }

func (storageTestRuntime) GetImage(context.Context, string) (*models.Image, error) { return nil, nil }

func (storageTestRuntime) GetImageLayers(context.Context, string, string, string) ([]*models.ImageLayer, error) {
	return nil, nil
}

func (storageTestRuntime) GetImageConfigInfo(context.Context, string) (*models.ImageConfigInfo, error) {
	return nil, nil
}

func (storageTestRuntime) ListPods(context.Context) ([]*models.Pod, error) { return nil, nil }

func (storageTestRuntime) GetContainerProcesses(context.Context, string) ([]*models.Process, error) {
	return nil, nil
}

func (storageTestRuntime) GetContainerTop(context.Context, string) (*models.ProcessTop, error) {
	return nil, nil
}

func (storageTestRuntime) GetContainerMounts(context.Context, string) ([]*models.Mount, error) {
	return []*models.Mount{{Destination: "/", Source: "overlay", Type: "overlay", Origin: models.MountOriginLiveExtra, State: models.MountStateLiveOnly}}, nil
}

var _ runtime.Runtime = storageTestRuntime{}

func findTreeNode(node *tview.TreeNode, target string) *tview.TreeNode {
	if node == nil {
		return nil
	}
	plain := strings.ReplaceAll(node.GetText(), "[aqua::b]", "")
	plain = strings.ReplaceAll(plain, "[yellow::b]", "")
	plain = strings.ReplaceAll(plain, "[-:-:-]", "")
	if strings.Contains(plain, target) {
		return node
	}
	for _, child := range node.GetChildren() {
		if found := findTreeNode(child, target); found != nil {
			return found
		}
	}
	return nil
}
