package views

import (
	"bytes"
	"context"
	"encoding/json"
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

// runtimePathRef tags a tree node that points at a filesystem path so it can
// be previewed inline.
type runtimePathRef struct {
	Title string
	Path  string
}

// runtimeBrowserEntry is the per-node payload for the runtime preview
// browser tree. It mirrors the structure used by the layer browser so users
// can expand directories and inspect individual files.
type runtimeBrowserEntry struct {
	path    string
	isDir   bool
	loaded  bool
	isError bool
}

// previewMaxBytes caps the amount of file content loaded into the preview pane.
const runtimePreviewMaxBytes = 512 * 1024

// runtime preview focus targets.
const (
	runtimeFocusTree    = 0
	runtimeFocusBrowser = 1
	runtimeFocusContent = 2
)

// RuntimeInfoView renders the Runtime page.
type RuntimeInfoView struct {
	*tview.Flex

	app         *tview.Application
	body        *tview.Flex // tree | rightPane
	rightPane   *tview.Flex // browserTree / preview
	tree        *tview.TreeView
	browserTree *tview.TreeView
	preview     *tview.TextView
	statusBar   *tview.TextView
	container   runtime.Container
	previewOpen bool
	focus       int // runtimeFocus*
	currentRef  *runtimePathRef
	mu          sync.Mutex
}

// NewRuntimeInfoView creates a new runtime info view.
func NewRuntimeInfoView(app *tview.Application) *RuntimeInfoView {
	v := &RuntimeInfoView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	v.tree = tview.NewTreeView()
	components.InitTreeView(v.tree)
	v.tree.SetRoot(components.NewTreeNode(components.Muted("No runtime data")).SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})
	v.tree.SetChangedFunc(func(node *tview.TreeNode) {
		v.updatePreview(node)
	})

	v.preview = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.preview.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview"))).SetTitleAlign(tview.AlignLeft)
	v.preview.SetBackgroundColor(components.ColorBg)

	v.browserTree = tview.NewTreeView()
	components.InitTreeView(v.browserTree)
	v.browserTree.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.browserTree.SetTitle(fmt.Sprintf(" %s ", components.Accent("Browser"))).SetTitleAlign(tview.AlignLeft)
	v.browserTree.SetRoot(components.NewTreeNode(components.Muted("No path selected")).SetSelectable(false))
	v.browserTree.SetSelectedFunc(func(node *tview.TreeNode) { v.toggleBrowserNode(node) })
	v.browserTree.SetChangedFunc(func(node *tview.TreeNode) { v.renderBrowserPreview(node) })

	v.rightPane = tview.NewFlex().SetDirection(tview.FlexRow)
	v.rightPane.AddItem(v.browserTree, 0, 2, false)
	v.rightPane.AddItem(v.preview, 0, 3, false)

	v.body = tview.NewFlex().SetDirection(tview.FlexColumn)
	v.body.AddItem(v.tree, 0, 1, true)

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.body, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the container handle.
func (v *RuntimeInfoView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
	v.renderEmpty()
	v.updateStatusBar()
}

// Refresh loads runtime metadata from the container handle.
func (v *RuntimeInfoView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.renderEmpty()
		return nil
	}

	profile, err := c.Runtime(ctx)
	if err != nil {
		v.renderError(err)
		return err
	}

	config, _ := c.Config(ctx)
	state, _ := c.State(ctx)

	v.render(profile, config, state)
	v.updateStatusBar()
	return nil
}

func (v *RuntimeInfoView) renderError(err error) {
	queueUpdateDraw(v.app, func() {
		root := components.NewTreeNode(components.Accent("Runtime")).SetSelectable(false).SetExpanded(true)
		root.AddChild(components.NewTreeNode(fmt.Sprintf("[%s]Failed to load runtime: %v[-]", components.ColorName(components.ColorFgError), err)).SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		components.ApplyTreeFocusStyle(v.tree, true)
	})
}

// HandleInput processes tree interaction.
func (v *RuntimeInfoView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return event
	}
	if event.Rune() == 'p' || event.Rune() == 'P' {
		v.togglePreview()
		return nil
	}
	// Tab / Shift+Tab cycle focus between tree, browser tree and preview content
	// while the preview pane is open, so each pane can use its own keyboard
	// navigation (browser: arrows + Enter to expand; preview: PgUp/PgDn etc.).
	if v.previewOpen && (event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab) {
		v.cyclePreviewFocus(event.Key() == tcell.KeyBacktab)
		return nil
	}
	switch v.focus {
	case runtimeFocusBrowser:
		return v.handleBrowserInput(event)
	case runtimeFocusContent:
		// Forward all keys to the TextView so its scroll handlers run.
		return event
	}
	return components.HandleTreeInput(event, v.tree, v.expandAll, func(node *tview.TreeNode) {
		v.updatePreview(node)
	})
}

// handleBrowserInput routes keys for the right-pane browser tree.
func (v *RuntimeInfoView) handleBrowserInput(event *tcell.EventKey) *tcell.EventKey {
	switch event.Key() {
	case tcell.KeyEnter:
		v.toggleBrowserNode(v.browserTree.GetCurrentNode())
		return nil
	}
	switch event.Rune() {
	case 'e', 'E':
		v.toggleBrowserNode(v.browserTree.GetCurrentNode())
		return nil
	case 'a', 'A':
		if root := v.browserTree.GetRoot(); root != nil {
			// Lazy-load any unloaded directory descendants before expanding.
			v.loadAllBrowserChildren(root)
			components.ExpandAllNodes(root)
		}
		return nil
	}
	return event
}

// GetFocusPrimitive returns the current focus target for this view.
func (v *RuntimeInfoView) GetFocusPrimitive() tview.Primitive {
	if !v.previewOpen {
		return v.tree
	}
	switch v.focus {
	case runtimeFocusBrowser:
		return v.browserTree
	case runtimeFocusContent:
		return v.preview
	default:
		return v.tree
	}
}

// cyclePreviewFocus advances focus through tree → browser → content (or reverse).
func (v *RuntimeInfoView) cyclePreviewFocus(reverse bool) {
	if !v.previewOpen {
		v.focus = runtimeFocusTree
		return
	}
	step := 1
	if reverse {
		step = -1
	}
	v.focus = ((v.focus + step) + 3) % 3
	v.applyPreviewFocusStyle()
	if v.app != nil {
		v.app.SetFocus(v.GetFocusPrimitive())
	}
	v.updateStatusBar()
}

// applyPreviewFocusStyle highlights whichever pane currently owns focus.
func (v *RuntimeInfoView) applyPreviewFocusStyle() {
	components.ApplyTreeFocusStyle(v.tree, v.focus == runtimeFocusTree)
	if v.previewOpen {
		components.ApplyTreeFocusStyle(v.browserTree, v.focus == runtimeFocusBrowser)
	} else {
		components.ApplyTreeFocusStyle(v.browserTree, false)
	}
	v.browserTree.SetBorderColor(components.ColorFgBorder)
	v.preview.SetBorderColor(components.ColorFgBorder)
	if v.previewOpen {
		switch v.focus {
		case runtimeFocusBrowser:
			v.browserTree.SetBorderColor(components.ColorFgAccent)
		case runtimeFocusContent:
			v.preview.SetBorderColor(components.ColorFgAccent)
		}
	}
}

func (v *RuntimeInfoView) renderEmpty() {
	queueUpdateDraw(v.app, func() {
		root := components.NewTreeNode(components.Accent("Runtime")).SetSelectable(false).SetExpanded(true)
		root.AddChild(components.NewTreeNode(components.Muted("Refresh to resolve shim, OCI runtime and namespace metadata")).SetSelectable(false))
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		components.ApplyTreeFocusStyle(v.tree, true)
	})
}

func (v *RuntimeInfoView) render(rt *runtime.RuntimeProfile, config *runtime.ContainerConfig, state *runtime.ContainerState) {
	root := components.NewTreeNode(components.Accent("Runtime")).SetSelectable(false).SetExpanded(true)

	if rt == nil {
		root.AddChild(components.NewTreeNode(components.Muted("Runtime metadata unresolved")).SetSelectable(false))
	} else {
		root.AddChild(buildShimNodeV1(rt, state))
		root.AddChild(buildOCINodeV1(rt))
		root.AddChild(buildNamespaceNodeV1(config))
	}

	queueUpdateDraw(v.app, func() {
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
		components.ApplyTreeFocusStyle(v.tree, true)
		v.updatePreview(v.tree.GetCurrentNode())
	})
}

func (v *RuntimeInfoView) expandAll() {
	root := v.tree.GetRoot()
	components.ExpandAllNodes(root)
	v.tree.SetCurrentNode(root)
}

// togglePreview shows or hides the right-side browser + preview pane.
func (v *RuntimeInfoView) togglePreview() {
	v.previewOpen = !v.previewOpen
	v.body.Clear()
	v.body.AddItem(v.tree, 0, 1, true)
	if v.previewOpen {
		v.body.AddItem(v.rightPane, 0, 1, false)
		v.currentRef = nil
		v.updatePreview(v.tree.GetCurrentNode())
	} else {
		// Closing the preview returns focus to the main tree.
		v.focus = runtimeFocusTree
		if v.app != nil {
			v.app.SetFocus(v.tree)
		}
	}
	v.applyPreviewFocusStyle()
	v.updateStatusBar()
}

// updatePreview synchronises the right pane with the currently selected node
// in the main runtime tree. It is a no-op when the pane is hidden.
func (v *RuntimeInfoView) updatePreview(node *tview.TreeNode) {
	if !v.previewOpen {
		return
	}
	if node == nil {
		v.currentRef = nil
		v.resetBrowserMessage("Select a path field to browse its content.")
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(" " + components.Muted("Select a path field to preview its content."))
		v.preview.ScrollToBeginning()
		return
	}
	ref, ok := node.GetReference().(*runtimePathRef)
	if !ok || ref == nil || ref.Path == "" {
		v.currentRef = nil
		v.resetBrowserMessage("Selected node has no associated path.")
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(" " + components.Muted("Selected node has no associated path."))
		v.preview.ScrollToBeginning()
		return
	}
	// Avoid rebuilding the browser tree when the same path is reselected, so
	// the user's expansion state is preserved.
	if v.currentRef != nil && v.currentRef.Path == ref.Path && v.currentRef.Title == ref.Title {
		return
	}
	v.currentRef = ref
	v.rebuildBrowser(ref)
}

// resetBrowserMessage replaces the browser tree with a single non-selectable
// placeholder line.
func (v *RuntimeInfoView) resetBrowserMessage(msg string) {
	root := components.NewTreeNode(components.Muted(msg)).SetSelectable(false)
	v.browserTree.SetRoot(root).SetCurrentNode(root)
	v.browserTree.SetTitle(fmt.Sprintf(" %s ", components.Accent("Browser")))
}

// rebuildBrowser populates the browser tree with the path referenced by the
// current main-tree selection. Directories lazily expand on demand.
func (v *RuntimeInfoView) rebuildBrowser(ref *runtimePathRef) {
	info, err := os.Stat(ref.Path)
	if err != nil {
		root := components.NewTreeNode(fmt.Sprintf("[%s]%s[-]",
			components.ColorName(components.ColorFgError),
			fmt.Sprintf("stat %s: %v", ref.Path, err))).
			SetSelectable(true).SetReference(&runtimeBrowserEntry{path: ref.Path, isError: true})
		v.browserTree.SetRoot(root).SetCurrentNode(root)
		v.browserTree.SetTitle(fmt.Sprintf(" %s %s ", components.Accent(ref.Title), components.Muted(ref.Path)))
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err))
		v.preview.ScrollToBeginning()
		return
	}
	entry := &runtimeBrowserEntry{path: ref.Path, isDir: info.IsDir()}
	label := filepath.Base(ref.Path)
	if label == "" || label == "." {
		label = ref.Path
	}
	if entry.isDir {
		label += "/"
	}
	root := components.NewTreeNode(runtimeBrowserNodeText(label, entry.isDir)).
		SetSelectable(true).SetExpanded(true).SetReference(entry)
	if entry.isDir {
		v.loadBrowserChildren(root, entry)
	}
	v.browserTree.SetRoot(root).SetCurrentNode(root)
	v.browserTree.SetTitle(fmt.Sprintf(" %s %s ", components.Accent(ref.Title), components.Muted(ref.Path)))
	v.renderBrowserPreview(root)
}

// runtimeBrowserNodeText formats a browser tree node label.
func runtimeBrowserNodeText(name string, isDir bool) string {
	if isDir {
		return fmt.Sprintf("[%s]%s[-]", components.ColorName(components.ColorFgAccentAlt), name)
	}
	return name
}

// toggleBrowserNode expands/collapses a directory node or refreshes the file
// preview for a leaf node.
func (v *RuntimeInfoView) toggleBrowserNode(node *tview.TreeNode) {
	if node == nil {
		return
	}
	entry, _ := node.GetReference().(*runtimeBrowserEntry)
	if entry == nil || !entry.isDir {
		v.renderBrowserPreview(node)
		return
	}
	v.loadBrowserChildren(node, entry)
	node.SetExpanded(!node.IsExpanded())
	v.renderBrowserPreview(node)
}

// loadBrowserChildren lazily reads directory entries the first time a
// directory node is touched.
func (v *RuntimeInfoView) loadBrowserChildren(node *tview.TreeNode, entry *runtimeBrowserEntry) {
	if node == nil || entry == nil || !entry.isDir || entry.loaded {
		return
	}
	node.ClearChildren()
	entries, err := os.ReadDir(entry.path)
	if err != nil {
		node.AddChild(components.NewTreeNode(fmt.Sprintf("[%s]%v[-]",
			components.ColorName(components.ColorFgError), err)).SetSelectable(false))
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
		isDir := de.IsDir()
		// Resolve symlinks one level so the user can still drill into them
		// when they point at directories.
		if !isDir && de.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(childPath); err == nil && st.IsDir() {
				isDir = true
			}
		}
		name := de.Name()
		if isDir {
			name += "/"
		}
		childEntry := &runtimeBrowserEntry{path: childPath, isDir: isDir}
		childNode := components.NewTreeNode(runtimeBrowserNodeText(name, isDir)).
			SetSelectable(true).SetExpanded(false).SetReference(childEntry)
		node.AddChild(childNode)
	}
	entry.loaded = true
}

// loadAllBrowserChildren walks the browser tree, lazily loading every
// directory it encounters so an "expand all" can take effect.
func (v *RuntimeInfoView) loadAllBrowserChildren(node *tview.TreeNode) {
	if node == nil {
		return
	}
	if entry, ok := node.GetReference().(*runtimeBrowserEntry); ok && entry != nil && entry.isDir {
		v.loadBrowserChildren(node, entry)
	}
	for _, child := range node.GetChildren() {
		v.loadAllBrowserChildren(child)
	}
}

// renderBrowserPreview updates the preview pane to reflect the currently
// selected node in the browser tree.
func (v *RuntimeInfoView) renderBrowserPreview(node *tview.TreeNode) {
	if !v.previewOpen || node == nil {
		return
	}
	entry, _ := node.GetReference().(*runtimeBrowserEntry)
	if entry == nil {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(" " + components.Muted("No selection"))
		v.preview.ScrollToBeginning()
		return
	}
	if entry.isError {
		return
	}
	if entry.isDir {
		v.preview.SetTitle(fmt.Sprintf(" %s %s ", components.Accent("Directory"), components.Muted(entry.path)))
		v.preview.SetText(formatRuntimeDirSummary(entry.path))
		v.preview.ScrollToBeginning()
		return
	}
	v.preview.SetTitle(fmt.Sprintf(" %s %s ", components.Accent("Preview"), components.Muted(filepath.Base(entry.path))))
	v.preview.SetText(loadRuntimeFilePreview(entry.path))
	v.preview.ScrollToBeginning()
}

// formatRuntimeDirSummary returns a metadata header + sorted listing for path.
func formatRuntimeDirSummary(path string) string {
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err)
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	var b strings.Builder
	fmt.Fprintf(&b, " %s\n", components.Muted(fmt.Sprintf("Directory · %d entries", len(entries))))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		info, err := e.Info()
		if err == nil && !e.IsDir() {
			fmt.Fprintf(&b, " %s  %s\n", formatBytesPad(info.Size(), 10), name)
		} else {
			fmt.Fprintf(&b, " %s  %s\n", strings.Repeat(" ", 10), name)
		}
	}
	return b.String()
}

// formatBytesPad returns formatBytes left-padded to width for column alignment.
func formatBytesPad(n int64, width int) string {
	s := formatBytes(n)
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// loadRuntimeFilePreview reads a file and returns formatted preview text.
// JSON files are pretty-printed; binary content is suppressed.
func loadRuntimeFilePreview(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err)
	}
	if st.Size() > runtimePreviewMaxBytes {
		return fmt.Sprintf(" %s\n %s",
			components.KV("Size:", formatBytes(st.Size())),
			components.Muted(fmt.Sprintf("File too large to preview (limit %s)", formatBytes(runtimePreviewMaxBytes))))
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf(" [%s]open error: %v[-]", components.ColorName(components.ColorFgError), err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, runtimePreviewMaxBytes+1))
	if err != nil {
		return fmt.Sprintf(" [%s]read error: %v[-]", components.ColorName(components.ColorFgError), err)
	}
	if int64(len(data)) > runtimePreviewMaxBytes {
		data = data[:runtimePreviewMaxBytes]
	}
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return fmt.Sprintf(" %s (%s)", components.Muted("[binary content suppressed]"), formatBytes(st.Size()))
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err == nil {
			return pretty.String()
		}
	}
	if int64(len(data)) < st.Size() {
		return string(data) + fmt.Sprintf("\n\n %s", components.Muted(fmt.Sprintf("[truncated; showing %d of %d bytes]", len(data), st.Size())))
	}
	return string(data)
}

func (v *RuntimeInfoView) updateStatusBar() {
	previewHint := components.KeyHint("p", "open preview")
	if v.previewOpen {
		previewHint = components.KeyHint("p", "close preview")
	}
	hints := []string{
		components.KeyHint("e", "toggle"),
		components.KeyHint("a", "expand/collapse"),
		previewHint,
	}
	if v.previewOpen {
		switch v.focus {
		case runtimeFocusBrowser:
			hints = append(hints,
				components.KeyHint("Tab", "focus content"),
				components.KeyHint("Enter", "open dir/file"),
			)
		case runtimeFocusContent:
			hints = append(hints,
				components.KeyHint("Tab", "focus tree"),
				components.KeyHint("↑/↓ PgUp/PgDn", "scroll"),
			)
		default:
			hints = append(hints, components.KeyHint("Tab", "focus browser"))
		}
	}
	v.statusBar.SetText(fmt.Sprintf(
		" %s  |  %s",
		components.Muted("Runtime: shim, OCI runtime and namespace anchors"),
		strings.Join(hints, "  "),
	))
}

// runtimeFieldNode renders a "Key: value" leaf under the given parent. When
// path is non-empty, the node carries a runtimePathRef so the preview pane can
// load it.
func runtimeFieldNode(label, value, path string) *tview.TreeNode {
	text := fmt.Sprintf("  %s %s", components.Muted(label+":"), components.Bright(value))
	node := components.NewTreeNode(text).SetSelectable(true)
	if strings.TrimSpace(path) != "" {
		node.SetReference(&runtimePathRef{Title: label, Path: path})
	}
	return node
}

func buildShimNodeV1(rt *runtime.RuntimeProfile, state *runtime.ContainerState) *tview.TreeNode {
	node := components.NewTreeNode(fmt.Sprintf("[%s::b]Shim[-:-:-]", components.ColorName(components.ColorFgAccentAlt))).SetSelectable(true).SetExpanded(true)

	added := false
	if state != nil {
		if state.PID > 0 {
			node.AddChild(runtimeFieldNode("Task PID", fmt.Sprintf("%d", state.PID), ""))
			added = true
		}
		if state.PPID > 0 {
			node.AddChild(runtimeFieldNode("Shim PID", fmt.Sprintf("%d", state.PPID), ""))
			added = true
		}
	}

	if rt.Shim != nil {
		shim := rt.Shim
		if shim.BinaryPath != "" {
			node.AddChild(runtimeFieldNode("Binary", shim.BinaryPath, shim.BinaryPath))
			added = true
		}
		if shim.SocketAddress != "" {
			// Socket addresses are not regular files (often unix:// URIs); no preview.
			node.AddChild(runtimeFieldNode("Socket", shim.SocketAddress, ""))
			added = true
		}
		if len(shim.Cmdline) > 0 {
			node.AddChild(runtimeFieldNode("Command", strings.Join(shim.Cmdline, " "), ""))
			added = true
		}
		if shim.SandboxBundleDir != "" {
			node.AddChild(runtimeFieldNode("Sandbox Bundle", shim.SandboxBundleDir, shim.SandboxBundleDir))
			added = true
		}
	}

	if rt.Conmon != nil {
		conmon := rt.Conmon
		if conmon.PID > 0 {
			node.AddChild(runtimeFieldNode("Conmon PID", fmt.Sprintf("%d", conmon.PID), ""))
			added = true
		}
		if conmon.BinaryPath != "" {
			node.AddChild(runtimeFieldNode("Conmon Binary", conmon.BinaryPath, conmon.BinaryPath))
			added = true
		}
		if len(conmon.Cmdline) > 0 {
			node.AddChild(runtimeFieldNode("Conmon Command", strings.Join(conmon.Cmdline, " "), ""))
			added = true
		}
		if conmon.LogPath != "" {
			node.AddChild(runtimeFieldNode("Log Path", conmon.LogPath, conmon.LogPath))
			added = true
		}
	}

	if !added {
		node.AddChild(components.NewTreeNode(components.Muted("  Shim metadata unresolved")).SetSelectable(true))
	}
	return node
}

func buildOCINodeV1(rt *runtime.RuntimeProfile) *tview.TreeNode {
	node := components.NewTreeNode(components.Accent("OCI Runtime")).SetSelectable(true).SetExpanded(true)
	oci := rt.OCI
	if oci == nil {
		node.AddChild(components.NewTreeNode(components.Muted("  OCI runtime metadata unresolved")).SetSelectable(true))
		return node
	}

	added := false
	if oci.RuntimeName != "" {
		node.AddChild(runtimeFieldNode("Runtime Name", oci.RuntimeName, ""))
		added = true
	}
	if oci.RuntimeBinary != "" {
		node.AddChild(runtimeFieldNode("Runtime Binary", oci.RuntimeBinary, oci.RuntimeBinary))
		added = true
	}
	if oci.BundleDir != "" {
		node.AddChild(runtimeFieldNode("Bundle Dir", oci.BundleDir, oci.BundleDir))
		added = true
	}
	if oci.StateDir != "" {
		node.AddChild(runtimeFieldNode("State Dir", oci.StateDir, oci.StateDir))
		added = true
	}
	if oci.StatePath != "" {
		node.AddChild(runtimeFieldNode("State Path", oci.StatePath, oci.StatePath))
		added = true
	}
	if oci.ConfigPath != "" {
		node.AddChild(runtimeFieldNode("Config Path", oci.ConfigPath, oci.ConfigPath))
		added = true
	}
	if !added {
		node.AddChild(components.NewTreeNode(components.Muted("  OCI runtime metadata unresolved")).SetSelectable(true))
	}
	return node
}

func buildNamespaceNodeV1(config *runtime.ContainerConfig) *tview.TreeNode {
	node := components.NewTreeNode(components.Accent("Namespace")).SetSelectable(true).SetExpanded(true)
	if config == nil || len(config.Namespaces) == 0 {
		node.AddChild(components.NewTreeNode(components.Muted("  Namespace metadata unresolved")).SetSelectable(true))
		return node
	}

	keys := make([]string, 0, len(config.Namespaces))
	for k := range config.Namespaces {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		// Namespace values may be procfs symlinks (e.g. /proc/<pid>/ns/pid).
		// Their content is meaningless to preview; leave path empty.
		node.AddChild(runtimeFieldNode(k, config.Namespaces[k], ""))
	}
	return node
}
