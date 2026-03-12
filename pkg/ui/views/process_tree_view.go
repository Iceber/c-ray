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

// ProcessTreeView displays container processes as a tree
type ProcessTreeView struct {
	*tview.Flex

	app *tview.Application
	rt  runtime.Runtime
	ctx context.Context

	tree      *tview.TreeView
	statusBar *tview.TextView

	containerID string
	processes   []*models.Process
	mu          sync.Mutex
}

// NewProcessTreeView creates a new process tree view
func NewProcessTreeView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *ProcessTreeView {
	v := &ProcessTreeView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
		ctx:  ctx,
	}

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)

	root := tview.NewTreeNode("[gray]No data[-]")
	v.tree.SetRoot(root)

	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.updateStatusBar()
	return v
}

// SetContainer sets the container ID
func (v *ProcessTreeView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.mu.Unlock()
}

// Refresh loads and displays process tree data
func (v *ProcessTreeView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	id := v.containerID
	v.mu.Unlock()

	if id == "" {
		return nil
	}

	processes, err := v.rt.GetContainerProcesses(ctx, id)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.processes = processes
	v.mu.Unlock()

	v.render()
	return nil
}

// render builds and displays the process tree
func (v *ProcessTreeView) render() {
	v.mu.Lock()
	procs := make([]*models.Process, len(v.processes))
	copy(procs, v.processes)
	v.mu.Unlock()

	// Build a map for quick lookup
	procMap := make(map[int]*models.Process)
	for _, p := range procs {
		procMap[p.PID] = p
	}

	// Find root processes (those whose parent is not in our list)
	var roots []*models.Process
	for _, p := range procs {
		if _, hasParent := procMap[p.PPID]; !hasParent {
			roots = append(roots, p)
		}
	}
	sort.Slice(roots, func(i, j int) bool {
		return roots[i].PID < roots[j].PID
	})

	v.app.QueueUpdateDraw(func() {
		rootNode := tview.NewTreeNode("[aqua::b]Process Tree[-:-:-]").
			SetSelectable(false)

		for _, root := range roots {
			node := v.buildProcessNode(root)
			rootNode.AddChild(node)
		}

		v.tree.SetRoot(rootNode)
		v.tree.SetCurrentNode(rootNode)
		v.updateStatusBar()
	})
}

// buildProcessNode recursively builds a tree node for a process and its children
func (v *ProcessTreeView) buildProcessNode(p *models.Process) *tview.TreeNode {
	cmd := p.Command
	if len(p.Args) > 0 {
		cmd = p.Command + " " + strings.Join(p.Args, " ")
	}
	if len(cmd) > 80 {
		cmd = cmd[:77] + "..."
	}

	stateColor := "white"
	switch p.State {
	case "S":
		stateColor = "green"
	case "R":
		stateColor = "aqua"
	case "D":
		stateColor = "yellow"
	case "Z":
		stateColor = "red"
	case "T":
		stateColor = "gray"
	}

	label := fmt.Sprintf("[yellow]%d[white] [%s](%s)[-] %s", p.PID, stateColor, p.State, cmd)
	node := tview.NewTreeNode(label).
		SetSelectable(true).
		SetExpanded(true).
		SetReference(p)

	// Add resource info as a child node when expanded
	infoLabel := fmt.Sprintf("[gray]cpu:[white]%.1f%%  [gray]mem:[white]%.1f%%  [gray]rss:[white]%s",
		p.CPUPercent, p.MemoryPercent, formatBytes(int64(p.MemoryRSS)))
	if p.ReadBytesPerSec > 0 || p.WriteBytesPerSec > 0 {
		infoLabel += fmt.Sprintf("  [gray]r/s:[white]%s  [gray]w/s:[white]%s",
			formatRate(p.ReadBytesPerSec), formatRate(p.WriteBytesPerSec))
	}
	if p.ReadBytes > 0 || p.WriteBytes > 0 {
		infoLabel += fmt.Sprintf("  [gray]r:[white]%s  [gray]w:[white]%s",
			formatBytes(int64(p.ReadBytes)), formatBytes(int64(p.WriteBytes)))
	}
	infoNode := tview.NewTreeNode(infoLabel).
		SetSelectable(false)
	node.AddChild(infoNode)

	// Add children processes
	if len(p.Children) > 0 {
		children := make([]*models.Process, len(p.Children))
		copy(children, p.Children)
		sort.Slice(children, func(i, j int) bool {
			return children[i].PID < children[j].PID
		})
		for _, child := range children {
			childNode := v.buildProcessNode(child)
			node.AddChild(childNode)
		}
	}

	return node
}

// HandleInput processes key events
func (v *ProcessTreeView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	// Always allow Ctrl+C to propagate for global quit
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Key() {
	case tcell.KeyEnter:
		// Expand/collapse current node
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	}
	switch event.Rune() {
	case 'e', 'E':
		v.toggleExpand()
		return nil
	case 'a', 'A':
		v.expandAll()
		return nil
	}
	return event
}

// toggleExpand toggles the expansion of the currently selected node
func (v *ProcessTreeView) toggleExpand() {
	node := v.tree.GetCurrentNode()
	if node != nil {
		node.SetExpanded(!node.IsExpanded())
	}
}

// expandAll expands or collapses all nodes
func (v *ProcessTreeView) expandAll() {
	root := v.tree.GetRoot()
	if root == nil {
		return
	}
	// Check if most are expanded - if so, collapse; otherwise expand
	expanded := true
	root.Walk(func(node, parent *tview.TreeNode) bool {
		if !node.IsExpanded() && len(node.GetChildren()) > 0 {
			expanded = false
			return false
		}
		return true
	})

	target := !expanded
	root.Walk(func(node, parent *tview.TreeNode) bool {
		node.SetExpanded(target)
		return true
	})
}

// GetFocusPrimitive returns the focusable primitive (tree)
func (v *ProcessTreeView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

// updateStatusBar renders the status bar
func (v *ProcessTreeView) updateStatusBar() {
	v.mu.Lock()
	count := len(v.processes)
	v.mu.Unlock()

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Processes: [green]%d[white]  |  [yellow]e[white]:toggle expand  [yellow]a[white]:expand/collapse all  [yellow]Enter[white]:select",
		count,
	))
}
