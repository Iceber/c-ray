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

// RuntimeInfoView renders the Runtime page.
type RuntimeInfoView struct {
	*tview.Flex

	app       *tview.Application
	tree      *tview.TreeView
	statusBar *tview.TextView
	container runtime.Container
	mu        sync.Mutex
}

// NewRuntimeInfoView creates a new runtime info view.
func NewRuntimeInfoView(app *tview.Application) *RuntimeInfoView {
	v := &RuntimeInfoView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(components.ColorFgBorder)
	v.tree.SetRoot(tview.NewTreeNode(components.Muted("No runtime data")).SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the container handle.
func (v *RuntimeInfoView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
	v.renderEmpty()
	v.updateStatusBar()
}

// Refresh loads runtime metadata from the container handle.
func (v *RuntimeInfoView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.renderEmpty()
		return nil
	}

	profile, err := c.Runtime(ctx)
	if err != nil {
		v.renderError(err)
		return err
	}

	config, _ := c.Config(ctx)
	state, _ := c.State(ctx)

	v.render(profile, config, state)
	v.updateStatusBar()
	return nil
}

func (v *RuntimeInfoView) renderError(err error) {
	queueUpdateDraw(v.app, func() {
		root := tview.NewTreeNode(components.Accent("Runtime")).SetSelectable(false).SetExpanded(true)
		root.AddChild(tview.NewTreeNode(fmt.Sprintf("[%s]Failed to load runtime: %v[-]", components.ColorName(components.ColorFgError), err)).SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
	})
}

// HandleInput processes tree interaction.
func (v *RuntimeInfoView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil || event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Key() {
	case tcell.KeyEnter:
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	}
	switch event.Rune() {
	case 'e', 'E':
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	case 'a', 'A':
		v.expandAll()
		return nil
	}
	return event
}

// GetFocusPrimitive returns the tree focus target.
func (v *RuntimeInfoView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

func (v *RuntimeInfoView) renderEmpty() {
	queueUpdateDraw(v.app, func() {
		root := tview.NewTreeNode(components.Accent("Runtime")).SetSelectable(false).SetExpanded(true)
		root.AddChild(tview.NewTreeNode(components.Muted("Refresh to resolve shim, OCI runtime and namespace metadata")).SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
	})
}

func (v *RuntimeInfoView) render(rt *runtime.RuntimeProfile, config *runtime.ContainerConfig, state *runtime.ContainerState) {
	root := tview.NewTreeNode(components.Accent("Runtime")).SetSelectable(false).SetExpanded(true)

	if rt == nil {
		root.AddChild(tview.NewTreeNode(components.Muted("Runtime metadata unresolved")).SetSelectable(false))
	} else {
		root.AddChild(buildShimNodeV1(rt, state))
		root.AddChild(buildOCINodeV1(rt))
		root.AddChild(buildNamespaceNodeV1(config))
	}

	queueUpdateDraw(v.app, func() {
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
	})
}

func (v *RuntimeInfoView) expandAll() {
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
}

func (v *RuntimeInfoView) updateStatusBar() {
	v.statusBar.SetText(fmt.Sprintf(
		" %s  |  %s  %s",
		components.Muted("Runtime: shim, OCI runtime and namespace anchors"),
		components.KeyHint("e", "toggle"),
		components.KeyHint("a", "expand/collapse"),
	))
}

func buildShimNodeV1(runtime *runtime.RuntimeProfile, state *runtime.ContainerState) *tview.TreeNode {
	node := tview.NewTreeNode(fmt.Sprintf("[%s::b]Shim[-:-:-]", components.ColorName(components.ColorFgAccentAlt))).SetSelectable(true).SetExpanded(true)

	rows := []string{}
	if state != nil {
		if state.PID > 0 {
			rows = append(rows, fmt.Sprintf("Task PID: %d", state.PID))
		}
		if state.PPID > 0 {
			rows = append(rows, fmt.Sprintf("Shim PID: %d", state.PPID))
		}
	}

	if runtime.Shim != nil {
		shim := runtime.Shim
		if shim.BinaryPath != "" {
			rows = append(rows, "Binary: "+shim.BinaryPath)
		}
		if shim.SocketAddress != "" {
			rows = append(rows, "Socket: "+shim.SocketAddress)
		}
		if len(shim.Cmdline) > 0 {
			rows = append(rows, "Command: "+strings.Join(shim.Cmdline, " "))
		}
		if shim.SandboxBundleDir != "" {
			rows = append(rows, "Sandbox Bundle: "+shim.SandboxBundleDir)
		}
	}

	if runtime.Conmon != nil {
		conmon := runtime.Conmon
		if conmon.PID > 0 {
			rows = append(rows, fmt.Sprintf("Conmon PID: %d", conmon.PID))
		}
		if conmon.BinaryPath != "" {
			rows = append(rows, "Conmon Binary: "+conmon.BinaryPath)
		}
		if len(conmon.Cmdline) > 0 {
			rows = append(rows, "Conmon Command: "+strings.Join(conmon.Cmdline, " "))
		}
		if conmon.LogPath != "" {
			rows = append(rows, "Log Path: "+conmon.LogPath)
		}
	}

	if len(rows) == 0 {
		rows = append(rows, "Shim metadata unresolved")
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s", components.Muted(row))).SetSelectable(false))
	}
	return node
}

func buildOCINodeV1(runtime *runtime.RuntimeProfile) *tview.TreeNode {
	node := tview.NewTreeNode(components.Accent("OCI Runtime")).SetSelectable(true).SetExpanded(true)
	oci := runtime.OCI
	if oci == nil {
		node.AddChild(tview.NewTreeNode(components.Muted("  OCI runtime metadata unresolved")).SetSelectable(false))
		return node
	}

	rows := []string{}
	if oci.RuntimeName != "" {
		rows = append(rows, "Runtime Name: "+oci.RuntimeName)
	}
	if oci.RuntimeBinary != "" {
		rows = append(rows, "Runtime Binary: "+oci.RuntimeBinary)
	}
	if oci.BundleDir != "" {
		rows = append(rows, "Bundle Dir: "+oci.BundleDir)
	}
	if oci.StateDir != "" {
		rows = append(rows, "State Dir: "+oci.StateDir)
	}
	if oci.ConfigPath != "" {
		rows = append(rows, "Config Path: "+oci.ConfigPath)
	}
	if len(rows) == 0 {
		rows = append(rows, "OCI runtime metadata unresolved")
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s", components.Muted(row))).SetSelectable(false))
	}
	return node
}

func buildNamespaceNodeV1(config *runtime.ContainerConfig) *tview.TreeNode {
	node := tview.NewTreeNode(components.Accent("Namespace")).SetSelectable(true).SetExpanded(true)
	if config == nil || len(config.Namespaces) == 0 {
		node.AddChild(tview.NewTreeNode(components.Muted("  Namespace metadata unresolved")).SetSelectable(false))
		return node
	}

	keys := make([]string, 0, len(config.Namespaces))
	for k := range config.Namespaces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted(k+":"), components.Bright(config.Namespaces[k]))).SetSelectable(false))
	}
	return node
}
