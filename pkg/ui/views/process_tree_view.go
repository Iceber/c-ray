package views

import (
	"context"
	"fmt"
	"path/filepath"
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
	detail      *models.ContainerDetail
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

// SetDetail sets container detail used for shim context.
func (v *ProcessTreeView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
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
	detail := v.detail
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

	queueUpdateDraw(v.app, func() {
		rootNode := tview.NewTreeNode(processTreeRootLabel(detail)).
			SetSelectable(true).
			SetExpanded(true)

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

	label := fmt.Sprintf(
		"[white::b]%s[-:-:-] [gray][pid:%d][-] [%s](%s)[-] %s",
		processDisplayName(p),
		p.PID,
		stateColor,
		p.State,
		processCommandSummary(p),
	)
	node := tview.NewTreeNode(label).
		SetSelectable(true).
		SetExpanded(true).
		SetReference(p)

	// Add children processes
	if len(p.Children) > 0 {
		children := make([]*models.Process, len(p.Children))
		copy(children, p.Children)
		sort.Slice(children, func(i, j int) bool {
			return children[i].PID < children[j].PID
		})
		for _, childNode := range v.buildChildNodes(children) {
			node.AddChild(childNode)
		}
	}

	return node
}

func (v *ProcessTreeView) buildChildNodes(children []*models.Process) []*tview.TreeNode {
	groups := make(map[string][]*models.Process)
	order := make([]string, 0)
	for _, child := range children {
		key := processGroupKey(child)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], child)
	}

	nodes := make([]*tview.TreeNode, 0, len(order))
	for _, key := range order {
		group := groups[key]
		if len(group) == 1 {
			nodes = append(nodes, v.buildProcessNode(group[0]))
			continue
		}

		agg := tview.NewTreeNode(fmt.Sprintf("[aqua]%d ×[-] %s [gray][same command][-]", len(group), processCommandSummary(group[0]))).
			SetSelectable(true).
			SetExpanded(false)
		for _, child := range group {
			agg.AddChild(v.buildProcessNode(child))
		}
		nodes = append(nodes, agg)
	}
	return nodes
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

// SnapshotProcessCount returns a thread-safe count of processes.
// This method is designed to be called from other views without breaking encapsulation.
func (v *ProcessTreeView) SnapshotProcessCount() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.processes)
}

// updateStatusBar renders the status bar
func (v *ProcessTreeView) updateStatusBar() {
	v.mu.Lock()
	count := len(v.processes)
	v.mu.Unlock()

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Tree: [green]%d[white]  |  [aqua]root[-]: shim -> container process tree  |  [yellow]e[white]:toggle expand  [yellow]a[white]:expand/collapse all",
		count,
	))
}

func processTreeRootLabel(detail *models.ContainerDetail) string {
	shimName := "containerd-shim"
	shimPID := uint32(0)
	if detail != nil {
		shimPID = detail.ShimPID
		if detail.RuntimeProfile != nil && detail.RuntimeProfile.Shim != nil && detail.RuntimeProfile.Shim.BinaryPath != "" {
			shimName = filepath.Base(detail.RuntimeProfile.Shim.BinaryPath)
		}
	}
	if shimPID > 0 {
		return fmt.Sprintf("[aqua::b]Shim[-:-:-] [gray](%s, not in container)[-] [white][pid:%d][-]", shimName, shimPID)
	}
	return fmt.Sprintf("[aqua::b]Shim[-:-:-] [gray](%s, not in container)[-]", shimName)
}

func processDisplayName(process *models.Process) string {
	if process.Command != "" {
		return filepath.Base(process.Command)
	}
	if len(process.Args) > 0 {
		return filepath.Base(process.Args[0])
	}
	return fmt.Sprintf("pid-%d", process.PID)
}

func processCommandSummary(process *models.Process) string {
	parts := []string{}
	if process.Command != "" {
		parts = append(parts, process.Command)
	}
	if len(process.Args) > 0 {
		parts = append(parts, strings.Join(process.Args, " "))
	}
	cmd := strings.TrimSpace(strings.Join(parts, " "))
	if cmd == "" {
		cmd = processDisplayName(process)
	}
	return truncateForCard(cmd, 72)
}

func processGroupKey(process *models.Process) string {
	return processDisplayName(process) + "\x00" + processCommandSummary(process)
}
