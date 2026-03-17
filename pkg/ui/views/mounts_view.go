package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// MountsView renders the Mounts subpage.
type MountsView struct {
	*tview.Flex

	app         *tview.Application
	tree        *tview.TreeView
	detailView  *tview.TextView
	statusBar   *tview.TextView
	container   runtime.Container
	mounts      []*runtime.Mount
	runtimePath string // rootfs path for the root mount display
	mu          sync.Mutex
}

// NewMountsView creates a new Mounts view.
func NewMountsView(app *tview.Application) *MountsView {
	v := &MountsView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
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

	v.detailView = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	v.detailView.SetBorder(true).SetBorderColor(tcell.ColorDarkCyan).SetTitle(" Mount Detail ")

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.detailView, 6, 0, false)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the container handle.
func (v *MountsView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mounts = nil
	v.runtimePath = ""
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
}

// Refresh loads the mount list from the container handle.
func (v *MountsView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.render()
		return nil
	}

	mounts, err := c.Mounts(ctx)
	if err != nil {
		return err
	}

	// Get rootfs path for root mount display.
	runtimePath := ""
	if rt, err := c.Runtime(ctx); err == nil && rt != nil && rt.RootFSPath != "" {
		runtimePath = rt.RootFSPath
	}

	v.mu.Lock()
	v.mounts = mounts
	v.runtimePath = runtimePath
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
	return nil
}

// HandleInput processes tree interaction.
func (v *MountsView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil || event.Key() == tcell.KeyCtrlC {
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
	mounts := make([]*runtime.Mount, len(v.mounts))
	copy(mounts, v.mounts)
	runtimePath := v.runtimePath
	v.mu.Unlock()

	root := tview.NewTreeNode("[aqua::b]Mounts[-:-:-]").SetSelectable(false).SetExpanded(true)
	if len(mounts) == 0 {
		root.AddChild(tview.NewTreeNode("[gray]Refresh to resolve mounts[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		v.renderSelectionDetail(root)
		return
	}

	rootMount, criMounts, runtimeMounts, otherMounts := splitMounts(mounts)
	if rootMount != nil {
		root.AddChild(buildMountNodeV1(rootMount, runtimePath))
	}
	root.AddChild(buildMountGroupNodeV1("CRI Mounts", criMounts, true, runtimePath))
	root.AddChild(buildMountGroupNodeV1("Runtime Mounts", runtimeMounts, false, runtimePath))
	root.AddChild(buildMountGroupNodeV1("Kernel / Other", otherMounts, false, runtimePath))

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
		v.detailView.SetText(" [gray]Select a mount entry to inspect[-]")
		return
	}
	mount, _ := node.GetReference().(*runtime.Mount)
	if mount == nil {
		v.detailView.SetText(" [gray]Select a concrete mount entry[-]")
		return
	}

	v.mu.Lock()
	runtimePath := v.runtimePath
	v.mu.Unlock()

	v.detailView.SetText(fmt.Sprintf(
		" [gray]Target:[-] [white]%s[-]\n [gray]Source:[-] [white]%s[-]\n [gray]Type:[-] [white]%s[-]   [gray]Origin:[-] [white]%s[-]   [gray]State:[-] [white]%s[-]\n [gray]Command:[-] [white]%s[-]",
		fallbackMountField(mount.Destination),
		fallbackMountField(displaySource(mount, runtimePath)),
		fallbackMountField(mount.Type),
		fallbackMountField(mountOriginStr(mount.Origin)),
		fallbackMountField(mountStateStr(mount.State)),
		buildMountCmd(mount, runtimePath),
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
	v.statusBar.SetText(" [white]Mounts:[-] rootfs, CRI, runtime defaults and live extras  |  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse all")
}

// --- Helpers ---

func splitMounts(mounts []*runtime.Mount) (*runtime.Mount, []*runtime.Mount, []*runtime.Mount, []*runtime.Mount) {
	var rootMount *runtime.Mount
	var criMounts, runtimeMounts, other []*runtime.Mount
	for _, m := range mounts {
		if m == nil {
			continue
		}
		if m.Destination == "/" && rootMount == nil {
			rootMount = m
			continue
		}
		switch m.Origin {
		case runtime.MountOriginCRI:
			criMounts = append(criMounts, m)
		case runtime.MountOriginRuntimeDefault:
			runtimeMounts = append(runtimeMounts, m)
		default:
			other = append(other, m)
		}
	}
	sortMounts(criMounts)
	sortMounts(runtimeMounts)
	sortMounts(other)
	return rootMount, criMounts, runtimeMounts, other
}

func sortMounts(mounts []*runtime.Mount) {
	sort.SliceStable(mounts, func(i, j int) bool {
		return mountSortKey(mounts[i]) < mountSortKey(mounts[j])
	})
}

func mountSortKey(m *runtime.Mount) string {
	if m == nil {
		return ""
	}
	prefix := "1"
	if strings.HasPrefix(m.Destination, "/etc") {
		prefix = "0"
	}
	return prefix + ":" + m.Destination + ":" + preferredSource(m)
}

func buildMountGroupNodeV1(title string, mounts []*runtime.Mount, expanded bool, runtimePath string) *tview.TreeNode {
	label := fmt.Sprintf("[aqua::b]%s (%d)[-:-:-]", title, len(mounts))
	node := tview.NewTreeNode(label).SetSelectable(true).SetExpanded(expanded)
	if len(mounts) == 0 {
		node.AddChild(tview.NewTreeNode("[gray]No entries[-]").SetSelectable(false))
		return node
	}
	for _, m := range mounts {
		node.AddChild(buildMountNodeV1(m, runtimePath))
	}
	return node
}

func buildMountNodeV1(m *runtime.Mount, runtimePath string) *tview.TreeNode {
	target := cropColumn(fallbackMountField(m.Destination), 28)
	source := cropColumn(fallbackMountField(displaySource(m, runtimePath)), 44)
	label := fmt.Sprintf("%-28s  %s", target, source)
	node := tview.NewTreeNode(label).SetReference(m).SetSelectable(true).SetExpanded(false)
	node.AddChild(tview.NewTreeNode("[gray]  Type: [white]" + fallbackMountField(m.Type) + "[-]").SetSelectable(false))
	node.AddChild(tview.NewTreeNode("[gray]  Source: [white]" + fallbackMountField(displaySource(m, runtimePath)) + "[-]").SetSelectable(false))
	if m.HostPath != "" {
		node.AddChild(tview.NewTreeNode("[gray]  Host Path: [white]" + m.HostPath + "[-]").SetSelectable(false))
	}
	if m.LiveSource != "" && m.LiveSource != m.Source {
		node.AddChild(tview.NewTreeNode("[gray]  Live Source: [white]" + m.LiveSource + "[-]").SetSelectable(false))
	}
	node.AddChild(tview.NewTreeNode("[gray]  Options: [white]" + joinOpts(m.Options) + "[-]").SetSelectable(false))
	node.AddChild(tview.NewTreeNode("[gray]  Origin: [white]" + mountOriginStr(m.Origin) + "[-]").SetSelectable(false))
	node.AddChild(tview.NewTreeNode("[gray]  State: [white]" + mountStateStr(m.State) + "[-]").SetSelectable(false))
	if m.Note != "" {
		node.AddChild(tview.NewTreeNode("[gray]  Note: [white]" + m.Note + "[-]").SetSelectable(false))
	}
	return node
}

func preferredSource(m *runtime.Mount) string {
	if m == nil {
		return ""
	}
	if strings.TrimSpace(m.HostPath) != "" {
		return m.HostPath
	}
	if strings.TrimSpace(m.LiveSource) != "" {
		return m.LiveSource
	}
	return m.Source
}

func displaySource(m *runtime.Mount, runtimePath string) string {
	if m == nil {
		return ""
	}
	if m.Destination == "/" && strings.TrimSpace(runtimePath) != "" {
		return runtimePath
	}
	return preferredSource(m)
}

func fallbackMountField(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func cropColumn(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}

func joinOpts(opts []string) string {
	if len(opts) == 0 {
		return "-"
	}
	return strings.Join(opts, ",")
}

func mountOriginStr(origin runtime.MountOrigin) string {
	switch origin {
	case runtime.MountOriginCRI:
		return "CRI"
	case runtime.MountOriginRuntimeDefault:
		return "runtime-default"
	case runtime.MountOriginLiveExtra:
		return "kernel/live-extra"
	default:
		return string(origin)
	}
}

func mountStateStr(state runtime.MountState) string {
	switch state {
	case runtime.MountStateDeclaredLive:
		return "declared + live"
	case runtime.MountStateDeclaredOnly:
		return "declared only"
	case runtime.MountStateLiveOnly:
		return "live only"
	default:
		return string(state)
	}
}

func buildMountCmd(m *runtime.Mount, runtimePath string) string {
	if m == nil {
		return "-"
	}
	args := []string{"mount"}
	if m.Type != "" {
		args = append(args, "-t", m.Type)
	}
	if len(m.Options) > 0 {
		args = append(args, "-o", strings.Join(m.Options, ","))
	}
	args = append(args, fallbackMountField(displaySource(m, runtimePath)), fallbackMountField(m.Destination))
	return strings.Join(args, " ")
}
