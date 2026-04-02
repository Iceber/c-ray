package components

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// InitTreeView applies the standard tree view style: no border, graphics
// lines enabled, and the default border color for tree connectors.
func InitTreeView(tree *tview.TreeView) {
	tree.SetBorder(false)
	tree.SetGraphics(true)
	tree.SetGraphicsColor(ColorFgBorder)
}

// ExpandAllNodes toggles expand/collapse for all nodes under root.
// The toggle direction is determined by the current state of root:
// if root is expanded, all nodes collapse; otherwise all expand.
// Root itself is always left expanded after the call.
func ExpandAllNodes(root *tview.TreeNode) {
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
}

// HandleTreeInput processes the common tree keyboard shortcuts:
//   - Enter / e / E  → toggle expand on current node
//   - a / A          → expand/collapse all via expandAllFn
//
// Returns nil when the event is consumed, or the original event to propagate.
// onToggle is called after a node is toggled (may be nil).
func HandleTreeInput(event *tcell.EventKey, tree *tview.TreeView, expandAllFn func(), onToggle func(*tview.TreeNode)) *tcell.EventKey {
	if event == nil || event.Key() == tcell.KeyCtrlC {
		return event
	}
	toggleCurrent := func() *tcell.EventKey {
		if node := tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
			if onToggle != nil {
				onToggle(node)
			}
		}
		return nil
	}
	switch event.Key() {
	case tcell.KeyEnter:
		return toggleCurrent()
	}
	switch event.Rune() {
	case 'e', 'E':
		return toggleCurrent()
	case 'a', 'A':
		if expandAllFn != nil {
			expandAllFn()
		}
		return nil
	}
	return event
}
