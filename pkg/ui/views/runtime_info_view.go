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

// RuntimeInfoView renders the Runtime page.
type RuntimeInfoView struct {
	*tview.Flex

	rt          runtime.Runtime
	tree        *tview.TreeView
	statusBar   *tview.TextView
	containerID string
	detail      *models.ContainerDetail
	mu          sync.Mutex
}

// NewRuntimeInfoView creates a new runtime info view.
func NewRuntimeInfoView(rt runtime.Runtime) *RuntimeInfoView {
	v := &RuntimeInfoView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		rt:   rt,
	}

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.tree.SetRoot(tview.NewTreeNode("[gray]No runtime data[-]").SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})

	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the active container.
func (v *RuntimeInfoView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.detail = nil
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
}

// Refresh loads runtime metadata.
func (v *RuntimeInfoView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	containerID := v.containerID
	v.mu.Unlock()
	if strings.TrimSpace(containerID) == "" {
		v.render()
		return nil
	}

	detail, err := v.rt.GetContainerRuntimeInfo(ctx, containerID)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
	return nil
}

// HandleInput processes tree interaction.
func (v *RuntimeInfoView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
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

func (v *RuntimeInfoView) render() {
	v.mu.Lock()
	detail := v.detail
	v.mu.Unlock()

	root := tview.NewTreeNode("[aqua::b]Runtime[-:-:-]").SetSelectable(false).SetExpanded(true)
	if detail == nil {
		root.AddChild(tview.NewTreeNode("[gray]Refresh to resolve shim, OCI runtime and namespace metadata[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		return
	}

	root.AddChild(buildRuntimeShimNode(detail))
	root.AddChild(buildRuntimeOCINode(detail))
	root.AddChild(buildRuntimeNamespaceNode(detail))

	v.tree.SetRoot(root)
	v.tree.SetCurrentNode(root)
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
	v.statusBar.SetText(" [white]Runtime:[-] shim, OCI runtime and namespace anchors  |  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse all")
}

func buildRuntimeShimNode(detail *models.ContainerDetail) *tview.TreeNode {
	node := tview.NewTreeNode("[yellow::b]Shim[-:-:-]").SetSelectable(true).SetExpanded(true)
	shim := (*models.ShimInfo)(nil)
	if detail.RuntimeProfile != nil {
		shim = detail.RuntimeProfile.Shim
	}
	rows := []string{
		fmt.Sprintf("Task PID: %d", detail.PID),
		fmt.Sprintf("Shim PID: %d", detail.ShimPID),
	}
	if shim != nil {
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
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}
	return node
}

func buildRuntimeOCINode(detail *models.ContainerDetail) *tview.TreeNode {
	node := tview.NewTreeNode("[aqua::b]OCI Runtime[-:-:-]").SetSelectable(true).SetExpanded(true)
	oci := (*models.OCIInfo)(nil)
	if detail.RuntimeProfile != nil {
		oci = detail.RuntimeProfile.OCI
	}
	rows := []string{}
	if oci != nil {
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
	}
	if len(rows) == 0 {
		rows = append(rows, "OCI runtime metadata unresolved")
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}
	return node
}

func buildRuntimeNamespaceNode(detail *models.ContainerDetail) *tview.TreeNode {
	node := tview.NewTreeNode("[aqua::b]Namespace[-:-:-]").SetSelectable(true).SetExpanded(true)
	if len(detail.Namespaces) == 0 {
		node.AddChild(tview.NewTreeNode("[gray]  Namespace metadata unresolved[-]").SetSelectable(false))
		return node
	}

	keys := make([]string, 0, len(detail.Namespaces))
	for key := range detail.Namespaces {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  %s: [white]%s[-]", key, detail.Namespaces[key])).SetSelectable(false))
	}
	return node
}
