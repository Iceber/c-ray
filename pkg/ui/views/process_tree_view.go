package views

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// ProcessTreeView displays container processes as a tree.
type ProcessTreeView struct {
	*tview.Flex

	app       *tview.Application
	tree      *tview.TreeView
	statusBar *tview.TextView
	container runtime.Container
	processes []*runtime.Process
	mu        sync.Mutex
}

// NewProcessTreeView creates a new process tree view.
func NewProcessTreeView(app *tview.Application) *ProcessTreeView {
	v := &ProcessTreeView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.tree.SetRoot(tview.NewTreeNode("[gray]No data[-]"))

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the container handle.
func (v *ProcessTreeView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.processes = nil
	v.mu.Unlock()
}

// Refresh loads and displays process tree data.
func (v *ProcessTreeView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		return nil
	}

	processes, err := c.Processes(ctx)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.processes = processes
	v.mu.Unlock()

	v.render(ctx)
	return nil
}

func (v *ProcessTreeView) render(ctx context.Context) {
	v.mu.Lock()
	procs := make([]*runtime.Process, len(v.processes))
	copy(procs, v.processes)
	c := v.container
	v.mu.Unlock()

	// Build parent map for tree.
	procMap := make(map[int]*runtime.Process)
	for _, p := range procs {
		procMap[p.PID] = p
	}
	var roots []*runtime.Process
	for _, p := range procs {
		if _, hasParent := procMap[p.PPID]; !hasParent {
			roots = append(roots, p)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].PID < roots[j].PID })

	// Get shim info for root label.
	rootLabel := processTreeRootLabel(c, ctx)

	// Build child map.
	childMap := make(map[int][]*runtime.Process)
	for _, p := range procs {
		childMap[p.PPID] = append(childMap[p.PPID], p)
	}

	queueUpdateDraw(v.app, func() {
		rootNode := tview.NewTreeNode(rootLabel).SetSelectable(true).SetExpanded(true)
		for _, root := range roots {
			rootNode.AddChild(buildProcessNode(root, childMap))
		}
		v.tree.SetRoot(rootNode)
		v.tree.SetCurrentNode(rootNode)
		v.updateStatusBar()
	})
}

func buildProcessNode(p *runtime.Process, childMap map[int][]*runtime.Process) *tview.TreeNode {
	stateColor := processStateColor(p.State)
	label := fmt.Sprintf("[white::b]%s[-:-:-] [gray][pid:%d][-] [%s](%s)[-] %s",
		processName(p), p.PID, stateColor, p.State, processCmd(p))

	node := tview.NewTreeNode(label).SetSelectable(true).SetExpanded(true).SetReference(p)

	children := childMap[p.PID]
	if len(children) > 0 {
		sort.Slice(children, func(i, j int) bool { return children[i].PID < children[j].PID })
		for _, childNode := range buildChildNodes(children, childMap) {
			node.AddChild(childNode)
		}
	}
	return node
}

func buildChildNodes(children []*runtime.Process, childMap map[int][]*runtime.Process) []*tview.TreeNode {
	groups := make(map[string][]*runtime.Process)
	var order []string
	for _, child := range children {
		key := processGroupKey(child)
		if _, ok := groups[key]; !ok {
			order = append(order, key)
		}
		groups[key] = append(groups[key], child)
	}

	var nodes []*tview.TreeNode
	for _, key := range order {
		group := groups[key]
		if len(group) == 1 {
			nodes = append(nodes, buildProcessNode(group[0], childMap))
			continue
		}
		agg := tview.NewTreeNode(fmt.Sprintf("[aqua]%d ×[-] %s [gray][same command][-]", len(group), processCmd(group[0]))).
			SetSelectable(true).SetExpanded(false)
		for _, child := range group {
			agg.AddChild(buildProcessNode(child, childMap))
		}
		nodes = append(nodes, agg)
	}
	return nodes
}

// HandleInput processes key events.
func (v *ProcessTreeView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
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

func (v *ProcessTreeView) expandAll() {
	root := v.tree.GetRoot()
	if root == nil {
		return
	}
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

// GetFocusPrimitive returns the focusable primitive.
func (v *ProcessTreeView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

func (v *ProcessTreeView) updateStatusBar() {
	v.mu.Lock()
	count := len(v.processes)
	v.mu.Unlock()
	v.statusBar.SetText(fmt.Sprintf(
		" [white]Tree: [green]%d[white]  |  [aqua]root[-]: shim -> container process tree  |  [yellow]e[white]:toggle expand  [yellow]a[white]:expand/collapse all",
		count,
	))
}

func processTreeRootLabel(c runtime.Container, ctx context.Context) string {
	shimName := "containerd-shim"
	shimPID := uint32(0)
	if c != nil {
		if rt, err := c.Runtime(ctx); err == nil && rt != nil {
			if rt.Shim != nil && rt.Shim.BinaryPath != "" {
				shimName = filepath.Base(rt.Shim.BinaryPath)
			}
			if rt.Conmon != nil && rt.Conmon.PID > 0 {
				shimPID = rt.Conmon.PID
				shimName = filepath.Base(rt.Conmon.BinaryPath)
			}
		}
		if state, err := c.State(ctx); err == nil && state != nil && state.PPID > 0 {
			shimPID = state.PPID
		}
	}
	if shimPID > 0 {
		return fmt.Sprintf("[aqua::b]Shim[-:-:-] [gray](%s, not in container)[-] [white][pid:%d][-]", shimName, shimPID)
	}
	return fmt.Sprintf("[aqua::b]Shim[-:-:-] [gray](%s, not in container)[-]", shimName)
}

func processName(p *runtime.Process) string {
	if p.Command != "" {
		return filepath.Base(p.Command)
	}
	if len(p.Args) > 0 {
		return filepath.Base(p.Args[0])
	}
	return fmt.Sprintf("pid-%d", p.PID)
}

func processCmd(p *runtime.Process) string {
	var parts []string
	if p.Command != "" {
		parts = append(parts, p.Command)
	}
	if len(p.Args) > 0 {
		parts = append(parts, strings.Join(p.Args, " "))
	}
	cmd := strings.TrimSpace(strings.Join(parts, " "))
	if cmd == "" {
		cmd = processName(p)
	}
	return truncateForCard(cmd, 72)
}

func processGroupKey(p *runtime.Process) string {
	return processName(p) + "\x00" + processCmd(p)
}

func processStateColor(state string) string {
	switch state {
	case "S":
		return "green"
	case "R":
		return "aqua"
	case "D":
		return "yellow"
	case "Z":
		return "red"
	case "T":
		return "gray"
	default:
		return "white"
	}
}
