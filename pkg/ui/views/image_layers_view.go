package views

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// ImageLayersView renders the Rootfs Layers subpage.
type ImageLayersView struct {
	*tview.Flex

	app         *tview.Application
	ctx         context.Context
	header      *tview.TextView
	body        *tview.Flex
	tree        *tview.TreeView
	browser     *tview.Flex
	browserInfo *tview.TextView
	browserTree *tview.TreeView
	preview     *tview.TextView
	statusBar   *tview.TextView

	container   runtime.Container
	storage     *runtime.ContainerStorage
	rwStats     *runtime.ContainerRWLayerStats
	config      *runtime.ContainerConfig
	runtime     *runtime.RuntimeProfile
	imageConfig *runtime.ImageConfigInfo
	lastError   error
	browserOpen bool
	browserRoot string
	focusPane   int // 0=tree, 1=browser
	mu          sync.Mutex
}

type layerBrowserEntry struct {
	path   string
	isDir  bool
	loaded bool
}

// NewImageLayersView creates a new Rootfs Layers view.
func NewImageLayersView(app *tview.Application, ctx context.Context) *ImageLayersView {
	v := &ImageLayersView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		ctx:  ctx,
	}

	v.header = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	v.header.SetBorder(true).SetBorderColor(tcell.ColorDarkCyan).SetTitle(" Rootfs Context ")

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.tree.SetRoot(tview.NewTreeNode("[gray]No rootfs layer data[-]").SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})

	v.browserInfo = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	v.browserInfo.SetBorder(true).SetBorderColor(tcell.ColorDarkCyan).SetTitle(" Layer Browser ")
	v.browserInfo.SetText(" [gray]Select a layer and press i to inspect its snapshot path[-]")

	v.browserTree = tview.NewTreeView()
	v.browserTree.SetBorder(false)
	v.browserTree.SetGraphics(true)
	v.browserTree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.browserTree.SetRoot(tview.NewTreeNode("[gray]No layer browser data[-]").SetSelectable(false))
	v.browserTree.SetSelectedFunc(func(node *tview.TreeNode) { v.toggleBrowserNode(node) })
	v.browserTree.SetChangedFunc(func(node *tview.TreeNode) { v.renderPreview(node) })

	v.preview = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.preview.SetBorder(true).SetBorderColor(tcell.ColorDarkCyan).SetTitle(" Preview ")
	v.preview.SetText(" [gray]No file selected[-]")

	v.browser = tview.NewFlex().SetDirection(tview.FlexRow)
	v.browser.AddItem(v.browserInfo, 3, 0, false)
	v.browser.AddItem(v.browserTree, 0, 2, true)
	v.browser.AddItem(v.preview, 0, 3, false)

	v.body = tview.NewFlex().SetDirection(tview.FlexColumn)

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.header, 4, 0, false)
	v.Flex.AddItem(v.body, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.refreshBodyLayout()
	v.render()
	v.updateStatusBar()
	return v
}

// SetContainer sets the container handle.
func (v *ImageLayersView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.storage = nil
	v.rwStats = nil
	v.config = nil
	v.runtime = nil
	v.imageConfig = nil
	v.lastError = nil
	v.browserOpen = false
	v.browserRoot = ""
	v.focusPane = 0
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
}

// Refresh loads storage and related data from the container handle.
func (v *ImageLayersView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.render()
		return nil
	}

	storage, err := c.Storage(ctx)
	if err != nil {
		v.mu.Lock()
		v.lastError = err
		v.storage = nil
		v.mu.Unlock()
		v.render()
		return err
	}

	config, _ := c.Config(ctx)
	profile, _ := c.Runtime(ctx)
	rwStats, _ := c.RWLayerStats(ctx)

	var imgConfig *runtime.ImageConfigInfo
	if img, err := c.Image(ctx); err == nil && img != nil {
		imgConfig, _ = img.Config(ctx)
	}

	v.mu.Lock()
	v.storage = storage
	v.config = config
	v.runtime = profile
	v.rwStats = &rwStats
	v.imageConfig = imgConfig
	v.lastError = nil
	v.mu.Unlock()

	v.render()
	v.updateStatusBar()
	return nil
}

// HandleInput processes tree interaction.
func (v *ImageLayersView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil || event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Key() {
	case tcell.KeyEnter:
		if v.focusPane == 1 {
			v.toggleBrowserNode(v.browserTree.GetCurrentNode())
		} else if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	}
	switch event.Rune() {
	case 'i', 'I':
		if v.browserOpen {
			v.closeBrowser()
		} else {
			v.openBrowserFromSelection()
		}
		return nil
	case 'e', 'E':
		if v.focusPane == 1 {
			v.toggleBrowserNode(v.browserTree.GetCurrentNode())
		} else if node := v.tree.GetCurrentNode(); node != nil {
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
func (v *ImageLayersView) GetFocusPrimitive() tview.Primitive {
	if v.browserOpen && v.focusPane == 1 {
		return v.browserTree
	}
	return v.tree
}

func (v *ImageLayersView) render() {
	v.mu.Lock()
	storage := v.storage
	config := v.config
	runtime := v.runtime
	rwStats := v.rwStats
	imgConfig := v.imageConfig
	lastError := v.lastError
	v.mu.Unlock()

	v.header.SetText(buildLayerHeaderV1(config, runtime, imgConfig))

	root := tview.NewTreeNode("[aqua::b]Rootfs Layers[-:-:-]").SetSelectable(false).SetExpanded(true)
	if lastError != nil {
		root.AddChild(tview.NewTreeNode("[red]Failed to load layers: " + lastError.Error() + "[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		v.refreshBodyLayout()
		return
	}
	if storage == nil {
		root.AddChild(tview.NewTreeNode("[gray]Refresh to resolve snapshotter, rootfs path and image layers[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		v.refreshBodyLayout()
		return
	}

	root.AddChild(buildRWLayerNodeV1(config, rwStats, storage.RWLayerPath))
	root.AddChild(buildReadOnlyLayersNodeV1(storage.ReadOnlyLayers))

	v.tree.SetRoot(root)
	v.tree.SetCurrentNode(root)
	v.refreshBodyLayout()
}

func (v *ImageLayersView) expandAll() {
	target := v.tree
	if v.focusPane == 1 {
		target = v.browserTree
	}
	root := target.GetRoot()
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
	target.SetCurrentNode(root)
}

func (v *ImageLayersView) updateStatusBar() {
	if v.browserOpen {
		v.statusBar.SetText(" [white]Rootfs Layers:[-] browser open  |  [yellow]i[white]:close browser  [yellow]e/Enter[white]:toggle  [yellow]a[white]:expand/collapse all")
		return
	}
	v.statusBar.SetText(" [white]Rootfs Layers:[-] rw layer and read-only layers  |  [yellow]i[white]:browse layer  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse all")
}

func (v *ImageLayersView) refreshBodyLayout() {
	v.body.Clear()
	v.body.AddItem(v.tree, 0, 3, !v.browserOpen)
	if v.browserOpen {
		v.body.AddItem(v.browser, 0, 2, true)
	}
}

// --- Browser ---

func (v *ImageLayersView) openBrowserFromSelection() {
	node := v.tree.GetCurrentNode()
	path, title := selectedLayerPath(node, v.config, v.storage)
	if strings.TrimSpace(path) == "" {
		v.statusBar.SetText(" [white]Rootfs Layers:[-] select a layer with a readable path before opening the browser")
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		v.statusBar.SetText(fmt.Sprintf(" [white]Rootfs Layers:[-] unable to inspect: %v", err))
		return
	}

	v.mu.Lock()
	v.browserOpen = true
	v.browserRoot = path
	v.focusPane = 1
	v.mu.Unlock()

	v.browserInfo.SetText(fmt.Sprintf(" [gray]Layer:[-] [white]%s[-]\n [gray]Path:[-] [white]%s[-]", title, path))
	v.initBrowserTree(path, info)
	v.refreshBodyLayout()
	if v.app != nil {
		v.app.SetFocus(v.browserTree)
	}
	v.updateStatusBar()
}

func (v *ImageLayersView) closeBrowser() {
	v.mu.Lock()
	v.browserOpen = false
	v.browserRoot = ""
	v.focusPane = 0
	v.mu.Unlock()
	v.browserInfo.SetText(" [gray]Select a layer and press i to inspect its snapshot path[-]")
	v.preview.SetText(" [gray]No file selected[-]")
	v.browserTree.SetRoot(tview.NewTreeNode("[gray]No layer browser data[-]").SetSelectable(false))
	v.refreshBodyLayout()
	if v.app != nil {
		v.app.SetFocus(v.tree)
	}
	v.updateStatusBar()
}

func (v *ImageLayersView) initBrowserTree(path string, info os.FileInfo) {
	entry := &layerBrowserEntry{path: path, isDir: info.IsDir()}
	rootText := filepath.Base(path)
	if rootText == "" || rootText == "." {
		rootText = path
	}
	root := tview.NewTreeNode(rootText).SetSelectable(true).SetExpanded(true).SetReference(entry)
	if entry.isDir {
		v.loadBrowserChildren(root, entry)
	}
	v.browserTree.SetRoot(root)
	v.browserTree.SetCurrentNode(root)
	v.renderPreview(root)
}

func (v *ImageLayersView) toggleBrowserNode(node *tview.TreeNode) {
	if node == nil {
		return
	}
	entry, _ := node.GetReference().(*layerBrowserEntry)
	if entry == nil || !entry.isDir {
		v.renderPreview(node)
		return
	}
	v.loadBrowserChildren(node, entry)
	node.SetExpanded(!node.IsExpanded())
	v.renderPreview(node)
}

func (v *ImageLayersView) loadBrowserChildren(node *tview.TreeNode, entry *layerBrowserEntry) {
	if node == nil || entry == nil || !entry.isDir || entry.loaded {
		return
	}
	node.ClearChildren()
	entries, err := os.ReadDir(entry.path)
	if err != nil {
		node.AddChild(tview.NewTreeNode("[red]" + err.Error() + "[-]").SetSelectable(false))
		entry.loaded = true
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	for _, de := range entries {
		childPath := filepath.Join(entry.path, de.Name())
		childEntry := &layerBrowserEntry{path: childPath, isDir: de.IsDir()}
		name := de.Name()
		if de.IsDir() {
			name += "/"
		}
		childNode := tview.NewTreeNode(name).SetSelectable(true).SetExpanded(false).SetReference(childEntry)
		node.AddChild(childNode)
	}
	entry.loaded = true
}

func (v *ImageLayersView) renderPreview(node *tview.TreeNode) {
	if node == nil {
		v.preview.SetText(" [gray]No file selected[-]")
		return
	}
	entry, _ := node.GetReference().(*layerBrowserEntry)
	if entry == nil || entry.isDir {
		v.preview.SetText(" [gray]Select a file to preview[-]")
		return
	}

	fi, err := os.Stat(entry.path)
	if err != nil {
		v.preview.SetText(fmt.Sprintf(" [red]%v[-]", err))
		return
	}
	if fi.Size() > 512*1024 {
		v.preview.SetText(fmt.Sprintf(" [gray]File too large to preview (%s)[-]", formatBytes(fi.Size())))
		return
	}

	f, err := os.Open(entry.path)
	if err != nil {
		v.preview.SetText(fmt.Sprintf(" [red]%v[-]", err))
		return
	}
	defer f.Close()

	buf := make([]byte, 4096)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		v.preview.SetText(fmt.Sprintf(" [red]%v[-]", err))
		return
	}
	content := string(buf[:n])
	if !utf8.ValidString(content) {
		v.preview.SetText(fmt.Sprintf(" [gray]Binary file (%s)[-]", formatBytes(fi.Size())))
		return
	}
	v.preview.SetText(content)
}

// --- Builder helpers ---

func buildLayerHeaderV1(config *runtime.ContainerConfig, runtime *runtime.RuntimeProfile, _ *runtime.ImageConfigInfo) string {
	snapshotter := "-"
	rootfsPath := "-"
	readonly := "yes"

	if config != nil {
		if config.Snapshotter != "" {
			snapshotter = config.Snapshotter
		}
		if config.SnapshotKey != "" || config.WritableLayerPath != "" {
			readonly = "no"
		}
	}
	if runtime != nil && runtime.RootFSPath != "" {
		rootfsPath = runtime.RootFSPath
	}

	return fmt.Sprintf(
		" [gray]Snapshotter:[-] [white]%s[-]\n [gray]Rootfs Directory:[-] [white]%s[-]\n [gray]Readonly:[-] [white]%s[-]",
		snapshotter, rootfsPath, readonly,
	)
}

func buildRWLayerNodeV1(config *runtime.ContainerConfig, rwStats *runtime.ContainerRWLayerStats, rwLayerPath string) *tview.TreeNode {
	node := tview.NewTreeNode("[yellow::b]RW Layer[-:-:-]").SetSelectable(true).SetExpanded(true)

	snapshotKey := "unknown"
	path := fallbackValue(rwLayerPath, "unknown")
	if config != nil {
		if config.SnapshotKey != "" {
			snapshotKey = config.SnapshotKey
		}
		if config.WritableLayerPath != "" {
			path = config.WritableLayerPath
		}
	}

	rows := []string{
		"Snapshot Key: " + snapshotKey,
		"Path: " + path,
	}
	if rwStats != nil && (rwStats.RWLayerUsage > 0 || rwStats.RWLayerInodes > 0) {
		rows = append(rows, fmt.Sprintf("Disk Usage: %s (%d inodes)", formatBytes(rwStats.RWLayerUsage), rwStats.RWLayerInodes))
	} else {
		rows = append(rows, "Disk Usage: unknown")
	}

	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}

	// Store path for browser.
	if path != "unknown" {
		node.SetReference(&layerBrowserEntry{path: path, isDir: true})
	}
	return node
}

func buildReadOnlyLayersNodeV1(layers []*runtime.ImageLayer) *tview.TreeNode {
	count := len(layers)
	node := tview.NewTreeNode(fmt.Sprintf("[aqua::b]Read-Only Layer (%d, top to base)[-:-:-]", count)).
		SetSelectable(true).SetExpanded(true)

	if count == 0 {
		node.AddChild(tview.NewTreeNode("[gray]No read-only image layers resolved[-]").SetSelectable(false))
		return node
	}

	sorted := make([]*runtime.ImageLayer, len(layers))
	copy(sorted, layers)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Index > sorted[j].Index })

	for _, layer := range sorted {
		label := fmt.Sprintf("Layer %d: %s", layer.Index, shortenLayerIDV1(layer.SnapshotKey, layer.UncompressedDigest, layer.CompressedDigest))
		layerNode := tview.NewTreeNode(label).SetSelectable(true).SetExpanded(false)
		layerNode.AddChild(tview.NewTreeNode("[gray]  Rootfs Diff ID: [white]" + fallbackLayerField(layer.UncompressedDigest) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode("[gray]  Snapshot Key: [white]" + fallbackLayerField(layer.SnapshotKey) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode("[gray]  Snapshot Path: [white]" + fallbackLayerField(layer.Path) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode("[gray]  Content Path: [white]" + fallbackLayerField(layer.ContentPath) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Content Size: [white]%s[-]", formatLayerSize(layer))).SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Disk Usage: [white]%s[-]", formatLayerDiskUsage(layer))).SetSelectable(false))
		if layer.Path != "" {
			layerNode.SetReference(&layerBrowserEntry{path: layer.Path, isDir: true})
		}
		node.AddChild(layerNode)
	}
	return node
}

func shortenLayerIDV1(values ...string) string {
	for _, val := range values {
		val = strings.TrimSpace(val)
		if val == "" {
			continue
		}
		val = strings.TrimPrefix(val, "sha256:")
		if len(val) > 12 {
			return val[:12]
		}
		return val
	}
	return "unresolved"
}

func fallbackLayerField(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unresolved"
	}
	return value
}

func formatLayerSize(layer *runtime.ImageLayer) string {
	if layer == nil || layer.Size <= 0 {
		return "unknown"
	}
	compression := strings.TrimSpace(layer.CompressionType)
	if compression == "" {
		compression = "unknown"
	}
	return fmt.Sprintf("%s (%s)", formatBytes(layer.Size), compression)
}

func formatLayerDiskUsage(layer *runtime.ImageLayer) string {
	if layer == nil || (layer.UsageSize <= 0 && layer.UsageInodes <= 0) {
		return "unknown"
	}
	return fmt.Sprintf("%s (%d inodes)", formatBytes(layer.UsageSize), layer.UsageInodes)
}

func selectedLayerPath(node *tview.TreeNode, config *runtime.ContainerConfig, storage *runtime.ContainerStorage) (string, string) {
	if node == nil {
		return "", ""
	}
	if entry, ok := node.GetReference().(*layerBrowserEntry); ok && entry != nil && entry.path != "" {
		return entry.path, node.GetText()
	}
	// Fallback: try RW layer path from config.
	if config != nil && config.WritableLayerPath != "" {
		return config.WritableLayerPath, "RW Layer"
	}
	if storage != nil && storage.RWLayerPath != "" {
		return storage.RWLayerPath, "RW Layer"
	}
	return "", ""
}
