package views

import (
	"bytes"
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
	"github.com/icebergu/c-ray/pkg/ui/components"
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

	container             runtime.Container
	storage               *runtime.ContainerStorage
	rwStats               *runtime.ContainerRWLayerStats
	containerPID          uint32
	config                *runtime.ContainerConfig
	runtime               *runtime.RuntimeProfile
	imageConfig           *runtime.ImageConfigInfo
	lastError             error
	browserOpen           bool
	browserRootPath       string
	browserMode           int // 0=merged rootfs, 1=fallback layer path
	selectedLayerTitle    string
	selectedLayerDiffPath string
	highlightLayerPaths   map[string]bool // rel paths directly in selected layer
	highlightAncestors    map[string]bool // ancestor dirs of highlighted paths
	focusPane             int             // 0=tree, 1=browser
	previewExpanded       bool            // whether preview is showing file content
	mu                    sync.Mutex
}

type layerBrowserEntry struct {
	path    string
	relPath string // relative to browser root
	isDir   bool
	loaded  bool
}

type layerTreeSelection struct {
	path  string
	title string
}

// NewImageLayersView creates a new Rootfs Layers view.
func NewImageLayersView(app *tview.Application, ctx context.Context) *ImageLayersView {
	v := &ImageLayersView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		ctx:  ctx,
	}

	v.header = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	v.header.SetBorder(true).SetBorderColor(components.ColorFgBorder).SetTitle(fmt.Sprintf(" %s ", components.Accent("Rootfs Context")))

	v.tree = tview.NewTreeView()
	components.InitTreeView(v.tree)
	v.tree.SetRoot(components.NewTreeNode(components.Muted("No rootfs layer data")).SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})
	v.tree.SetChangedFunc(func(node *tview.TreeNode) {
		if v.browserOpen {
			v.syncBrowserWithSelection(node)
		}
	})

	v.browserInfo = tview.NewTextView().SetDynamicColors(true).SetWrap(true)
	v.browserInfo.SetBorder(true).SetBorderColor(components.ColorFgBorder).SetTitle(fmt.Sprintf(" %s ", components.Accent("Layer Browser")))
	v.browserInfo.SetText(fmt.Sprintf(" %s", components.Muted("Select a layer and press i to inspect its path")))

	v.browserTree = tview.NewTreeView()
	components.InitTreeView(v.browserTree)
	v.browserTree.SetRoot(components.NewTreeNode(components.Muted("No layer browser data")).SetSelectable(false))
	v.browserTree.SetSelectedFunc(func(node *tview.TreeNode) { v.toggleBrowserNode(node) })
	v.browserTree.SetChangedFunc(func(node *tview.TreeNode) {
		v.previewExpanded = false
		v.renderPreview(node)
	})

	v.preview = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.preview.SetBorder(true).SetBorderColor(components.ColorFgBorder).SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
	v.preview.SetText(fmt.Sprintf(" %s", components.Muted("No file selected")))

	v.browser = tview.NewFlex().SetDirection(tview.FlexRow)
	v.browser.AddItem(v.browserInfo, 4, 0, false)
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
	v.updateFocusStyles()
	return v
}

// SetContainer sets the container handle.
func (v *ImageLayersView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.storage = nil
	v.rwStats = nil
	v.containerPID = 0
	v.config = nil
	v.runtime = nil
	v.imageConfig = nil
	v.lastError = nil
	v.browserOpen = false
	v.browserRootPath = ""
	v.browserMode = 0
	v.selectedLayerTitle = ""
	v.selectedLayerDiffPath = ""
	v.highlightLayerPaths = nil
	v.highlightAncestors = nil
	v.focusPane = 0
	v.mu.Unlock()
	v.render()
	v.updateStatusBar()
}

func (v *ImageLayersView) SetContainerPID(pid uint32) {
	v.mu.Lock()
	v.containerPID = pid
	v.mu.Unlock()
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
	case tcell.KeyTab, tcell.KeyBacktab:
		if v.browserOpen {
			if v.focusPane == 0 {
				v.focusPane = 1
				if v.app != nil {
					v.app.SetFocus(v.browserTree)
				}
			} else {
				v.focusPane = 0
				if v.app != nil {
					v.app.SetFocus(v.tree)
				}
			}
			v.updateFocusStyles()
			return nil
		}
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
	case 'p', 'P':
		if v.browserOpen && v.focusPane == 1 {
			v.expandPreview()
			return nil
		}
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
	rt := v.runtime
	rwStats := v.rwStats
	imgConfig := v.imageConfig
	lastError := v.lastError
	selectedLayerPath := v.selectedLayerDiffPath
	v.mu.Unlock()

	headerText := buildLayerHeaderV1(config, storage, rt, imgConfig)

	root := components.NewTreeNode(components.Accent("Rootfs Layers")).SetSelectable(false).SetExpanded(true)
	if lastError != nil {
		root.AddChild(components.NewTreeNode(fmt.Sprintf("[%s]Failed to load layers: %s[-]", components.ColorName(components.ColorFgError), lastError.Error())).SetSelectable(false))
	} else if storage == nil {
		root.AddChild(components.NewTreeNode(components.Muted("Refresh to resolve snapshotter, rootfs path and image layers")).SetSelectable(false))
	} else {
		root.AddChild(buildRWLayerNodeV1(config, storage, rwStats, storage.RWLayerPath))
		root.AddChild(buildReadOnlyLayersNodeV1(storage.ReadOnlyLayers))
	}

	queueUpdateDraw(v.app, func() {
		v.header.SetText(headerText)
		v.tree.SetRoot(root)
		if selectedNode := findLayerNodeByPath(root, selectedLayerPath); selectedNode != nil {
			v.tree.SetCurrentNode(selectedNode)
		} else {
			v.tree.SetCurrentNode(root)
		}
		v.refreshBodyLayout()
		v.updateFocusStyles()
	})
}

func (v *ImageLayersView) expandAll() {
	target := v.tree
	if v.focusPane == 1 {
		target = v.browserTree
	}
	root := target.GetRoot()
	components.ExpandAllNodes(root)
	target.SetCurrentNode(root)
}

func (v *ImageLayersView) updateStatusBar() {
	if v.browserOpen {
		v.statusBar.SetText(fmt.Sprintf(" %s  |  %s  %s  %s  %s  %s",
			components.Muted("Rootfs Layers: browser open"),
			components.KeyHint("i", "close browser"),
			components.KeyHint("Tab", "switch pane"),
			components.KeyHint("e/Enter", "toggle"),
			components.KeyHint("p", "preview file"),
			components.KeyHint("a", "expand/collapse"),
		))
		return
	}
	v.statusBar.SetText(fmt.Sprintf(" %s  |  %s  %s  %s",
		components.Muted("Rootfs Layers: rw layer and read-only layers"),
		components.KeyHint("i", "browse rootfs"),
		components.KeyHint("e", "toggle"),
		components.KeyHint("a", "expand/collapse"),
	))
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
	v.mu.Lock()
	rt := v.runtime
	containerPID := v.containerPID
	config := v.config
	storage := v.storage
	v.mu.Unlock()

	// Resolve selected layer info.
	node := v.tree.GetCurrentNode()
	layerPath, layerTitle := selectedLayerPath(node, config, storage)

	rootPath, mode, err := resolveBrowserRoot(rt, containerPID, layerPath)
	if err != nil {
		v.statusBar.SetText(fmt.Sprintf(" %s %s", components.Bright("Rootfs Layers:"), err.Error()))
		return
	}

	info, err := os.Stat(rootPath)
	if err != nil {
		v.statusBar.SetText(fmt.Sprintf(" %s %s", components.Bright("Rootfs Layers:"), fmt.Sprintf("unable to inspect: %v", err)))
		return
	}

	// Scan selected layer diff path for highlight mapping.
	layerPaths, ancestorDirs := scanLayerPaths(layerPath)

	v.mu.Lock()
	v.browserOpen = true
	v.browserRootPath = rootPath
	v.browserMode = mode
	v.selectedLayerTitle = layerTitle
	v.selectedLayerDiffPath = layerPath
	v.highlightLayerPaths = layerPaths
	v.highlightAncestors = ancestorDirs
	v.focusPane = 1
	v.mu.Unlock()

	v.updateBrowserInfo()
	v.initBrowserTree(rootPath, info)
	v.refreshBodyLayout()
	if v.app != nil {
		v.app.SetFocus(v.browserTree)
	}
	v.updateFocusStyles()
	v.updateStatusBar()
}

// syncBrowserWithSelection is called when the left tree selection changes
// while the browser is open. It rescans and updates highlights without
// rebuilding the browser tree.
func (v *ImageLayersView) syncBrowserWithSelection(node *tview.TreeNode) {
	if node == nil || !v.browserOpen {
		return
	}

	v.mu.Lock()
	config := v.config
	storage := v.storage
	prevPath := v.selectedLayerDiffPath
	v.mu.Unlock()

	layerPath, layerTitle := selectedLayerPath(node, config, storage)
	if layerPath == "" || layerPath == prevPath {
		return
	}

	layerPaths, ancestorDirs := scanLayerPaths(layerPath)

	v.mu.Lock()
	v.selectedLayerTitle = layerTitle
	v.selectedLayerDiffPath = layerPath
	v.highlightLayerPaths = layerPaths
	v.highlightAncestors = ancestorDirs
	v.mu.Unlock()

	v.updateBrowserInfo()
	v.updateBrowserNodeHighlights()
}

func (v *ImageLayersView) closeBrowser() {
	v.mu.Lock()
	v.browserOpen = false
	v.browserRootPath = ""
	v.browserMode = 0
	v.selectedLayerTitle = ""
	v.selectedLayerDiffPath = ""
	v.highlightLayerPaths = nil
	v.highlightAncestors = nil
	v.focusPane = 0
	v.mu.Unlock()
	v.browserInfo.SetText(fmt.Sprintf(" %s", components.Muted("Select a layer and press i to inspect")))
	v.preview.SetText(fmt.Sprintf(" %s", components.Muted("No file selected")))
	v.browserTree.SetRoot(components.NewTreeNode(components.Muted("No layer browser data")).SetSelectable(false))
	v.refreshBodyLayout()
	if v.app != nil {
		v.app.SetFocus(v.tree)
	}
	v.updateFocusStyles()
	v.updateStatusBar()
}

func (v *ImageLayersView) updateBrowserInfo() {
	v.mu.Lock()
	mode := v.browserMode
	rootPath := v.browserRootPath
	layerTitle := v.selectedLayerTitle
	fileCount := len(v.highlightLayerPaths)
	v.mu.Unlock()

	modeLabel := "Merged Rootfs"
	if mode == 1 {
		modeLabel = "Layer Path (rootfs unavailable)"
	} else if mode == 2 {
		modeLabel = "Proc Root"
	}

	v.browserInfo.SetText(fmt.Sprintf(" %s %s  %s %s\n %s %s  %s %d entries",
		components.Muted("Mode:"), components.Bright(modeLabel),
		components.Muted("Root:"), rootPath,
		components.Muted("Layer:"), components.Bright(layerTitle),
		components.Muted("Highlighted:"), fileCount,
	))
}

func (v *ImageLayersView) initBrowserTree(path string, info os.FileInfo) {
	entry := &layerBrowserEntry{path: path, relPath: "", isDir: info.IsDir()}
	root := components.NewTreeNode(v.browserNodeText("/", true, "")).
		SetSelectable(true).SetExpanded(true).SetReference(entry)
	if entry.isDir {
		v.loadBrowserChildren(root, entry)
	}
	v.browserTree.SetRoot(root)
	v.browserTree.SetCurrentNode(root)
	v.renderPreview(root)
	v.updateFocusStyles()
}

func (v *ImageLayersView) updateFocusStyles() {
	components.ApplyTreeFocusStyle(v.tree, !v.browserOpen || v.focusPane == 0)
	components.ApplyTreeFocusStyle(v.browserTree, v.browserOpen && v.focusPane == 1)

	v.header.SetBorderColor(components.ColorFgBorder)
	v.browserInfo.SetBorderColor(components.ColorFgBorder)
	v.preview.SetBorderColor(components.ColorFgBorder)
	if v.browserOpen && v.focusPane == 1 {
		v.browserInfo.SetBorderColor(components.ColorFgAccent)
		v.preview.SetBorderColor(components.ColorFgAccent)
	}
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
		node.AddChild(components.NewTreeNode(fmt.Sprintf("[%s]%s[-]", components.ColorName(components.ColorFgError), err.Error())).SetSelectable(false))
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
		childRelPath := de.Name()
		if entry.relPath != "" {
			childRelPath = filepath.Join(entry.relPath, de.Name())
		}
		childEntry := &layerBrowserEntry{path: childPath, relPath: childRelPath, isDir: de.IsDir()}
		name := de.Name()
		if de.IsDir() {
			name += "/"
		}
		text := v.browserNodeText(name, de.IsDir(), childRelPath)
		childNode := components.NewTreeNode(text).SetSelectable(true).SetExpanded(false).SetReference(childEntry)
		node.AddChild(childNode)
	}
	entry.loaded = true
}

// browserNodeText returns the display text for a browser tree node,
// applying highlight colors when the node's relPath matches the selected layer.
func (v *ImageLayersView) browserNodeText(name string, isDir bool, relPath string) string {
	v.mu.Lock()
	layerPaths := v.highlightLayerPaths
	ancestors := v.highlightAncestors
	v.mu.Unlock()

	if relPath != "" && layerPaths[relPath] {
		// Direct layer content: strong highlight.
		if isDir {
			return fmt.Sprintf("[%s]%s[-]", components.ColorName(components.ColorFgAccentAlt), name)
		}
		return fmt.Sprintf("[%s::b]%s[-:-:-]", components.ColorName(components.ColorFgAccentAlt), name)
	}
	if relPath != "" && ancestors[relPath] {
		// Ancestor directory of layer content: subtle highlight.
		return fmt.Sprintf("[%s]%s[-]", components.ColorName(components.ColorFgAccent), name)
	}
	return name
}

// updateBrowserNodeHighlights walks the existing browser tree and refreshes
// node text colors based on the current highlight sets, preserving expansion state.
func (v *ImageLayersView) updateBrowserNodeHighlights() {
	root := v.browserTree.GetRoot()
	if root == nil {
		return
	}
	var walk func(node *tview.TreeNode)
	walk = func(node *tview.TreeNode) {
		entry, _ := node.GetReference().(*layerBrowserEntry)
		if entry != nil {
			name := filepath.Base(entry.path)
			if entry.relPath == "" {
				name = "/"
			} else if entry.isDir {
				name += "/"
			}
			node.SetText(v.browserNodeText(name, entry.isDir, entry.relPath))
		}
		for _, child := range node.GetChildren() {
			walk(child)
		}
	}
	walk(root)
}

// scanLayerPaths walks a layer diff directory and returns two sets:
// layerPaths contains relative paths of all files/dirs directly in the layer;
// ancestorDirs contains directories that are ancestors of layerPaths entries
// but not themselves in the layer.
func scanLayerPaths(diffPath string) (layerPaths map[string]bool, ancestorDirs map[string]bool) {
	layerPaths = make(map[string]bool)
	ancestorDirs = make(map[string]bool)
	if diffPath == "" {
		return
	}
	info, err := os.Stat(diffPath)
	if err != nil || !info.IsDir() {
		return
	}
	const maxEntries = 50000
	count := 0
	filepath.WalkDir(diffPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if count >= maxEntries {
			return filepath.SkipAll
		}
		rel, relErr := filepath.Rel(diffPath, path)
		if relErr != nil || rel == "." {
			return nil
		}
		layerPaths[rel] = true
		count++
		// Mark ancestor directories that are not themselves layer entries.
		dir := filepath.Dir(rel)
		for dir != "." && dir != "" {
			if layerPaths[dir] {
				break
			}
			ancestorDirs[dir] = true
			dir = filepath.Dir(dir)
		}
		return nil
	})
	return
}

func (v *ImageLayersView) renderPreview(node *tview.TreeNode) {
	if node == nil {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(fmt.Sprintf(" %s", components.Muted("No file selected")))
		return
	}
	entry, _ := node.GetReference().(*layerBrowserEntry)
	if entry == nil || entry.isDir {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(fmt.Sprintf(" %s", components.Muted("Select a file to preview")))
		return
	}

	fi, err := os.Stat(entry.path)
	if err != nil {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err))
		return
	}

	// Detect file type by sniffing the first 512 bytes.
	fileType, isText := detectFileType(entry.path)

	// If preview was explicitly expanded by the user, show content now.
	if v.previewExpanded && isText {
		v.showFileContent(entry.path, fi)
		return
	}

	// Default: show metadata summary with a hint to expand.
	v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
	var lines []string
	lines = append(lines, fmt.Sprintf(" %s  %s",
		components.KV("File:", filepath.Base(entry.path)),
		components.KV("Size:", formatBytes(fi.Size()))))
	lines = append(lines, fmt.Sprintf(" %s", components.KV("Type:", fileType)))
	if isText && fi.Size() <= 512*1024 {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf(" %s", components.Muted("Press p to view content")))
	} else if fi.Size() > 512*1024 {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf(" %s", components.Muted("File too large to preview")))
	}
	v.preview.SetText(strings.Join(lines, "\n"))
}

// expandPreview loads and shows the content of the currently selected file.
func (v *ImageLayersView) expandPreview() {
	node := v.browserTree.GetCurrentNode()
	if node == nil {
		return
	}
	entry, _ := node.GetReference().(*layerBrowserEntry)
	if entry == nil || entry.isDir {
		return
	}
	_, isText := detectFileType(entry.path)
	if !isText {
		return
	}
	fi, err := os.Stat(entry.path)
	if err != nil {
		return
	}
	v.previewExpanded = true
	v.showFileContent(entry.path, fi)
}

func (v *ImageLayersView) showFileContent(path string, fi os.FileInfo) {
	if fi.Size() > 512*1024 {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(fmt.Sprintf(" %s", components.Muted(fmt.Sprintf("File too large to preview (%s)", formatBytes(fi.Size())))))
		return
	}
	f, err := os.Open(path)
	if err != nil {
		v.preview.SetText(fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err))
		return
	}
	defer f.Close()
	buf := make([]byte, 512*1024)
	n, err := f.Read(buf)
	if err != nil && err != io.EOF {
		v.preview.SetText(fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err))
		return
	}
	content := string(buf[:n])
	if !utf8.ValidString(content) {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(fmt.Sprintf(" %s", components.Muted(fmt.Sprintf("Binary file (%s)", formatBytes(fi.Size())))))
		return
	}
	v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview — "+filepath.Base(path))))
	v.preview.SetText(content)
	v.preview.ScrollToBeginning()
}

// detectFileType sniffs the first 512 bytes of file to determine its MIME-like
// description and whether it is human-readable text.
// detectFileType inspects up to 512 bytes using magic byte signatures, similar
// to the file(1) command. Returns a human-readable type name and whether the
// file is human-readable text.
func detectFileType(path string) (typeName string, isText bool) {
	f, err := os.Open(path)
	if err != nil {
		return "unknown", false
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, _ := f.Read(buf)
	if n == 0 {
		return "empty file", true
	}
	sniff := buf[:n]
	if name, binary := probeMagicBytes(sniff); name != "" {
		return name, !binary
	}
	if fileLooksLikeText(sniff) {
		return identifyTextFile(sniff, filepath.Ext(path)), true
	}
	return "binary data", false
}

// probeMagicBytes matches common binary/archive/image format signatures.
// Returns ("", false) when no signature matches.
func probeMagicBytes(b []byte) (name string, isBinary bool) {
	at := func(off int, sig []byte) bool {
		if off+len(sig) > len(b) {
			return false
		}
		return bytes.Equal(b[off:off+len(sig)], sig)
	}
	pfx := func(sig []byte) bool { return at(0, sig) }

	switch {
	// --- Executables / shared libraries ---
	case pfx([]byte{0x7f, 'E', 'L', 'F'}):
		arch := "unknown arch"
		if len(b) > 18 {
			switch b[18] { // e_machine low byte (LE)
			case 0x3e:
				arch = "x86-64"
			case 0xb7:
				arch = "arm64"
			case 0x28:
				arch = "arm"
			case 0x03:
				arch = "i386"
			}
		}
		if len(b) > 16 {
			switch b[16] { // e_type low byte
			case 2:
				return "ELF executable (" + arch + ")", true
			case 3:
				return "ELF shared library (" + arch + ")", true
			}
		}
		return "ELF binary (" + arch + ")", true
	case pfx([]byte{0xfe, 0xed, 0xfa, 0xce}), pfx([]byte{0xce, 0xfa, 0xed, 0xfe}):
		return "Mach-O 32-bit binary", true
	case pfx([]byte{0xfe, 0xed, 0xfa, 0xcf}), pfx([]byte{0xcf, 0xfa, 0xed, 0xfe}):
		return "Mach-O 64-bit binary", true
	case pfx([]byte{0xca, 0xfe, 0xba, 0xbe}):
		return "Mach-O fat binary", true
	// --- Compression ---
	case pfx([]byte{0x1f, 0x8b}):
		return "gzip compressed data", true
	case pfx([]byte{'B', 'Z', 'h'}):
		return "bzip2 compressed data", true
	case pfx([]byte{0xfd, '7', 'z', 'X', 'Z', 0x00}):
		return "xz compressed data", true
	case pfx([]byte{0x28, 0xb5, 0x2f, 0xfd}):
		return "zstd compressed data", true
	case pfx([]byte{0x04, 0x22, 0x4d, 0x18}):
		return "LZ4 compressed data", true
	// --- Archives ---
	case pfx([]byte{'P', 'K', 0x03, 0x04}):
		return "ZIP archive", true
	case pfx([]byte{'7', 'z', 0xbc, 0xaf, 0x27, 0x1c}):
		return "7-Zip archive", true
	case at(257, []byte("ustar")):
		return "POSIX tar archive", true
	// --- Images ---
	case pfx([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}):
		return "PNG image", true
	case pfx([]byte{0xff, 0xd8, 0xff}):
		return "JPEG image", true
	case pfx([]byte{'G', 'I', 'F', '8'}):
		return "GIF image", true
	case pfx([]byte{'B', 'M'}):
		return "BMP image", true
	case pfx([]byte{'R', 'I', 'F', 'F'}) && at(8, []byte("WEBP")):
		return "WebP image", true
	// --- Documents / databases ---
	case pfx([]byte{'%', 'P', 'D', 'F', '-'}):
		return "PDF document", true
	case pfx([]byte("SQLite format 3\x00")):
		return "SQLite database", true
	}
	return "", false
}

// fileLooksLikeText returns true when the sample is valid UTF-8 with no null bytes.
func fileLooksLikeText(b []byte) bool {
	return utf8.Valid(b) && bytes.IndexByte(b, 0x00) < 0
}

// identifyTextFile heuristically labels a text file by content and extension.
func identifyTextFile(b []byte, ext string) string {
	trimmed := bytes.TrimSpace(b)

	// Shebang line.
	if bytes.HasPrefix(trimmed, []byte("#!")) {
		line := trimmed[2:]
		if i := bytes.IndexByte(line, '\n'); i != -1 {
			line = line[:i]
		}
		interp := strings.ToLower(string(bytes.TrimSpace(line)))
		switch {
		case strings.Contains(interp, "python"):
			return "Python script"
		case strings.Contains(interp, "bash"):
			return "Bash script"
		case strings.Contains(interp, "zsh"):
			return "Zsh script"
		case strings.Contains(interp, "sh"):
			return "Shell script"
		case strings.Contains(interp, "ruby"):
			return "Ruby script"
		case strings.Contains(interp, "perl"):
			return "Perl script"
		case strings.Contains(interp, "node"):
			return "Node.js script"
		case strings.Contains(interp, "php"):
			return "PHP script"
		default:
			return "script (" + string(bytes.TrimSpace(line)) + ")"
		}
	}

	// PEM block.
	if bytes.HasPrefix(trimmed, []byte("-----BEGIN")) {
		return "PEM certificate/key"
	}
	// JSON.
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		return "JSON"
	}
	// XML / HTML.
	if bytes.HasPrefix(trimmed, []byte("<?xml")) {
		return "XML"
	}
	if bytes.HasPrefix(trimmed, []byte("<!DOCTYPE")) || bytes.HasPrefix(bytes.ToLower(trimmed), []byte("<html")) {
		return "HTML"
	}

	// Extension fallback.
	switch strings.ToLower(ext) {
	case ".yaml", ".yml":
		return "YAML"
	case ".toml":
		return "TOML"
	case ".ini", ".conf", ".cfg", ".config":
		return "config file"
	case ".env":
		return "environment file"
	case ".py":
		return "Python source"
	case ".sh", ".bash":
		return "Shell script"
	case ".zsh":
		return "Zsh script"
	case ".rb":
		return "Ruby source"
	case ".go":
		return "Go source"
	case ".rs":
		return "Rust source"
	case ".c", ".h":
		return "C source"
	case ".cpp", ".cc", ".cxx", ".hpp":
		return "C++ source"
	case ".java":
		return "Java source"
	case ".js", ".mjs", ".cjs":
		return "JavaScript"
	case ".ts":
		return "TypeScript"
	case ".css":
		return "CSS"
	case ".html", ".htm":
		return "HTML"
	case ".xml":
		return "XML"
	case ".json":
		return "JSON"
	case ".md", ".markdown":
		return "Markdown"
	case ".txt":
		return "plain text"
	case ".pem", ".crt", ".key", ".cert", ".cer":
		return "PEM certificate/key"
	case ".log":
		return "log file"
	case ".csv":
		return "CSV"
	case ".sql":
		return "SQL"
	}
	return "text"
}

// --- Builder helpers ---

func buildLayerHeaderV1(config *runtime.ContainerConfig, storage *runtime.ContainerStorage, rt *runtime.RuntimeProfile, imgConfig *runtime.ImageConfigInfo) string {
	backend := "-"
	rootfsPath := "-"
	readonly := "yes"

	if storage != nil && storage.Backend != nil {
		backend = formatLayerBackendV1(storage.Backend)
	} else if config != nil && config.Backend != nil {
		backend = formatLayerBackendV1(config.Backend)
	}
	if storage != nil {
		if storage.ReadOnly {
			readonly = "yes"
		} else {
			readonly = "no"
		}
	} else if config != nil {
		if config.SnapshotKey != "" || config.WritableLayerPath != "" {
			readonly = "no"
		}
	}
	if rt != nil && rt.RootFSPath != "" {
		rootfsPath = rt.RootFSPath
	}

	lines := []string{
		" " + components.KV("Layer Backend: ", backend),
		" " + components.KV("Rootfs Directory: ", rootfsPath),
		" " + components.KV("Readonly: ", readonly),
	}

	if imgConfig != nil && imgConfig.Manifest != nil {
		if platform := imageCurrentPlatform(imgConfig); platform != "" {
			lines = append(lines, " "+components.KV("Platform: ", platform))
		}
		if platforms := imagePlatformsSummary(imgConfig); platforms != "" {
			lines = append(lines, " "+components.KV("Platforms: ", platforms))
		}
		if imgConfig.IndexPath != "" {
			lines = append(lines, " "+components.KV("Index Path: ", imgConfig.IndexPath))
		}
		if imgConfig.TargetKind != "" {
			lines = append(lines, " "+components.KV("Manifest: ", imgConfig.TargetKind+" / "+imgConfig.Schema))
		}
	}

	return strings.Join(lines, "\n")
}

func formatLayerBackendV1(backend *runtime.LayerBackend) string {
	if backend == nil {
		return "-"
	}
	name := fallbackValue(backend.Name, "unknown")
	switch backend.Kind {
	case runtime.LayerBackendDockerGraphDriver:
		return "Docker Graph Driver / " + name
	case runtime.LayerBackendContainerdSnapshotter:
		return "Containerd Snapshotter / " + name
	case runtime.LayerBackendContainersStorage:
		return "Containers Storage Driver / " + name
	default:
		return name
	}
}

func buildRWLayerNodeV1(config *runtime.ContainerConfig, storage *runtime.ContainerStorage, rwStats *runtime.ContainerRWLayerStats, rwLayerPath string) *tview.TreeNode {
	node := components.NewTreeNode(fmt.Sprintf("[%s::b]RW Layer[-:-:-]", components.ColorName(components.ColorFgAccentAlt))).SetSelectable(true).SetExpanded(true)

	identifierLabel := "Identifier"
	identifierValue := "unknown"
	path := fallbackValue(rwLayerPath, "unknown")
	if config != nil {
		if config.SnapshotKey != "" {
			identifierLabel = "Snapshot Key"
			identifierValue = config.SnapshotKey
		}
		if config.WritableLayerPath != "" {
			path = config.WritableLayerPath
		}
	}
	if storage != nil {
		if storage.Containerd != nil && storage.Containerd.RWSnapshotKey != "" {
			identifierLabel = "Snapshot Key"
			identifierValue = storage.Containerd.RWSnapshotKey
		}
		if storage.Docker != nil {
			if storage.Docker.RWSnapshotKey != "" {
				identifierLabel = "Snapshot Key"
				identifierValue = storage.Docker.RWSnapshotKey
			} else if storage.Docker.RWLayerID != "" {
				identifierLabel = "Layer ID"
				identifierValue = storage.Docker.RWLayerID
			}
		}
		if storage.Crio != nil && storage.Crio.RWLayerID != "" {
			identifierLabel = "Layer ID"
			identifierValue = storage.Crio.RWLayerID
		}
		if path == "unknown" && storage.RWLayerPath != "" {
			path = storage.RWLayerPath
		}
	}

	rows := []string{
		identifierLabel + ": " + identifierValue,
		"Path: " + path,
	}
	if storage != nil {
		if storage.ReadOnly {
			rows = append(rows, "Read Only: yes")
		} else {
			rows = append(rows, "Read Only: no")
		}
	}
	selection := &layerTreeSelection{path: path, title: "RW Layer"}
	if rwStats != nil && (rwStats.RWLayerUsage > 0 || rwStats.RWLayerInodes > 0) {
		rows = append(rows, fmt.Sprintf("Disk Usage: %s (%d inodes)", formatBytes(rwStats.RWLayerUsage), rwStats.RWLayerInodes))
	} else {
		rows = append(rows, "Disk Usage: unknown")
	}

	for _, row := range rows {
		node.AddChild(components.NewTreeNode(fmt.Sprintf("  %s", components.Muted(row))).SetSelectable(true).SetReference(selection))
	}

	if path != "unknown" {
		node.SetReference(selection)
	}
	return node
}

func buildReadOnlyLayersNodeV1(layers []*runtime.ImageLayer) *tview.TreeNode {
	count := len(layers)
	node := components.NewTreeNode(components.Accent(fmt.Sprintf("Read-Only Layer (%d, top to base)", count))).
		SetSelectable(true).SetExpanded(true)

	if count == 0 {
		node.AddChild(components.NewTreeNode(components.Muted("No read-only image layers resolved")).SetSelectable(true))
		return node
	}

	sorted := make([]*runtime.ImageLayer, len(layers))
	copy(sorted, layers)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Index > sorted[j].Index })

	for _, layer := range sorted {
		snapshotKey := ""
		if layer.Containerd != nil {
			snapshotKey = layer.Containerd.SnapshotKey
		}
		label := fmt.Sprintf("Layer %d: %s", layer.Index, shortenLayerIDV1(snapshotKey, layer.UncompressedDigest, layer.CompressedDigest))
		layerNode := components.NewTreeNode(label).SetSelectable(true).SetExpanded(false)
		selection := &layerTreeSelection{path: layer.Path, title: label}
		layerNode.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Rootfs Diff ID:"), components.Bright(fallbackLayerField(layer.UncompressedDigest)))).SetSelectable(true).SetReference(selection))
		layerNode.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Path:"), components.Bright(fallbackLayerField(layer.Path)))).SetSelectable(true).SetReference(selection))
		layerNode.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Content Size:"), components.Bright(formatLayerSize(layer)))).SetSelectable(true).SetReference(selection))
		layerNode.AddChild(components.NewTreeNode(fmt.Sprintf("  %s %s", components.Muted("Disk Usage:"), components.Bright(formatLayerDiskUsage(layer)))).SetSelectable(true).SetReference(selection))
		for _, detailsNode := range buildLayerBackendDetailsV1(layer, selection) {
			layerNode.AddChild(detailsNode)
		}
		if layer.Path != "" {
			layerNode.SetReference(selection)
		}
		node.AddChild(layerNode)
	}
	return node
}

func buildLayerBackendDetailsV1(layer *runtime.ImageLayer, selection *layerTreeSelection) []*tview.TreeNode {
	if layer == nil {
		return nil
	}

	var nodes []*tview.TreeNode
	if layer.Containerd != nil {
		rows := make([]string, 0, 2)
		if layer.Containerd.SnapshotKey != "" {
			rows = append(rows, "Snapshot Key: "+layer.Containerd.SnapshotKey)
		}
		if layer.Containerd.ContentPath != "" {
			rows = append(rows, "Content Path: "+layer.Containerd.ContentPath)
		}
		if len(rows) > 0 {
			node := components.NewTreeNode(fmt.Sprintf("  %s", components.Accent("Containerd"))).SetSelectable(true).SetExpanded(true)
			for _, row := range rows {
				node.AddChild(components.NewTreeNode(fmt.Sprintf("    %s", components.Muted(row))).SetSelectable(true).SetReference(selection))
			}
			node.SetReference(selection)
			nodes = append(nodes, node)
		}
	}

	if layer.Docker != nil {
		rows := make([]string, 0, 4)
		if layer.Docker.CacheID != "" {
			rows = append(rows, "Cache ID: "+layer.Docker.CacheID)
		}
		if layer.Docker.ShortLinkID != "" {
			rows = append(rows, "Short Link ID: "+layer.Docker.ShortLinkID)
		}
		if layer.Docker.ShortLinkPath != "" {
			rows = append(rows, "Short Link Path: "+layer.Docker.ShortLinkPath)
		}
		if layer.Docker.GraphDriver != "" {
			rows = append(rows, "Graph Driver: "+layer.Docker.GraphDriver)
		}
		if len(rows) > 0 {
			node := components.NewTreeNode(fmt.Sprintf("  %s", components.Accent("Docker"))).SetSelectable(true).SetExpanded(true)
			for _, row := range rows {
				node.AddChild(components.NewTreeNode(fmt.Sprintf("    %s", components.Muted(row))).SetSelectable(true).SetReference(selection))
			}
			node.SetReference(selection)
			nodes = append(nodes, node)
		}
	}

	if layer.Crio != nil {
		rows := make([]string, 0, 3)
		if layer.Crio.ID != "" {
			rows = append(rows, "Layer ID: "+layer.Crio.ID)
		}
		if len(layer.Crio.Names) > 0 {
			rows = append(rows, "Names: "+strings.Join(layer.Crio.Names, ", "))
		}
		if layer.Crio.OverlayLinkID != "" {
			rows = append(rows, "Overlay Link ID: "+layer.Crio.OverlayLinkID)
		}
		if len(rows) > 0 {
			node := components.NewTreeNode(fmt.Sprintf("  %s", components.Accent("CRI-O"))).SetSelectable(true).SetExpanded(true)
			for _, row := range rows {
				node.AddChild(components.NewTreeNode(fmt.Sprintf("    %s", components.Muted(row))).SetSelectable(true).SetReference(selection))
			}
			node.SetReference(selection)
			nodes = append(nodes, node)
		}
	}

	return nodes
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
	return fallbackValue(value, "unresolved")
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
	if selection, ok := node.GetReference().(*layerTreeSelection); ok && selection != nil && selection.path != "" {
		return selection.path, selection.title
	}
	return "", ""
}

func findLayerNodeByPath(node *tview.TreeNode, path string) *tview.TreeNode {
	if node == nil || path == "" {
		return nil
	}
	if selection, ok := node.GetReference().(*layerTreeSelection); ok && selection != nil && selection.path == path {
		return node
	}
	for _, child := range node.GetChildren() {
		if found := findLayerNodeByPath(child, path); found != nil {
			return found
		}
	}
	return nil
}

func resolveBrowserRoot(rt *runtime.RuntimeProfile, containerPID uint32, layerPath string) (string, int, error) {
	if rt != nil && rt.RootFSPath != "" {
		if info, err := os.Stat(rt.RootFSPath); err == nil {
			if info.IsDir() {
				if empty, emptyErr := isDirectoryEmpty(rt.RootFSPath); emptyErr == nil {
					if !empty {
						return rt.RootFSPath, 0, nil
					}
					if procRoot := procRootPath(containerPID); procRoot != "" {
						if procInfo, procErr := os.Stat(procRoot); procErr == nil && procInfo.IsDir() {
							return procRoot, 2, nil
						}
					}
				} else {
					return "", 0, fmt.Errorf("unable to inspect merged rootfs: %v", emptyErr)
				}
			}
		}
	}

	if strings.TrimSpace(layerPath) == "" {
		return "", 0, fmt.Errorf("select a layer with a readable path before opening the browser")
	}
	return layerPath, 1, nil
}

func procRootPath(containerPID uint32) string {
	if containerPID == 0 {
		return ""
	}
	return filepath.Join("/proc", fmt.Sprintf("%d", containerPID), "root")
}

func isDirectoryEmpty(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer file.Close()

	_, err = file.Readdirnames(1)
	if err == io.EOF {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}
