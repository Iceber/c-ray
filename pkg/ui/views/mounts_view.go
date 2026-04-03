package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
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
	components.InitTreeView(v.tree)
	v.tree.SetRoot(components.NewTreeNode(components.Muted("No mount metadata")).SetSelectable(false))
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
	v.detailView.SetBorder(true).SetBorderColor(components.ColorFgBorder).SetTitle(fmt.Sprintf(" %s ", components.Accent("Mount Detail")))

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
		v.mu.Lock()
		v.mounts = nil
		v.mu.Unlock()
		v.render()
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
	return components.HandleTreeInput(event, v.tree, v.expandAll, func(node *tview.TreeNode) {
		v.renderSelectionDetail(node)
	})
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

	root := components.NewTreeNode(components.Accent("Mounts")).SetSelectable(false).SetExpanded(true)
	if len(mounts) == 0 {
		root.AddChild(components.NewTreeNode(components.Muted("Refresh to resolve mounts")).SetSelectable(false))
	} else {
		rootMount, criMounts, runtimeMounts, otherMounts := splitMounts(mounts)
		if rootMount != nil {
			root.AddChild(buildMountNodeV1(rootMount, runtimePath))
		}
		root.AddChild(buildMountGroupNodeV1("CRI Mounts", criMounts, true, runtimePath))
		root.AddChild(buildMountGroupNodeV1("Runtime Mounts", runtimeMounts, false, runtimePath))
		root.AddChild(buildMountGroupNodeV1("Kernel / Other", otherMounts, false, runtimePath))
	}

	current := root
	if len(root.GetChildren()) > 0 {
		current = root.GetChildren()[0]
	}

	queueUpdateDraw(v.app, func() {
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(current)
		components.ApplyTreeFocusStyle(v.tree, true)
		v.renderSelectionDetail(current)
	})
}

func (v *MountsView) renderSelectionDetail(node *tview.TreeNode) {
	if node == nil {
		v.detailView.SetText(fmt.Sprintf(" %s", components.Muted("Select a mount entry to inspect")))
		return
	}
	mount, _ := node.GetReference().(*runtime.Mount)
	if mount == nil {
		v.detailView.SetText(fmt.Sprintf(" %s", components.Muted("Select a concrete mount entry")))
		return
	}

	v.mu.Lock()
	runtimePath := v.runtimePath
	v.mu.Unlock()

	v.detailView.SetText(fmt.Sprintf(
		" %s\n %s\n %s   %s   %s\n %s",
		components.KV("Target: ", fallbackMountField(mount.Destination)),
		components.KV("Source: ", fallbackMountField(displaySource(mount, runtimePath))),
		components.KV("Type: ", fallbackMountField(mount.Type)),
		components.KV("Origin: ", fallbackMountField(mountOriginStr(mount.Origin))),
		components.KV("State: ", fallbackMountField(mountStateStr(mount.State))),
		components.KV("Command: ", buildMountCmd(mount, runtimePath)),
	))
}

func (v *MountsView) expandAll() {
	root := v.tree.GetRoot()
	components.ExpandAllNodes(root)
	v.tree.SetCurrentNode(root)
	v.renderSelectionDetail(root)
}

func (v *MountsView) updateStatusBar() {
	v.statusBar.SetText(fmt.Sprintf(" %s  |  %s  %s",
		components.Muted("Mounts: rootfs, CRI, runtime defaults and live extras"),
		components.KeyHint("e", "toggle"),
		components.KeyHint("a", "expand/collapse"),
	))
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
	label := components.Accent(fmt.Sprintf("%s (%d)", title, len(mounts)))
	node := components.NewTreeNode(label).SetSelectable(true).SetExpanded(expanded)
	if len(mounts) == 0 {
		node.AddChild(components.NewTreeNode(components.Muted("No entries")).SetSelectable(true))
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
	node := components.NewTreeNode(label).SetReference(m).SetSelectable(true).SetExpanded(false)
	node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Type:"), components.Bright(fallbackMountField(m.Type)))).SetSelectable(true))
	node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Source:"), components.Bright(fallbackMountField(displaySource(m, runtimePath))))).SetSelectable(true))
	if m.HostPath != "" {
		node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Host Path:"), components.Bright(m.HostPath))).SetSelectable(true))
	}
	if m.LiveSource != "" && m.LiveSource != m.Source {
		node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Live Source:"), components.Bright(m.LiveSource))).SetSelectable(true))
	}
	node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Options:"), components.Bright(joinOpts(m.Options)))).SetSelectable(true))
	node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Origin:"), components.Bright(mountOriginStr(m.Origin)))).SetSelectable(true))
	node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("State:"), components.Bright(mountStateStr(m.State)))).SetSelectable(true))
	if m.Note != "" {
		node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Note:"), components.Bright(m.Note))).SetSelectable(true))
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
	return fallbackValue(value, "-")
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
