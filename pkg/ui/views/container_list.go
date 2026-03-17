package views

import (
	"context"
	"fmt"
	"sort"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// containerEntry bundles a handle with its pre-fetched info for rendering.
type containerEntry struct {
	handle runtime.Container
	info   *runtime.ContainerInfo
}

// ContainerTreeView displays containers grouped by pods.
type ContainerTreeView struct {
	*tview.Flex

	app       *tview.Application
	rt        runtime.Runtime
	tree      *tview.TreeView
	statusBar *tview.TextView
	entries   []containerEntry

	onSelect func(c runtime.Container)
}

type treeNodeData struct {
	nodeType    int // 0=pod, 1=container, 2=standalone
	container   runtime.Container
	containerID string
	podUID      string
}

const (
	nodeTypePod = iota
	nodeTypeContainer
	nodeTypeStandalone
)

// NewContainerTreeView creates a new tree-based container list view.
func NewContainerTreeView(app *tview.Application, rt runtime.Runtime) *ContainerTreeView {
	v := &ContainerTreeView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
	}

	root := tview.NewTreeNode("Containers").SetColor(tcell.ColorAqua).SetSelectable(false)
	root.AddChild(tview.NewTreeNode("[gray]Loading containers...[-]").SetSelectable(false))

	v.tree = tview.NewTreeView().SetRoot(root).SetCurrentNode(root)
	v.tree.SetBorder(true).SetBorderColor(tcell.ColorDarkSlateGray)
	v.tree.SetTitle(" [Containers] ").SetTitleColor(tcell.ColorAqua)

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if data, ok := node.GetReference().(*treeNodeData); ok && data != nil {
			switch data.nodeType {
			case nodeTypeContainer, nodeTypeStandalone:
				if v.onSelect != nil && data.container != nil {
					v.onSelect(data.container)
				}
			case nodeTypePod:
				node.SetExpanded(!node.IsExpanded())
			}
		}
	})

	v.tree.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			return event
		}
		switch event.Rune() {
		case 'e', 'E':
			if node := v.tree.GetCurrentNode(); node != nil {
				node.SetExpanded(!node.IsExpanded())
			}
			return nil
		case 'a', 'A':
			v.toggleAllNodes()
			return nil
		}
		return event
	})

	return v
}

// SetSelectedFunc sets the callback for container selection.
func (v *ContainerTreeView) SetSelectedFunc(handler func(c runtime.Container)) {
	v.onSelect = handler
}

// Refresh loads and displays container data.
func (v *ContainerTreeView) Refresh(ctx context.Context) error {
	containers, err := v.rt.ListContainers(ctx)
	if err != nil {
		return err
	}

	savedData := v.getCurrentNodeData()

	entries := make([]containerEntry, 0, len(containers))
	for _, c := range containers {
		info, err := c.Info(ctx)
		if err != nil {
			continue
		}
		entries = append(entries, containerEntry{handle: c, info: info})
	}

	v.entries = entries
	queueUpdateDraw(v.app, func() {
		v.render()
		v.restoreSelection(savedData)
	})
	return nil
}

func (v *ContainerTreeView) getCurrentNodeData() *treeNodeData {
	node := v.tree.GetCurrentNode()
	if node == nil {
		return nil
	}
	data, _ := node.GetReference().(*treeNodeData)
	return data
}

type podGroup struct {
	uid       string
	name      string
	namespace string
	running   int
	total     int
}

func (v *ContainerTreeView) render() {
	root := v.tree.GetRoot()
	root.ClearChildren()

	pods := make(map[string]*podGroup)
	var standalone []containerEntry

	for _, e := range v.entries {
		if e.info.PodUID != "" {
			pg, ok := pods[e.info.PodUID]
			if !ok {
				pg = &podGroup{
					uid:       e.info.PodUID,
					name:      e.info.PodName,
					namespace: e.info.PodNamespace,
				}
				pods[e.info.PodUID] = pg
			}
			pg.total++
			if e.info.Status == runtime.ContainerStatusRunning {
				pg.running++
			}
		} else {
			standalone = append(standalone, e)
		}
	}

	// Sort pod UIDs by name.
	podUIDs := make([]string, 0, len(pods))
	for uid := range pods {
		podUIDs = append(podUIDs, uid)
	}
	sort.Slice(podUIDs, func(i, j int) bool {
		return pods[podUIDs[i]].name < pods[podUIDs[j]].name
	})

	sort.Slice(standalone, func(i, j int) bool {
		return standalone[i].info.Name < standalone[j].info.Name
	})

	// Add pod nodes.
	for _, uid := range podUIDs {
		pg := pods[uid]
		podNode := v.createPodNode(pg)
		root.AddChild(podNode)

		podContainers := v.podContainers(uid)
		for _, e := range podContainers {
			podNode.AddChild(v.createContainerNode(e, true))
		}
	}

	// Add standalone containers.
	for _, e := range standalone {
		root.AddChild(v.createContainerNode(e, false))
	}

	root.SetExpanded(true)
	for _, node := range root.GetChildren() {
		if data, ok := node.GetReference().(*treeNodeData); ok && data.nodeType == nodeTypePod {
			node.SetExpanded(true)
		}
	}

	if v.tree.GetCurrentNode() == nil {
		v.selectFirstNode()
	}

	v.updateStatusBar(len(pods), len(standalone))
}

func (v *ContainerTreeView) podContainers(uid string) []containerEntry {
	var result []containerEntry
	for _, e := range v.entries {
		if e.info.PodUID == uid {
			result = append(result, e)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].info.Name < result[j].info.Name
	})
	return result
}

func (v *ContainerTreeView) createPodNode(pg *podGroup) *tview.TreeNode {
	runningColor := tcell.ColorGreen
	if pg.running == 0 {
		runningColor = tcell.ColorRed
	}
	text := fmt.Sprintf("[::b][Pod][-] [::b]%s[-] [gray]%s[-]  [%s]%d[-]/[%s]%d[-]",
		pg.name, pg.namespace,
		getColorName(runningColor), pg.running,
		getColorName(tcell.ColorWhite), pg.total,
	)
	node := tview.NewTreeNode(text).SetColor(tcell.ColorWhite).SetSelectable(true).SetExpanded(true)
	node.SetReference(&treeNodeData{nodeType: nodeTypePod, podUID: pg.uid})
	return node
}

func (v *ContainerTreeView) createContainerNode(e containerEntry, isInPod bool) *tview.TreeNode {
	statusColor := containerStatusColor(e.info.Status)
	sid := shortID(e.info.ID)
	pid := "-"
	if e.info.PID > 0 {
		pid = fmt.Sprintf("%d", e.info.PID)
	}
	age := formatAge(e.info.CreatedAt)

	displayName := e.info.Name
	if displayName == "" {
		displayName = sid
	}

	nodeType := nodeTypeStandalone
	if isInPod {
		nodeType = nodeTypeContainer
	}

	text := fmt.Sprintf("[%s]●[-] %s [gray](%s)[-] [gray]PID[-]:%s [gray]Age[-]:%s",
		getColorName(statusColor), displayName, sid, pid, age)

	node := tview.NewTreeNode(text).SetSelectable(true)
	node.SetReference(&treeNodeData{nodeType: nodeType, container: e.handle, containerID: e.info.ID, podUID: e.info.PodUID})
	return node
}

func (v *ContainerTreeView) toggleAllNodes() {
	root := v.tree.GetRoot()
	if root == nil {
		return
	}
	expanded := true
	for _, child := range root.GetChildren() {
		if !child.IsExpanded() {
			expanded = false
			break
		}
	}
	for _, child := range root.GetChildren() {
		child.SetExpanded(!expanded)
	}
}

func (v *ContainerTreeView) restoreSelection(savedData *treeNodeData) {
	if savedData == nil {
		v.selectFirstNode()
		return
	}

	root := v.tree.GetRoot()
	if root == nil {
		return
	}

	switch savedData.nodeType {
	case nodeTypeContainer, nodeTypeStandalone:
		if node := v.findNodeByContainerID(root, savedData.containerID); node != nil {
			v.tree.SetCurrentNode(node)
			return
		}
		if savedData.podUID != "" {
			if node := v.findNodeByPodUID(root, savedData.podUID); node != nil {
				v.tree.SetCurrentNode(node)
				return
			}
		}
	case nodeTypePod:
		if node := v.findNodeByPodUID(root, savedData.podUID); node != nil {
			v.tree.SetCurrentNode(node)
			return
		}
	}

	v.selectFirstNode()
}

func (v *ContainerTreeView) findNodeByContainerID(root *tview.TreeNode, targetID string) *tview.TreeNode {
	if targetID == "" {
		return nil
	}
	for _, child := range root.GetChildren() {
		if data, ok := child.GetReference().(*treeNodeData); ok && data != nil {
			if data.nodeType == nodeTypeStandalone && data.containerID == targetID {
				return child
			}
		}
		for _, grandchild := range child.GetChildren() {
			if data, ok := grandchild.GetReference().(*treeNodeData); ok && data != nil {
				if data.nodeType == nodeTypeContainer && data.containerID == targetID {
					return grandchild
				}
			}
		}
	}
	return nil
}

func (v *ContainerTreeView) findNodeByPodUID(root *tview.TreeNode, podUID string) *tview.TreeNode {
	for _, child := range root.GetChildren() {
		if data, ok := child.GetReference().(*treeNodeData); ok && data != nil {
			if data.nodeType == nodeTypePod && data.podUID == podUID {
				return child
			}
		}
	}
	return nil
}

func (v *ContainerTreeView) selectFirstNode() {
	root := v.tree.GetRoot()
	if root == nil {
		return
	}
	children := root.GetChildren()
	if len(children) > 0 {
		v.tree.SetCurrentNode(children[0])
		return
	}
	v.tree.SetCurrentNode(root)
}

func (v *ContainerTreeView) updateStatusBar(podCount, standaloneCount int) {
	total := len(v.entries)
	running := 0
	for _, e := range v.entries {
		if e.info.Status == runtime.ContainerStatusRunning {
			running++
		}
	}
	v.statusBar.SetText(fmt.Sprintf(
		" [white]Containers: [green]%d[white] total, [green]%d[white] running, [aqua]%d[white] pods, [gray]%d[white] standalone  |  [yellow]Enter[white]:detail  [yellow]e[white]:toggle  [yellow]a[white]:all  [yellow]r[white]:refresh",
		total, running, podCount, standaloneCount,
	))
}

// GetFocusPrimitive returns the inner tree that should receive keyboard focus.
func (v *ContainerTreeView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

func containerStatusColor(status runtime.ContainerStatus) tcell.Color {
	switch status {
	case runtime.ContainerStatusRunning:
		return tcell.ColorGreen
	case runtime.ContainerStatusPaused:
		return tcell.ColorYellow
	case runtime.ContainerStatusStopped:
		return tcell.ColorRed
	case runtime.ContainerStatusCreated:
		return tcell.ColorDarkCyan
	default:
		return tcell.ColorGray
	}
}
