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
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// ImageLayersView renders the Rootfs Layers subpage.
type ImageLayersView struct {
	*tview.Flex

	app         *tview.Application
	rt          runtime.Runtime
	ctx         context.Context
	header      *tview.TextView
	body        *tview.Flex
	tree        *tview.TreeView
	browser     *tview.Flex
	browserInfo *tview.TextView
	browserTree *tview.TreeView
	preview     *tview.TextView
	statusBar   *tview.TextView
	containerID string
	detail      *models.ContainerDetail
	runtimeInfo *models.ContainerDetail
	layers      []*models.ImageLayer
	configInfo  *models.ImageConfigInfo
	lastError   error
	browserOpen bool
	browserRoot string
	focusPane   imageLayersFocusPane
	mu          sync.Mutex
}

type imageLayersFocusPane int

const (
	imageLayersFocusTree imageLayersFocusPane = iota
	imageLayersFocusBrowser
)

type layerBrowserEntry struct {
	path   string
	isDir  bool
	loaded bool
}

// NewImageLayersView creates a new Rootfs Layers view.
func NewImageLayersView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *ImageLayersView {
	v := &ImageLayersView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
		ctx:  ctx,
	}

	v.header = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	v.header.SetBorder(true)
	v.header.SetBorderColor(tcell.ColorDarkCyan)
	v.header.SetTitle(" Rootfs Context ")

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

	v.browserInfo = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(true)
	v.browserInfo.SetBorder(true)
	v.browserInfo.SetBorderColor(tcell.ColorDarkCyan)
	v.browserInfo.SetTitle(" Layer Browser ")
	v.browserInfo.SetText(" [gray]Select a layer and press i to inspect its snapshot path[-]")

	v.browserTree = tview.NewTreeView()
	v.browserTree.SetBorder(false)
	v.browserTree.SetGraphics(true)
	v.browserTree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.browserTree.SetRoot(tview.NewTreeNode("[gray]No layer browser data[-]").SetSelectable(false))
	v.browserTree.SetSelectedFunc(func(node *tview.TreeNode) {
		v.toggleBrowserNode(node)
	})
	v.browserTree.SetChangedFunc(func(node *tview.TreeNode) {
		v.renderPreview(node)
	})

	v.preview = tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	v.preview.SetBorder(true)
	v.preview.SetBorderColor(tcell.ColorDarkCyan)
	v.preview.SetTitle(" Preview ")
	v.preview.SetText(" [gray]No file selected[-]")

	v.browser = tview.NewFlex().SetDirection(tview.FlexRow)
	v.browser.AddItem(v.browserInfo, 3, 0, false)
	v.browser.AddItem(v.browserTree, 0, 2, true)
	v.browser.AddItem(v.preview, 0, 3, false)

	v.body = tview.NewFlex().SetDirection(tview.FlexColumn)

	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.header, 4, 0, false)
	v.Flex.AddItem(v.body, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.refreshBodyLayout()

	v.render()
	v.updateStatusBar()
	return v
}

// SetContainer sets the active container.
func (v *ImageLayersView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.layers = nil
	v.configInfo = nil
	v.runtimeInfo = nil
	v.lastError = nil
	v.browserOpen = false
	v.browserRoot = ""
	v.focusPane = imageLayersFocusTree
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
}

// SetDetail stores overview detail for image resolution.
func (v *ImageLayersView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
	v.render()
}

// SetRuntimeInfo stores runtime context used by the header and RW layer.
func (v *ImageLayersView) SetRuntimeInfo(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.runtimeInfo = detail
	v.mu.Unlock()
	v.render()
}

// Refresh loads image layers and image config metadata.
func (v *ImageLayersView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	detail := v.detail
	runtimeInfo := v.runtimeInfo
	v.mu.Unlock()

	if detail == nil {
		v.render()
		return nil
	}

	imageRef := resolveLayerImageRef(detail)
	if imageRef == "" {
		v.mu.Lock()
		v.lastError = fmt.Errorf("image reference is empty")
		v.mu.Unlock()
		v.render()
		return nil
	}

	snapshotter := ""
	rwSnapshotKey := ""
	if runtimeInfo != nil {
		snapshotter = runtimeInfo.Snapshotter
		rwSnapshotKey = runtimeInfo.SnapshotKey
	}

	layers, err := v.rt.GetImageLayers(ctx, imageRef, snapshotter, rwSnapshotKey)
	if err != nil {
		v.mu.Lock()
		v.lastError = err
		v.layers = nil
		v.configInfo = nil
		v.mu.Unlock()
		v.render()
		v.updateStatusBar()
		return err
	}
	configInfo, _ := v.rt.GetImageConfigInfo(ctx, imageRef)

	v.mu.Lock()
	v.layers = layers
	v.configInfo = configInfo
	v.lastError = nil
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
	return nil
}

// HandleInput processes tree interaction.
func (v *ImageLayersView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return nil
	}
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Key() {
	case tcell.KeyEnter:
		if v.focusPane == imageLayersFocusBrowser {
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
		if v.focusPane == imageLayersFocusBrowser {
			v.toggleBrowserNode(v.browserTree.GetCurrentNode())
		} else if node := v.tree.GetCurrentNode(); node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
		return nil
	case 'a', 'A':
		if v.focusPane == imageLayersFocusBrowser {
			v.expandBrowserAll()
		} else {
			v.expandAll()
		}
		return nil
	}
	return event
}

// GetFocusPrimitive returns the tree focus target.
func (v *ImageLayersView) GetFocusPrimitive() tview.Primitive {
	if v.browserOpen && v.focusPane == imageLayersFocusBrowser {
		return v.browserTree
	}
	return v.tree
}

func (v *ImageLayersView) render() {
	v.mu.Lock()
	detail := v.detail
	runtimeInfo := v.runtimeInfo
	layers := append([]*models.ImageLayer(nil), v.layers...)
	configInfo := v.configInfo
	lastError := v.lastError
	v.mu.Unlock()

	activeDetail := runtimeInfo
	if activeDetail == nil {
		activeDetail = detail
	}

	v.header.SetText(buildLayerHeader(activeDetail, configInfo))

	root := tview.NewTreeNode("[aqua::b]Rootfs Layers[-:-:-]").SetSelectable(false).SetExpanded(true)
	if lastError != nil {
		root.AddChild(tview.NewTreeNode("[red]Failed to load read-only layers: " + lastError.Error() + "[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		v.refreshBodyLayout()
		return
	}
	if activeDetail == nil && len(layers) == 0 {
		root.AddChild(tview.NewTreeNode("[gray]Refresh to resolve snapshotter, rootfs path and image layers[-]").SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		v.refreshBodyLayout()
		return
	}

	root.AddChild(buildRWLayerNode(activeDetail))
	root.AddChild(buildReadOnlyLayersNode(layers))

	v.tree.SetRoot(root)
	v.tree.SetCurrentNode(root)
	v.refreshBodyLayout()
}

func (v *ImageLayersView) expandAll() {
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

func (v *ImageLayersView) updateStatusBar() {
	if v.browserOpen {
		v.statusBar.SetText(" [white]Rootfs Layers:[-] browser open for selected layer  |  [yellow]i[white]:close browser  [yellow]e/Enter[white]:toggle dir  [yellow]a[white]:expand/collapse all")
		return
	}
	v.statusBar.SetText(" [white]Rootfs Layers:[-] rw layer and read-only layers (top to base)  |  [yellow]i[white]:browse layer files  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse all")
}

func buildLayerHeader(detail *models.ContainerDetail, configInfo *models.ImageConfigInfo) string {
	snapshotter := "-"
	rootfsPath := "-"
	readonly := "yes"

	if detail != nil {
		if strings.TrimSpace(detail.Snapshotter) != "" {
			snapshotter = detail.Snapshotter
		}
		if detail.RuntimeProfile != nil && detail.RuntimeProfile.RootFS != nil {
			rootfs := detail.RuntimeProfile.RootFS
			switch {
			case strings.TrimSpace(rootfs.MountRootFSPath) != "":
				rootfsPath = rootfs.MountRootFSPath
			case strings.TrimSpace(rootfs.BundleRootFSPath) != "":
				rootfsPath = rootfs.BundleRootFSPath
			}
		}
		if strings.TrimSpace(detail.SnapshotKey) != "" || strings.TrimSpace(detail.WritableLayerPath) != "" {
			readonly = "no"
		}
	}
	_ = configInfo

	return fmt.Sprintf(
		" [gray]Snapshotter:[-] [white]%s[-]\n [gray]Rootfs Directory:[-] [white]%s[-]\n [gray]Readonly:[-] [white]%s[-]",
		snapshotter,
		rootfsPath,
		readonly,
	)
}

func buildRWLayerNode(detail *models.ContainerDetail) *tview.TreeNode {
	node := tview.NewTreeNode("[yellow::b]RW Layer[-:-:-]").SetSelectable(true).SetExpanded(true)
	if detail == nil {
		node.AddChild(tview.NewTreeNode("[gray]RW layer is unresolved[-]").SetSelectable(false))
		return node
	}
	node.SetReference(detail)

	rows := []string{}
	if detail.SnapshotKey != "" {
		rows = append(rows, "Snapshot Key: "+detail.SnapshotKey)
	} else {
		rows = append(rows, "Snapshot Key: unknown")
	}
	if detail.WritableLayerPath != "" {
		rows = append(rows, "Path: "+detail.WritableLayerPath)
	} else {
		rows = append(rows, "Path: unknown")
	}
	if detail.RWLayerUsage > 0 || detail.RWLayerInodes > 0 {
		rows = append(rows, fmt.Sprintf("Disk Usage: %s (%d inodes)", formatBytes(detail.RWLayerUsage), detail.RWLayerInodes))
	} else {
		rows = append(rows, "Disk Usage: unknown")
	}
	for _, row := range rows {
		node.AddChild(tview.NewTreeNode("[gray]  " + row + "[-]").SetSelectable(false))
	}
	return node
}

func buildReadOnlyLayersNode(layers []*models.ImageLayer) *tview.TreeNode {
	count := len(layers)
	title := fmt.Sprintf("[aqua::b]Read-Only Layer (%d, top to base)[-:-:-]", count)
	node := tview.NewTreeNode(title).SetSelectable(true).SetExpanded(true)
	if count == 0 {
		node.AddChild(tview.NewTreeNode("[gray]No read-only image layers resolved[-]").SetSelectable(false))
		return node
	}

	sorted := append([]*models.ImageLayer(nil), layers...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].Index > sorted[j].Index
	})

	for _, layer := range sorted {
		label := fmt.Sprintf("Layer %d: %s", layer.Index, shortenLayerID(layer.SnapshotKey, layer.UncompressedDigest, layer.CompressedDigest))
		layerNode := tview.NewTreeNode(label).SetSelectable(true).SetExpanded(false).SetReference(layer)
		layerNode.AddChild(tview.NewTreeNode("[gray]  Rootfs Diff ID: [white]" + fallbackLayerValue(layer.UncompressedDigest) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode("[gray]  Snapshot Key: [white]" + fallbackLayerValue(layer.SnapshotKey) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode("[gray]  Snapshot Path: [white]" + fallbackLayerValue(layer.SnapshotPath) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode("[gray]  Content Path: [white]" + fallbackLayerValue(layer.ContentPath) + "[-]").SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Content Size: [white]%s[-]", formatLayerContentSize(layer))).SetSelectable(false))
		layerNode.AddChild(tview.NewTreeNode(fmt.Sprintf("[gray]  Disk Usage: [white]%s[-]", formatLayerUsage(layer))).SetSelectable(false))
		node.AddChild(layerNode)
	}
	return node
}

func shortenLayerID(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		value = strings.TrimPrefix(value, "sha256:")
		if len(value) > 12 {
			return value[:12]
		}
		return value
	}
	return "unresolved"
}

func fallbackLayerValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unresolved"
	}
	return value
}

func formatLayerUsage(layer *models.ImageLayer) string {
	if layer == nil {
		return "unknown"
	}
	if layer.UsageSize > 0 || layer.UsageInodes > 0 {
		return fmt.Sprintf("%s (%d inodes)", formatBytes(layer.UsageSize), layer.UsageInodes)
	}
	return "unknown"
}

func formatLayerContentSize(layer *models.ImageLayer) string {
	if layer == nil || layer.Size <= 0 {
		return "unknown"
	}
	compression := strings.TrimSpace(layer.CompressionType)
	if compression == "" {
		compression = "unknown"
	}
	return fmt.Sprintf("%s (%s)", formatBytes(layer.Size), compression)
}

func resolveLayerImageRef(detail *models.ContainerDetail) string {
	if detail == nil {
		return ""
	}
	for _, candidate := range []string{detail.Image, detail.ImageName, detail.ImageID} {
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func (v *ImageLayersView) refreshBodyLayout() {
	v.body.Clear()
	v.body.AddItem(v.tree, 0, 3, !v.browserOpen)
	if v.browserOpen {
		v.body.AddItem(v.browser, 0, 2, true)
	}
}

func (v *ImageLayersView) openBrowserFromSelection() {
	v.mu.Lock()
	detail := v.detail
	runtimeInfo := v.runtimeInfo
	v.mu.Unlock()

	activeDetail := runtimeInfo
	if activeDetail == nil {
		activeDetail = detail
	}

	path, title := selectedLayerBrowsePath(v.tree.GetCurrentNode(), activeDetail)
	if strings.TrimSpace(path) == "" {
		v.statusBar.SetText(" [white]Rootfs Layers:[-] select a concrete layer with a readable path before opening the browser")
		return
	}

	info, err := os.Stat(path)
	if err != nil {
		v.statusBar.SetText(fmt.Sprintf(" [white]Rootfs Layers:[-] unable to inspect layer path: %v", err))
		return
	}

	v.mu.Lock()
	v.browserOpen = true
	v.browserRoot = path
	v.focusPane = imageLayersFocusBrowser
	v.mu.Unlock()

	v.browserInfo.SetText(fmt.Sprintf(" [gray]Layer:[-] [white]%s[-]\n [gray]Path:[-] [white]%s[-]", title, path))
	v.initializeBrowserTree(path, info)
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
	v.focusPane = imageLayersFocusTree
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

func (v *ImageLayersView) initializeBrowserTree(path string, info os.FileInfo) {
	entry := &layerBrowserEntry{path: path, isDir: info.IsDir()}
	rootText := path
	if base := filepath.Base(path); base != "" && base != "." && base != string(filepath.Separator) {
		rootText = base
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
	children, err := buildBrowserChildren(entry.path)
	node.ClearChildren()
	if err != nil {
		node.AddChild(tview.NewTreeNode("[red]Unable to read directory: " + err.Error() + "[-]").SetSelectable(false))
		entry.loaded = true
		return
	}
	for _, child := range children {
		node.AddChild(child)
	}
	entry.loaded = true
}

func buildBrowserChildren(path string) ([]*tview.TreeNode, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	children := make([]*tview.TreeNode, 0, len(entries))
	for _, item := range entries {
		itemPath := filepath.Join(path, item.Name())
		label := item.Name()
		ref := &layerBrowserEntry{path: itemPath, isDir: item.IsDir()}
		if item.IsDir() {
			label += "/"
		}
		node := tview.NewTreeNode(label).SetSelectable(true).SetReference(ref)
		if item.IsDir() {
			node.AddChild(tview.NewTreeNode("[gray]loading[-]").SetSelectable(false))
		}
		children = append(children, node)
	}
	if len(children) == 0 {
		children = append(children, tview.NewTreeNode("[gray]Empty directory[-]").SetSelectable(false))
	}
	return children, nil
}

func (v *ImageLayersView) renderPreview(node *tview.TreeNode) {
	if node == nil {
		v.preview.SetText(" [gray]No file selected[-]")
		return
	}
	entry, _ := node.GetReference().(*layerBrowserEntry)
	if entry == nil {
		v.preview.SetText(" [gray]Select a file or directory to preview[-]")
		return
	}
	text, title := describeBrowserEntry(entry)
	v.preview.SetTitle(title)
	v.preview.SetText(text)
	if entry.isDir {
		v.loadBrowserChildren(node, entry)
	}
}

func describeBrowserEntry(entry *layerBrowserEntry) (string, string) {
	if entry == nil {
		return " [gray]No file selected[-]", " Preview "
	}
	info, err := os.Stat(entry.path)
	if err != nil {
		return fmt.Sprintf(" [red]Unable to stat path:[-] %v", err), " Preview "
	}
	if info.IsDir() {
		entries, err := os.ReadDir(entry.path)
		if err != nil {
			return fmt.Sprintf(" [red]Unable to read directory:[-] %v", err), " Preview "
		}
		lines := []string{
			fmt.Sprintf(" [gray]Directory:[-] [white]%s[-]", entry.path),
			fmt.Sprintf(" [gray]Entries:[-] [white]%d[-]", len(entries)),
		}
		limit := len(entries)
		if limit > 24 {
			limit = 24
		}
		for _, child := range entries[:limit] {
			name := child.Name()
			if child.IsDir() {
				name += "/"
			}
			lines = append(lines, "  "+name)
		}
		if len(entries) > limit {
			lines = append(lines, fmt.Sprintf("  ... %d more", len(entries)-limit))
		}
		return strings.Join(lines, "\n"), " Directory Preview "
	}

	file, err := os.Open(entry.path)
	if err != nil {
		return fmt.Sprintf(" [red]Unable to open file:[-] %v", err), " File Preview "
	}
	defer file.Close()

	chunk, err := io.ReadAll(io.LimitReader(file, 16384))
	if err != nil {
		return fmt.Sprintf(" [red]Unable to read file:[-] %v", err), " File Preview "
	}
	lines := []string{
		fmt.Sprintf(" [gray]File:[-] [white]%s[-]", entry.path),
		fmt.Sprintf(" [gray]Size:[-] [white]%s[-]", formatBytes(info.Size())),
	}
	if looksBinary(chunk) {
		lines = append(lines, " [gray]Preview:[-] binary or non-UTF8 content")
		return strings.Join(lines, "\n"), " File Preview "
	}
	lines = append(lines, "", string(chunk))
	if info.Size() > int64(len(chunk)) {
		lines = append(lines, "", fmt.Sprintf("[gray]... truncated, showing first %d bytes[-]", len(chunk)))
	}
	return strings.Join(lines, "\n"), " File Preview "
}

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if !utf8.Valid(data) {
		return true
	}
	for _, b := range data {
		if b == 0 {
			return true
		}
	}
	return false
}

func selectedLayerBrowsePath(node *tview.TreeNode, activeDetail *models.ContainerDetail) (string, string) {
	if node == nil {
		return "", ""
	}
	switch ref := node.GetReference().(type) {
	case *models.ImageLayer:
		return strings.TrimSpace(ref.SnapshotPath), node.GetText()
	case *models.ContainerDetail:
		if ref != nil {
			return strings.TrimSpace(ref.WritableLayerPath), "RW Layer"
		}
	}
	if activeDetail != nil && node.GetText() == "[yellow::b]RW Layer[-:-:-]" {
		return strings.TrimSpace(activeDetail.WritableLayerPath), "RW Layer"
	}
	return "", ""
}

func (v *ImageLayersView) expandBrowserAll() {
	root := v.browserTree.GetRoot()
	if root == nil {
		return
	}
	var walk func(node *tview.TreeNode)
	walk = func(node *tview.TreeNode) {
		entry, _ := node.GetReference().(*layerBrowserEntry)
		if entry != nil && entry.isDir {
			v.loadBrowserChildren(node, entry)
			node.SetExpanded(true)
		}
		for _, child := range node.GetChildren() {
			walk(child)
		}
	}
	walk(root)
	v.browserTree.SetCurrentNode(root)
	v.renderPreview(root)
}
