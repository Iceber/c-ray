package views

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"

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

// previewMaxBytes caps the amount of file content loaded into the preview pane.
const runtimePreviewMaxBytes = 64 * 1024

// RuntimeInfoView renders the Runtime page.
type RuntimeInfoView struct {
	*tview.Flex

	app         *tview.Application
	body        *tview.Flex // tree | preview
	tree        *tview.TreeView
	preview     *tview.TextView
	statusBar   *tview.TextView
	container   runtime.Container
	previewOpen bool
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
	if event.Rune() == 'p' || event.Rune() == 'P' {
		v.togglePreview()
		return nil
	}
	return components.HandleTreeInput(event, v.tree, v.expandAll, func(node *tview.TreeNode) {
		v.updatePreview(node)
	})
}

// GetFocusPrimitive returns the tree focus target.
func (v *RuntimeInfoView) GetFocusPrimitive() tview.Primitive {
	return v.tree
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

// togglePreview shows or hides the preview pane next to the tree.
func (v *RuntimeInfoView) togglePreview() {
	v.previewOpen = !v.previewOpen
	v.body.Clear()
	v.body.AddItem(v.tree, 0, 1, true)
	if v.previewOpen {
		v.body.AddItem(v.preview, 0, 1, false)
		v.updatePreview(v.tree.GetCurrentNode())
	}
	v.updateStatusBar()
}

// updatePreview refreshes the preview pane content for the given tree node.
// It is a no-op when the preview pane is hidden.
func (v *RuntimeInfoView) updatePreview(node *tview.TreeNode) {
	if !v.previewOpen {
		return
	}
	if node == nil {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(" " + components.Muted("Select a path field to preview its content."))
		return
	}
	ref, ok := node.GetReference().(*runtimePathRef)
	if !ok || ref == nil || ref.Path == "" {
		v.preview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.preview.SetText(" " + components.Muted("Selected node has no associated path."))
		return
	}
	v.preview.SetTitle(fmt.Sprintf(" %s %s ", components.Accent(ref.Title), components.Muted(ref.Path)))
	v.preview.SetText(loadRuntimePathPreview(ref.Path))
	v.preview.ScrollToBeginning()
}

// loadRuntimePathPreview reads the file/dir at path and returns formatted
// preview content. For directories it returns a sorted entry listing; for
// regular files it returns the first runtimePreviewMaxBytes bytes (JSON
// pretty-printed when applicable).
func loadRuntimePathPreview(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Sprintf(" [%s]stat error: %v[-]", components.ColorName(components.ColorFgError), err)
	}
	if st.IsDir() {
		entries, err := os.ReadDir(path)
		if err != nil {
			return fmt.Sprintf(" [%s]readdir error: %v[-]", components.ColorName(components.ColorFgError), err)
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		var b strings.Builder
		fmt.Fprintf(&b, " %s\n", components.Muted(fmt.Sprintf("Directory · %d entries", len(entries))))
		for _, e := range entries {
			marker := " "
			if e.IsDir() {
				marker = "/"
			}
			fmt.Fprintf(&b, " %s%s\n", e.Name(), marker)
		}
		return b.String()
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Sprintf(" [%s]open error: %v[-]", components.ColorName(components.ColorFgError), err)
	}
	defer f.Close()
	buf := make([]byte, runtimePreviewMaxBytes)
	n, _ := f.Read(buf)
	data := buf[:n]
	// Refuse to render obvious binaries.
	if bytes.IndexByte(data, 0) >= 0 {
		return fmt.Sprintf(" %s (%d bytes shown)", components.Muted("[binary content suppressed]"), n)
	}
	// Pretty-print JSON when applicable.
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && (trimmed[0] == '{' || trimmed[0] == '[') {
		var pretty bytes.Buffer
		if err := json.Indent(&pretty, data, "", "  "); err == nil {
			return pretty.String()
		}
	}
	if int64(n) < st.Size() {
		return string(data) + fmt.Sprintf("\n\n %s", components.Muted(fmt.Sprintf("[truncated; showing %d of %d bytes]", n, st.Size())))
	}
	return string(data)
}

func (v *RuntimeInfoView) updateStatusBar() {
	previewHint := components.KeyHint("p", "open preview")
	if v.previewOpen {
		previewHint = components.KeyHint("p", "close preview")
	}
	v.statusBar.SetText(fmt.Sprintf(
		" %s  |  %s  %s  %s",
		components.Muted("Runtime: shim, OCI runtime and namespace anchors"),
		components.KeyHint("e", "toggle"),
		components.KeyHint("a", "expand/collapse"),
		previewHint,
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
