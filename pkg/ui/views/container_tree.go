package views

import (
	"context"
	"fmt"
	"sort"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// ContainerTreeView displays containers in a tree structure with pods as parent nodes
type ContainerTreeView struct {
	*tview.Flex

	app        *tview.Application
	tree       *tview.TreeView
	statusBar  *tview.TextView
	rt         runtime.Runtime
	containers []*models.Container

	// Track selected container
	selectedContainerID string

	// Callback when a container is selected
	onSelect func(containerID string)
}

// TreeNodeData holds the data for each tree node
type TreeNodeData struct {
	NodeType    NodeType
	ContainerID string
	PodUID      string
}

// NodeType represents the type of tree node
type NodeType int

const (
	NodeTypePod NodeType = iota
	NodeTypeContainer
	NodeTypeStandaloneContainer
)

// podGroup represents a group of containers belonging to a pod
type podGroup struct {
	UID       string
	Name      string
	Namespace string
	Status    models.ContainerStatus
	Running   int
	Total     int
}

// NewContainerTreeView creates a new tree-based container list view
func NewContainerTreeView(app *tview.Application, rt runtime.Runtime) *ContainerTreeView {
	v := &ContainerTreeView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
	}

	// Create root node
	root := tview.NewTreeNode("Containers").
		SetColor(tcell.ColorAqua).
		SetSelectable(false)

	// Add loading indicator as initial state
	loadingNode := tview.NewTreeNode("[gray]Loading containers...[-]").
		SetSelectable(false)
	root.AddChild(loadingNode)

	v.tree = tview.NewTreeView().
		SetRoot(root).
		SetCurrentNode(root)

	v.tree.SetBorder(true).
		SetBorderColor(tcell.ColorDarkSlateGray).
		SetTitle(" [Containers] ").
		SetTitleColor(tcell.ColorAqua)

	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	// Handle selection
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if data, ok := node.GetReference().(*TreeNodeData); ok && data != nil {
			switch data.NodeType {
			case NodeTypeContainer, NodeTypeStandaloneContainer:
				v.selectedContainerID = data.ContainerID
				if v.onSelect != nil {
					v.onSelect(data.ContainerID)
				}
			case NodeTypePod:
				// Toggle expansion for pod nodes
				node.SetExpanded(!node.IsExpanded())
			}
		}
	})

	// Handle input for expand/collapse
	v.tree.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Always allow Ctrl+C to propagate for global quit
		if event.Key() == tcell.KeyCtrlC {
			return event
		}

		switch event.Rune() {
		case 'e', 'E':
			// Toggle expand/collapse for current node
			if node := v.tree.GetCurrentNode(); node != nil {
				node.SetExpanded(!node.IsExpanded())
			}
			return nil
		case 'a', 'A':
			// Expand/collapse all
			v.toggleAllNodes()
			return nil
		case 'r', 'R':
			// Refresh
			return event // Let parent handle refresh
		}

		return event
	})

	return v
}

// SetSelectedFunc sets the callback for container selection
func (v *ContainerTreeView) SetSelectedFunc(handler func(containerID string)) {
	v.onSelect = handler
}

// Refresh loads and displays container data
func (v *ContainerTreeView) Refresh(ctx context.Context) error {
	containers, err := v.rt.ListContainers(ctx)
	if err != nil {
		return err
	}

	// Save current selection before re-rendering
	savedData := v.getCurrentNodeData()

	v.containers = containers

	// UI updates must be done on the main thread
	if v.app != nil {
		v.app.QueueUpdateDraw(func() {
			v.render()
			v.restoreSelection(savedData)
		})
	} else {
		v.render()
		v.restoreSelection(savedData)
	}

	return nil
}

// getCurrentNodeData returns the TreeNodeData of the currently selected node
func (v *ContainerTreeView) getCurrentNodeData() *TreeNodeData {
	node := v.tree.GetCurrentNode()
	if node == nil {
		return nil
	}
	if data, ok := node.GetReference().(*TreeNodeData); ok {
		return data
	}
	return nil
}

// render updates the tree with current container data
func (v *ContainerTreeView) render() {
	root := v.tree.GetRoot()
	root.ClearChildren()

	// Group containers by pod
	podGroups := make(map[string]*podGroup)
	standaloneContainers := make([]*models.Container, 0)

	for _, c := range v.containers {
		if c.PodUID != "" {
			// Container belongs to a pod
			pg, exists := podGroups[c.PodUID]
			if !exists {
				pg = &podGroup{
					UID:       c.PodUID,
					Name:      c.PodName,
					Namespace: c.PodNamespace,
					Status:    models.ContainerStatusUnknown,
				}
				podGroups[c.PodUID] = pg
			}
			pg.Total++
			if c.Status == models.ContainerStatusRunning {
				pg.Running++
			}
			// Pod status is running if any container is running
			if c.Status == models.ContainerStatusRunning {
				pg.Status = models.ContainerStatusRunning
			}
		} else {
			// Standalone container
			standaloneContainers = append(standaloneContainers, c)
		}
	}

	// Sort pod groups by name
	podUIDs := make([]string, 0, len(podGroups))
	for uid := range podGroups {
		podUIDs = append(podUIDs, uid)
	}
	sort.Slice(podUIDs, func(i, j int) bool {
		return podGroups[podUIDs[i]].Name < podGroups[podUIDs[j]].Name
	})

	// Sort standalone containers by name
	sort.Slice(standaloneContainers, func(i, j int) bool {
		return standaloneContainers[i].Name < standaloneContainers[j].Name
	})

	// Add pod nodes with their containers
	for _, uid := range podUIDs {
		pg := podGroups[uid]
		podNode := v.createPodNode(pg)
		root.AddChild(podNode)

		// Find and add containers for this pod
		podContainers := v.getPodContainers(uid)
		for _, c := range podContainers {
			containerNode := v.createContainerNode(c, true)
			podNode.AddChild(containerNode)
		}
	}

	// Add standalone containers
	for _, c := range standaloneContainers {
		containerNode := v.createContainerNode(c, false)
		root.AddChild(containerNode)
	}

	// Expand all pods by default
	root.SetExpanded(true)
	for _, node := range root.GetChildren() {
		if data, ok := node.GetReference().(*TreeNodeData); ok && data.NodeType == NodeTypePod {
			node.SetExpanded(true)
		}
	}

	v.updateStatusBar(len(podGroups), len(standaloneContainers))
}

// createPodNode creates a tree node for a pod
func (v *ContainerTreeView) createPodNode(pg *podGroup) *tview.TreeNode {
	runningColor := tcell.ColorGreen
	if pg.Running == 0 {
		runningColor = tcell.ColorRed
	}

	// Format: [Pod] name (namespace) - running/total
	text := fmt.Sprintf("[::b][Pod][-] [::b]%s[-] [gray]%s[-]  [%s]%d[-]/[%s]%d[-]",
		pg.Name, pg.Namespace,
		getColorName(runningColor), pg.Running,
		getColorName(tcell.ColorWhite), pg.Total)

	node := tview.NewTreeNode(text).
		SetColor(tcell.ColorWhite).
		SetSelectable(true).
		SetExpanded(true)

	node.SetReference(&TreeNodeData{
		NodeType: NodeTypePod,
		PodUID:   pg.UID,
	})

	return node
}

// createContainerNode creates a tree node for a container
func (v *ContainerTreeView) createContainerNode(c *models.Container, isInPod bool) *tview.TreeNode {
	statusColor := v.getStatusColor(c.Status)

	// Short ID (12 chars)
	shortID := c.ID
	if len(shortID) > 12 {
		shortID = shortID[:12]
	}

	// Determine display name: if in pod and name equals short ID, show as "phase:<id>"
	// This identifies pause containers where the name is the container ID
	// Otherwise show as "<name>:<id>"
	var displayName string
	if isInPod && c.Name == shortID {
		displayName = fmt.Sprintf("phase:%s", shortID)
	} else {
		displayName = fmt.Sprintf("%s:%s", c.Name, shortID)
	}

	pid := fmt.Sprintf("%d", c.PID)
	if c.PID == 0 {
		pid = "-"
	}

	// Mark exited containers with [Exited] prefix
	var statusPrefix string
	if c.Status == models.ContainerStatusStopped {
		statusPrefix = "[red][Exited][-] "
	}

	var text string
	if isInPod {
		// Indented for pod children
		text = fmt.Sprintf("  ├─ [%s]%s %s [gray]%s[-]  [darkgray]PID:[-]%s",
			getColorName(statusColor), statusPrefix, displayName, c.Image, pid)
	} else {
		// No indent for standalone
		text = fmt.Sprintf("[%s]%s %s [gray]%s[-]  [darkgray]PID:[-]%s",
			getColorName(statusColor), statusPrefix, displayName, c.Image, pid)
	}

	node := tview.NewTreeNode(text).
		SetColor(tcell.ColorWhite).
		SetSelectable(true)

	nodeType := NodeTypeContainer
	if !isInPod {
		nodeType = NodeTypeStandaloneContainer
	}

	// Get PodUID for pod containers
	var podUID string
	if isInPod {
		podUID = c.PodUID
	}

	node.SetReference(&TreeNodeData{
		NodeType:    nodeType,
		ContainerID: c.ID,
		PodUID:      podUID,
	})

	return node
}

// getPodContainers returns all containers belonging to a specific pod
// Sorted by creation time (newest first) so that exited containers appear at the bottom
func (v *ContainerTreeView) getPodContainers(podUID string) []*models.Container {
	var result []*models.Container
	for _, c := range v.containers {
		if c.PodUID == podUID {
			result = append(result, c)
		}
	}
	// Sort by creation time (newest first, oldest last)
	// This puts exited containers (usually older) at the bottom
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.After(result[j].CreatedAt)
	})
	return result
}

// getStatusColor returns the color for a container status
func (v *ContainerTreeView) getStatusColor(status models.ContainerStatus) tcell.Color {
	switch status {
	case models.ContainerStatusRunning:
		return tcell.ColorGreen
	case models.ContainerStatusPaused:
		return tcell.ColorYellow
	case models.ContainerStatusStopped:
		return tcell.ColorRed
	case models.ContainerStatusCreated:
		return tcell.ColorDarkCyan
	default:
		return tcell.ColorWhite
	}
}

// getColorName returns the color name for use in tview text
func getColorName(color tcell.Color) string {
	switch color {
	case tcell.ColorGreen:
		return "green"
	case tcell.ColorYellow:
		return "yellow"
	case tcell.ColorRed:
		return "red"
	case tcell.ColorDarkCyan:
		return "darkcyan"
	case tcell.ColorAqua:
		return "aqua"
	case tcell.ColorGray:
		return "gray"
	case tcell.ColorDarkGray:
		return "darkgray"
	default:
		return "white"
	}
}

// toggleAllNodes expands or collapses all pod nodes
func (v *ContainerTreeView) toggleAllNodes() {
	root := v.tree.GetRoot()
	if len(root.GetChildren()) == 0 {
		return
	}

	// Check if any pod is collapsed
	anyCollapsed := false
	for _, node := range root.GetChildren() {
		if data, ok := node.GetReference().(*TreeNodeData); ok && data.NodeType == NodeTypePod {
			if !node.IsExpanded() {
				anyCollapsed = true
				break
			}
		}
	}

	// Expand all if any is collapsed, otherwise collapse all
	for _, node := range root.GetChildren() {
		if data, ok := node.GetReference().(*TreeNodeData); ok && data.NodeType == NodeTypePod {
			node.SetExpanded(anyCollapsed)
		}
	}
}

// restoreSelection attempts to restore the previous selection after refresh
func (v *ContainerTreeView) restoreSelection(savedData *TreeNodeData) {
	if savedData == nil {
		v.selectFirstNode()
		return
	}

	root := v.tree.GetRoot()

	switch savedData.NodeType {
	case NodeTypeContainer, NodeTypeStandaloneContainer:
		// Try to find the same container
		if node := v.findNodeByContainerID(root, savedData.ContainerID); node != nil {
			v.tree.SetCurrentNode(node)
			return
		}
		// Container not found, try to select its pod
		if savedData.PodUID != "" {
			if podNode := v.findNodeByPodUID(root, savedData.PodUID); podNode != nil {
				v.tree.SetCurrentNode(podNode)
				return
			}
		}
		// Fallback to first node
		v.selectFirstNode()

	case NodeTypePod:
		// Try to find the same pod
		if node := v.findNodeByPodUID(root, savedData.PodUID); node != nil {
			v.tree.SetCurrentNode(node)
			return
		}
		// Pod not found, fallback to first node
		v.selectFirstNode()

	default:
		v.selectFirstNode()
	}
}

// findNodeByContainerID finds a node by container ID
func (v *ContainerTreeView) findNodeByContainerID(root *tview.TreeNode, containerID string) *tview.TreeNode {
	for _, child := range root.GetChildren() {
		// Check if this is a standalone container
		if data, ok := child.GetReference().(*TreeNodeData); ok {
			if data.NodeType == NodeTypeStandaloneContainer && data.ContainerID == containerID {
				return child
			}
		}
		// Check children (pod's containers)
		for _, grandchild := range child.GetChildren() {
			if data, ok := grandchild.GetReference().(*TreeNodeData); ok {
				if data.NodeType == NodeTypeContainer && data.ContainerID == containerID {
					return grandchild
				}
			}
		}
	}
	return nil
}

// findNodeByPodUID finds a pod node by Pod UID
func (v *ContainerTreeView) findNodeByPodUID(root *tview.TreeNode, podUID string) *tview.TreeNode {
	for _, child := range root.GetChildren() {
		if data, ok := child.GetReference().(*TreeNodeData); ok {
			if data.NodeType == NodeTypePod && data.PodUID == podUID {
				return child
			}
		}
	}
	return nil
}

// selectFirstNode selects the first available node
func (v *ContainerTreeView) selectFirstNode() {
	root := v.tree.GetRoot()
	children := root.GetChildren()
	if len(children) > 0 {
		v.tree.SetCurrentNode(children[0])
	} else {
		v.tree.SetCurrentNode(root)
	}
}

// updateStatusBar updates the status bar text
func (v *ContainerTreeView) updateStatusBar(podCount, standaloneCount int) {
	total := len(v.containers)
	running := 0
	for _, c := range v.containers {
		if c.Status == models.ContainerStatusRunning {
			running++
		}
	}

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Pods:[green]%d[white]  Standalone:[green]%d[white]  Containers:[green]%d[white]/[green]%d[white] running  |  [yellow]e[white]:expand  [yellow]a[white]:expand-all  [yellow]Enter[white]:detail  [yellow]r[white]:refresh",
		podCount, standaloneCount, running, total,
	))
}

// GetSelectedContainerID returns the currently selected container ID
func (v *ContainerTreeView) GetSelectedContainerID() string {
	return v.selectedContainerID
}

// HandleInput processes key events
func (v *ContainerTreeView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	// Always allow Ctrl+C to propagate for global quit
	if event.Key() == tcell.KeyCtrlC {
		return event
	}

	switch event.Rune() {
	case 'r', 'R':
		// Trigger refresh - handled by parent
		return event
	}

	return event
}
