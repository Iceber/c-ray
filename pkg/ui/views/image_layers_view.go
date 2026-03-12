package views

import (
	"context"
	"fmt"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// ImageLayersView displays image layer information for a container
type ImageLayersView struct {
	*tview.Flex

	app *tview.Application
	rt  runtime.Runtime
	ctx context.Context

	tree      *tview.TreeView
	statusBar *tview.TextView

	containerID string
	detail      *models.ContainerDetail
	layers      []*models.ImageLayer
	configInfo  *models.ImageConfigInfo
	mu          sync.Mutex
}

// NewImageLayersView creates a new image layers view
func NewImageLayersView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *ImageLayersView {
	v := &ImageLayersView{
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

	// Set up selection handler for Enter key
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

// SetContainer sets the container ID
func (v *ImageLayersView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.mu.Unlock()
}

// SetDetail sets the container detail
func (v *ImageLayersView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
}

// Refresh loads and displays image layer data
func (v *ImageLayersView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	detail := v.detail
	v.mu.Unlock()

	if detail == nil || detail.Image == "" {
		return nil
	}

	// Get image config info
	configInfo, err := v.rt.GetImageConfigInfo(ctx, detail.Image)
	if err == nil {
		v.mu.Lock()
		v.configInfo = configInfo
		v.mu.Unlock()
	}

	// Get layers with snapshot info (pass snapshotter and RW key from container detail)
	layers, err := v.rt.GetImageLayers(ctx, detail.Image, detail.Snapshotter, detail.SnapshotKey)
	if err != nil {
		v.mu.Lock()
		v.layers = nil
		v.mu.Unlock()
		v.render()
		return nil
	}

	v.mu.Lock()
	v.layers = layers
	v.mu.Unlock()

	v.render()
	return nil
}

// render builds and displays the image layers tree
func (v *ImageLayersView) render() {
	v.mu.Lock()
	detail := v.detail
	layers := v.layers
	configInfo := v.configInfo
	v.mu.Unlock()

	v.app.QueueUpdateDraw(func() {
		rootNode := tview.NewTreeNode("[aqua::b]Image Layers[-:-:-]").
			SetSelectable(false).
			SetExpanded(true)

		// 1. Image Config Section
		if configInfo != nil {
			configNode := v.createConfigNode(configInfo)
			rootNode.AddChild(configNode)
		}

		// 2. RW Layer Section
		if detail != nil && detail.SnapshotKey != "" {
			rwNode := v.createRWNode(detail)
			rootNode.AddChild(rwNode)
		}

		// 3. Read-Only Layers Section (top to base)
		if len(layers) > 0 {
			roNode := v.createReadOnlyLayersNode(layers)
			rootNode.AddChild(roNode)
		}

		v.tree.SetRoot(rootNode)
		v.tree.SetCurrentNode(rootNode)
		v.updateStatusBar()
	})
}

// createConfigNode creates the Image Config info node
func (v *ImageLayersView) createConfigNode(config *models.ImageConfigInfo) *tview.TreeNode {
	configNode := tview.NewTreeNode("[aqua::b]Image Config[-:-:-]").
		SetSelectable(true).
		SetExpanded(true)

	digest := truncateDigest(config.Digest, 19)
	configNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]Digest: [white]%s", digest)).SetSelectable(false))
	configNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]Content Path: [white]%s", config.ContentPath)).SetSelectable(false))
	configNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]Size: [white]%s", formatBytes(config.Size))).SetSelectable(false))

	return configNode
}

// createRWNode creates the RW Layer node
func (v *ImageLayersView) createRWNode(detail *models.ContainerDetail) *tview.TreeNode {
	rwNode := tview.NewTreeNode("[yellow::b]RW Layer[-:-:-] (active snapshot)").
		SetSelectable(true).
		SetExpanded(true)

	rwNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]Snapshot Key: [white]%s", detail.SnapshotKey)).SetSelectable(false))

	if detail.WritableLayerPath != "" {
		rwNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]Path: [darkgray]%s", detail.WritableLayerPath)).SetSelectable(false))
	}

	// Show usage information if available
	if detail.RWLayerUsage > 0 {
		rwNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]Usage Size: [white]%s", formatBytes(detail.RWLayerUsage))).SetSelectable(false))
		rwNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]Usage Inodes: [white]%d", detail.RWLayerInodes)).SetSelectable(false))
	}

	return rwNode
}

// createReadOnlyLayersNode creates the Read-Only Layers section
// layers are ordered from base (0) to top (n-1)
// Display from top to base (reverse order)
func (v *ImageLayersView) createReadOnlyLayersNode(layers []*models.ImageLayer) *tview.TreeNode {
	roNode := tview.NewTreeNode(fmt.Sprintf("[green::b]Read-Only Layers[-:-:-] (%d layers, top to base)", len(layers))).
		SetSelectable(true).
		SetExpanded(true)

	totalLayers := len(layers)
	// Display from top to base (reverse order)
	for i := totalLayers - 1; i >= 0; i-- {
		layer := layers[i]
		layerNode := v.createLayerNode(layer, totalLayers)
		roNode.AddChild(layerNode)
	}

	return roNode
}

// createLayerNode creates a single layer node with all details
// layer.Index is 0 for base, (total-1) for top
func (v *ImageLayersView) createLayerNode(layer *models.ImageLayer, totalLayers int) *tview.TreeNode {
	snapshotKey := layer.SnapshotKey
	if snapshotKey == "" {
		snapshotKey = "<empty>"
	}

	// Layer number: Index 0=base, Index (n-1)=top
	// Display number matches Index directly (base=0, top=n-1)
	layerNum := layer.Index

	shortKey := truncateDigest(snapshotKey, 19)
	node := tview.NewTreeNode(fmt.Sprintf("[white]Layer %d/%d: [gray]%s",
		layerNum, totalLayers, shortKey)).
		SetSelectable(true).
		SetExpanded(false)

	// Add layer details as child nodes
	node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Snapshot Key: [white]%s", snapshotKey)).SetSelectable(false))
	if layer.SnapshotPath != "" {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Snapshot Path: [darkcyan]%s", layer.SnapshotPath)).SetSelectable(false))
	}
	node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  RootFS Diff ID: [white]%s", layer.UncompressedDigest)).SetSelectable(false))
	node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Content Path: [white]%s", layer.ContentPath)).SetSelectable(false))

	// Content Size with compression type
	contentSizeStr := formatContentSize(layer.Size, layer.CompressionType)
	node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Content Size: [white]%s", contentSizeStr)).SetSelectable(false))

	// Disk Usage (only if snapshot exists and has usage data)
	if layer.SnapshotExists && layer.UsageSize > 0 {
		node.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Disk Usage: [white]%s (%d inodes)", formatBytes(layer.UsageSize), layer.UsageInodes)).SetSelectable(false))
	}

	// Show status if not unpacked
	if !layer.SnapshotExists {
		node.AddChild(tview.NewTreeNode("[red]  Status: not unpacked[-]").SetSelectable(false))
	}

	return node
}

// truncateDigest truncates a digest string for display
func truncateDigest(digest string, length int) string {
	if len(digest) <= length {
		return digest
	}
	return digest[:length]
}

// formatContentSize formats the content size with compression type
// Format: <size>(<compression>) or <size> if no compression
func formatContentSize(size int64, compression string) string {
	sizeStr := formatBytes(size)
	if compression == "" {
		return sizeStr + "(-)"
	}
	return fmt.Sprintf("%s(%s)", sizeStr, compression)
}

// HandleInput processes key events
func (v *ImageLayersView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
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
		if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	}
	return event
}

// GetFocusPrimitive returns the focusable primitive (tree)
func (v *ImageLayersView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

// updateStatusBar renders the status bar
func (v *ImageLayersView) updateStatusBar() {
	v.mu.Lock()
	count := len(v.layers)
	v.mu.Unlock()

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Layers: [green]%d[white]  |  [yellow]e[white]:toggle expand  [yellow]Enter[white]:select",
		count,
	))
}
