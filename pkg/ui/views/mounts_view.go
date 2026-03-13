package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// MountsView renders the Mounts subpage.
type MountsView struct {
	*tview.Flex

	app         *tview.Application
	rt          runtime.Runtime
	ctx         context.Context
	tree        *tview.TreeView
	detailView  *tview.TextView
	statusBar   *tview.TextView
	containerID string
	mounts      []*models.Mount
	runtimeInfo *models.ContainerDetail
	mu          sync.Mutex
}

// NewMountsView creates a new Mounts view.
func NewMountsView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *MountsView {
	v := &MountsView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
		ctx:  ctx,
	}

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.tree.SetRoot(tview.NewTreeNode("[gray]No mount metadata[-]").SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
			v.renderSelectionDetail(node)
		}
	})
	v.tree.SetChangedFunc(func(node *tview.TreeNode) {
		v.renderSelectionDetail(node)
	})

	v.detailView = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	v.detailView.SetBorder(true)
	v.detailView.SetBorderColor(tcell.ColorDarkCyan)
	v.detailView.SetTitle(" Mount Detail ")

	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.detailView, 6, 0, false)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.render()
	v.updateStatusBar()
	return v
}

// SetContainer sets the active container.
func (v *MountsView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.mounts = nil
	v.runtimeInfo = nil
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
}

// SetRuntimeInfo stores runtime context.
func (v *MountsView) SetRuntimeInfo(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.runtimeInfo = detail
	v.mu.Unlock()
	v.render()
}

// Refresh loads the mount list.
func (v *MountsView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	containerID := v.containerID
	v.mu.Unlock()

	if strings.TrimSpace(containerID) == "" {
		v.render()
		return nil
	}

	mounts, err := v.rt.GetContainerMounts(ctx, containerID)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.mounts = mounts
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
	return nil
}

// HandleInput processes tree interaction.
func (v *MountsView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return nil
	}
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Key() {
	case tcell.KeyEnter:
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
			v.renderSelectionDetail(node)
		}
		return nil
	}
	switch event.Rune() {
	case 'e', 'E':
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
			v.renderSelectionDetail(node)
		}
		return nil
	case 'a', 'A':
		v.expandAll()
		return nil
	}
	return event
}

// GetFocusPrimitive returns the tree focus target.
func (v *MountsView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

func (v *MountsView) render() {
	v.mu.Lock()
	mounts := append([]*models.Mount(nil), v.mounts...)
	runtimeInfo := v.runtimeInfo
	v.mu.Unlock()

	root := tview.NewTreeNode("[aqua::b]Mounts[-:-:-]").SetSelectable(false).SetExpanded(true)
	if len(mounts) == 0 {
		root.AddChild(tview.NewTreeNode("[gray]Refresh to resolve rootfs, CRI mounts, runtime defaults and live residual mounts[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		v.renderSelectionDetail(root)
		return
	}

	rootMount, criMounts, runtimeMounts, otherMounts := splitMountGroups(mounts)
	if rootMount != nil {
		root.AddChild(buildMountNode(rootMount, runtimeInfo))
	}
	root.AddChild(buildMountGroupNode("CRI Mounts", criMounts, true, runtimeInfo))
	root.AddChild(buildMountGroupNode("Runtime Mounts", runtimeMounts, false, runtimeInfo))
	root.AddChild(buildMountGroupNode("Kernel / Other", otherMounts, false, runtimeInfo))

	v.tree.SetRoot(root)
	current := root
	if len(root.GetChildren()) > 0 {
		current = root.GetChildren()[0]
	}
	v.tree.SetCurrentNode(current)
	v.renderSelectionDetail(current)
}

func (v *MountsView) renderSelectionDetail(node *tview.TreeNode) {
	if node == nil {
		v.detailView.SetText(" [gray]Select a mount entry to inspect its source and mount command[-]")
		return
	}
	mount, _ := node.GetReference().(*models.Mount)
	if mount == nil {
		v.detailView.SetText(" [gray]Select a concrete mount entry to open details below[-]")
		return
	}

	v.detailView.SetText(fmt.Sprintf(
		" [gray]Target:[-] [white]%s[-]\n [gray]Source:[-] [white]%s[-]\n [gray]Type:[-] [white]%s[-]   [gray]Origin:[-] [white]%s[-]   [gray]State:[-] [white]%s[-]\n [gray]Command:[-] [white]%s[-]",
		fallbackMountValue(mount.Destination),
		fallbackMountValue(displayMountSource(mount, v.runtimeInfo)),
		fallbackMountValue(mount.Type),
		fallbackMountValue(mountOriginLabel(mount.Origin)),
		fallbackMountValue(mountStateLabel(mount.State)),
		buildMountCommand(mount, v.runtimeInfo),
	))
}

func (v *MountsView) expandAll() {
	root := v.tree.GetRoot()
	if root == nil {
		return
	}
	expand := !root.IsExpanded()
	var walk func(node *tview.TreeNode)
	walk = func(node *tview.TreeNode) {
		node.SetExpanded(expand)
		for _, child := range node.GetChildren() {
			walk(child)
		}
	}
	walk(root)
	root.SetExpanded(true)
	v.tree.SetCurrentNode(root)
	v.renderSelectionDetail(root)
}

func (v *MountsView) updateStatusBar() {
	v.statusBar.SetText(" [white]Mounts:[-] rootfs, CRI mounts, runtime defaults and live extras  |  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse all")
}

func splitMountGroups(mounts []*models.Mount) (*models.Mount, []*models.Mount, []*models.Mount, []*models.Mount) {
	var rootMount *models.Mount
	criMounts := []*models.Mount{}
	runtimeMounts := []*models.Mount{}
	otherMounts := []*models.Mount{}

	for _, mount := range mounts {
		if mount == nil {
			continue
		}
		if mount.Destination == "/" && rootMount == nil {
			rootMount = mount
			continue
		}
		switch mount.Origin {
		case models.MountOriginCRI:
			criMounts = append(criMounts, mount)
		case models.MountOriginRuntimeDefault:
			runtimeMounts = append(runtimeMounts, mount)
		default:
			otherMounts = append(otherMounts, mount)
		}
	}

	sortMountGroup(criMounts)
	sortMountGroup(runtimeMounts)
	sortMountGroup(otherMounts)
	return rootMount, criMounts, runtimeMounts, otherMounts
}

func sortMountGroup(mounts []*models.Mount) {
	sort.SliceStable(mounts, func(i, j int) bool {
		left := mountSortKey(mounts[i])
		right := mountSortKey(mounts[j])
		return left < right
	})
}

func mountSortKey(mount *models.Mount) string {
	if mount == nil {
		return ""
	}
	prefix := "1"
	if strings.HasPrefix(mount.Destination, "/etc") {
		prefix = "0"
	}
	return prefix + ":" + mount.Destination + ":" + preferredMountSource(mount)
}

func buildMountGroupNode(title string, mounts []*models.Mount, expanded bool, detail *models.ContainerDetail) *tview.TreeNode {
	label := fmt.Sprintf("[aqua::b]%s (%d)[-:-:-]", title, len(mounts))
	node := tview.NewTreeNode(label).SetSelectable(true).SetExpanded(expanded)
	if len(mounts) == 0 {
		node.AddChild(tview.NewTreeNode("[gray]No entries[-]").SetSelectable(false))
		return node
	}
	for _, mount := range mounts {
		node.AddChild(buildMountNode(mount, detail))
	}
	return node
}

func buildMountNode(mount *models.Mount, detail *models.ContainerDetail) *tview.TreeNode {
	target := cropMountColumn(fallbackMountValue(mount.Destination), 28)
	source := cropMountColumn(fallbackMountValue(displayMountSource(mount, detail)), 44)
	label := fmt.Sprintf("%-28s  %s", target, source)
	node := tview.NewTreeNode(label).SetReference(mount).SetSelectable(true).SetExpanded(false)
	node.AddChild(tview.NewTreeNode("[gray]  Type: [white]" + fallbackMountValue(mount.Type) + "[-]").SetSelectable(false))
	node.AddChild(tview.NewTreeNode("[gray]  Source: [white]" + fallbackMountValue(displayMountSource(mount, detail)) + "[-]").SetSelectable(false))
	if mount.HostPath != "" {
		node.AddChild(tview.NewTreeNode("[gray]  Host Path: [white]" + mount.HostPath + "[-]").SetSelectable(false))
	}
	if mount.LiveSource != "" && mount.LiveSource != mount.Source {
		node.AddChild(tview.NewTreeNode("[gray]  Live Source: [white]" + mount.LiveSource + "[-]").SetSelectable(false))
	}
	node.AddChild(tview.NewTreeNode("[gray]  Options: [white]" + joinMountOptions(mount.Options) + "[-]").SetSelectable(false))
	node.AddChild(tview.NewTreeNode("[gray]  Origin: [white]" + mountOriginLabel(mount.Origin) + "[-]").SetSelectable(false))
	node.AddChild(tview.NewTreeNode("[gray]  State: [white]" + mountStateLabel(mount.State) + "[-]").SetSelectable(false))
	if mount.Note != "" {
		node.AddChild(tview.NewTreeNode("[gray]  Note: [white]" + mount.Note + "[-]").SetSelectable(false))
	}
	return node
}

func preferredMountSource(mount *models.Mount) string {
	if mount == nil {
		return ""
	}
	if strings.TrimSpace(mount.HostPath) != "" {
		return mount.HostPath
	}
	if strings.TrimSpace(mount.LiveSource) != "" {
		return mount.LiveSource
	}
	return mount.Source
}

func displayMountSource(mount *models.Mount, detail *models.ContainerDetail) string {
	if mount == nil {
		return ""
	}
	if mount.Destination == "/" {
		if rootfsPath := resolveRootfsMountPath(detail); strings.TrimSpace(rootfsPath) != "" {
			return rootfsPath
		}
	}
	return preferredMountSource(mount)
}

func resolveRootfsMountPath(detail *models.ContainerDetail) string {
	if detail == nil {
		return ""
	}
	if detail.RuntimeProfile != nil && detail.RuntimeProfile.RootFS != nil {
		rootfs := detail.RuntimeProfile.RootFS
		switch {
		case strings.TrimSpace(rootfs.MountRootFSPath) != "":
			return rootfs.MountRootFSPath
		case strings.TrimSpace(rootfs.BundleRootFSPath) != "":
			return rootfs.BundleRootFSPath
		}
	}
	if strings.TrimSpace(detail.WritableLayerPath) != "" {
		return detail.WritableLayerPath
	}
	if strings.TrimSpace(detail.ReadOnlyLayerPath) != "" {
		return detail.ReadOnlyLayerPath
	}
	return ""
}

func fallbackMountValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func cropMountColumn(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}

func joinMountOptions(options []string) string {
	if len(options) == 0 {
		return "-"
	}
	return strings.Join(options, ",")
}

func mountOriginLabel(origin models.MountOrigin) string {
	switch origin {
	case models.MountOriginCRI:
		return "CRI"
	case models.MountOriginRuntimeDefault:
		return "runtime-default"
	case models.MountOriginLiveExtra:
		return "kernel/live-extra"
	default:
		return string(origin)
	}
}

func mountStateLabel(state models.MountState) string {
	switch state {
	case models.MountStateDeclaredLive:
		return "declared + live"
	case models.MountStateDeclaredOnly:
		return "declared only"
	case models.MountStateLiveOnly:
		return "live only"
	default:
		return string(state)
	}
}

func buildMountCommand(mount *models.Mount, detail *models.ContainerDetail) string {
	if mount == nil {
		return "-"
	}
	args := []string{"mount"}
	if mount.Type != "" {
		args = append(args, "-t", mount.Type)
	}
	if len(mount.Options) > 0 {
		args = append(args, "-o", strings.Join(mount.Options, ","))
	}
	args = append(args, fallbackMountValue(displayMountSource(mount, detail)), fallbackMountValue(mount.Destination))
	return strings.Join(args, " ")
}
