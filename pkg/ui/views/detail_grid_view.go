package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/sysinfo"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// DetailGridView implements the 4-column grid layout for the container Info tab,
// replacing the old dual-column DetailSummaryView.
//
// Layout:
//
//	┌──────────────┬───────────┬──────────────────────────────┬──────────────┐
//	│ Network+Stdio│ Namespace │ Processes + Cgroup + FS      │ Pod + Image  │
//	│   (20%)      │  (10%)    │       (50%)                  │   (20%)      │
//	└──────────────┴───────────┴──────────────────────────────┴──────────────┘
type DetailGridView struct {
	*tview.Flex

	app              *tview.Application
	container        runtime.Container
	containerHostPID uint32
	mu               sync.Mutex
	focusPane        detailGridFocusPane
	topContent       *tview.Flex

	// Column panels
	networkPanel *tview.TextView
	stdioPanel   *tview.TextView
	nsPanel      *tview.TextView
	processTree  *tview.TreeView
	cgroupPanel  *tview.TextView
	fsPanel      *tview.TreeView
	podPanel     *tview.TextView
	imagePanel   *tview.TreeView
	detailPanel  *tview.TextView
}

type detailGridFocusPane int

const (
	detailGridFocusProcesses detailGridFocusPane = iota
	detailGridFocusFilesystem
	detailGridFocusImage
)

type detailGridSelectionDetail struct {
	Title string
	Lines []string
}

type detailGridField struct {
	Key   string
	Value string
}

// NewDetailGridView creates a new 4-column grid info view.
func NewDetailGridView(app *tview.Application) *DetailGridView {
	v := &DetailGridView{
		Flex:      tview.NewFlex().SetDirection(tview.FlexRow),
		app:       app,
		focusPane: detailGridFocusProcesses,
	}
	v.topContent = tview.NewFlex().SetDirection(tview.FlexColumn)

	// --- Column 1: Network + Stdio (20%) ---
	v.networkPanel = newGridPanel("Network")
	v.stdioPanel = newGridPanel("Stdio")
	col1 := tview.NewFlex().SetDirection(tview.FlexRow)
	col1.AddItem(v.networkPanel, 0, 2, false)
	col1.AddItem(v.stdioPanel, 0, 1, false)

	// --- Column 2: Namespace (10%) ---
	v.nsPanel = newGridPanel("Namespace")
	col2 := tview.NewFlex().SetDirection(tview.FlexRow)
	col2.AddItem(v.nsPanel, 0, 1, false)

	// --- Column 3: Processes + Cgroup/Filesystem (50%) ---
	v.processTree = tview.NewTreeView()
	components.InitTreeView(v.processTree)
	v.processTree.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.processTree.SetTitle(fmt.Sprintf(" %s%s ", components.Accent("Processes"), components.Dim("(p)"))).SetTitleAlign(tview.AlignLeft)
	v.processTree.SetBackgroundColor(components.ColorBg)
	v.processTree.SetRoot(components.NewTreeNode(components.Muted("No data")))
	v.processTree.SetChangedFunc(func(node *tview.TreeNode) {
		v.updateDetailPanelForNode(detailGridFocusProcesses, node)
	})

	v.cgroupPanel = newGridPanel("Cgroup")
	v.fsPanel = tview.NewTreeView()
	components.InitTreeView(v.fsPanel)
	v.fsPanel.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.fsPanel.SetTitle(fmt.Sprintf(" %s%s ", components.Accent("Filesystem"), components.Dim("(f)"))).SetTitleAlign(tview.AlignLeft)
	v.fsPanel.SetBackgroundColor(components.ColorBg)
	v.fsPanel.SetRoot(components.NewTreeNode(components.Muted("No data")))
	v.fsPanel.SetChangedFunc(func(node *tview.TreeNode) {
		v.updateDetailPanelForNode(detailGridFocusFilesystem, node)
	})
	bottomRow := tview.NewFlex().SetDirection(tview.FlexColumn)
	bottomRow.AddItem(v.cgroupPanel, 0, 1, false)
	bottomRow.AddItem(v.fsPanel, 0, 1, false)

	col3 := tview.NewFlex().SetDirection(tview.FlexRow)
	col3.AddItem(v.processTree, 0, 2, true)
	col3.AddItem(bottomRow, 0, 3, false)

	// --- Column 4: Pod + Image (20%) ---
	v.podPanel = newGridPanel("Pod")
	v.imagePanel = tview.NewTreeView()
	components.InitTreeView(v.imagePanel)
	v.imagePanel.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.imagePanel.SetTitle(fmt.Sprintf(" %s%s ", components.Accent("Image"), components.Dim("(i)"))).SetTitleAlign(tview.AlignLeft)
	v.imagePanel.SetBackgroundColor(components.ColorBg)
	v.imagePanel.SetRoot(components.NewTreeNode(components.Muted("No data")))
	v.imagePanel.SetChangedFunc(func(node *tview.TreeNode) {
		v.updateDetailPanelForNode(detailGridFocusImage, node)
	})
	col4 := tview.NewFlex().SetDirection(tview.FlexRow)
	col4.AddItem(v.podPanel, 0, 1, false)
	col4.AddItem(v.imagePanel, 0, 1, false)

	// Assemble columns into the top horizontal grid.
	v.topContent.AddItem(col1, 0, 5, false) // 20%
	v.topContent.AddItem(col2, 0, 2, false) // 8%
	v.topContent.AddItem(col3, 0, 13, true) // 52%
	v.topContent.AddItem(col4, 0, 5, false) // 20%

	v.detailPanel = tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	v.detailPanel.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.detailPanel.SetTitle(fmt.Sprintf(" %s ", components.Accent("Detail"))).SetTitleAlign(tview.AlignLeft)
	v.detailPanel.SetBackgroundColor(components.ColorBg)
	v.detailPanel.SetText("  " + components.Muted("Select a process or filesystem field to inspect full values and extra context."))

	v.Flex.AddItem(v.topContent, 0, 1, true)
	v.Flex.AddItem(v.detailPanel, 7, 0, false)
	v.updateFocusStyles()

	return v
}

func newGridPanel(title string) *tview.TextView {
	tv := tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	tv.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	tv.SetTitle(fmt.Sprintf(" %s ", components.Accent(title))).SetTitleAlign(tview.AlignLeft)
	tv.SetBackgroundColor(components.ColorBg)
	return tv
}

// SetContainer sets the container handle.
func (v *DetailGridView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
}

// Refresh loads all panel data from the container handle.
func (v *DetailGridView) Refresh(ctx context.Context) {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.renderEmpty()
		return
	}

	info, _ := c.Info(ctx)
	if info != nil {
		v.mu.Lock()
		v.containerHostPID = info.PID
		v.mu.Unlock()
	}

	config, _ := c.Config(ctx)
	profile, _ := c.Runtime(ctx)
	netState, _ := c.Network(ctx)
	stdio, _ := c.Stdio(ctx)
	processes, _ := c.Processes(ctx)
	storage, _ := c.Storage(ctx)
	mounts, _ := c.Mounts(ctx)

	var imageInfo *runtime.ImageInfo
	var imageConfig *runtime.ImageConfigInfo
	if img, err := c.Image(ctx); err == nil && img != nil {
		imageInfo, _ = img.Info(ctx)
		imageConfig, _ = img.Config(ctx)
	}

	// Collect placeholder notes for fields not yet exposed by the runtime interface
	var placeholders []string

	networkText := buildGridNetworkPanel(netState)
	stdioText, sp := buildGridStdioPanel(stdio)
	placeholders = append(placeholders, sp...)
	nsText := buildGridNamespacePanel(config)
	cgroupText, cp := buildGridCgroupPanel(config)
	placeholders = append(placeholders, cp...)
	fsRoot := buildGridFilesystemTree(config, storage, mounts)
	podText := buildGridPodPanel(info, profile)
	processRoot := buildGridProcessTree(c, ctx, processes)

	// Append placeholder summary to cgroup panel (smallest panel, good anchor)
	if len(placeholders) > 0 {
		cgroupText += fmt.Sprintf("\n\n  %s\n  %s",
			components.Accent("⚠ Not yet in runtime:"),
			components.Muted(strings.Join(placeholders, ", ")))
	}

	count := len(processes)
	queueUpdateDraw(v.app, func() {
		v.networkPanel.SetText(networkText)
		v.stdioPanel.SetText(stdioText)
		v.nsPanel.SetText(nsText)
		v.cgroupPanel.SetText(cgroupText)
		v.fsPanel.SetRoot(fsRoot)
		currentFSNode := fsRoot
		if len(fsRoot.GetChildren()) > 0 {
			currentFSNode = fsRoot.GetChildren()[0]
			if len(currentFSNode.GetChildren()) > 0 {
				currentFSNode = currentFSNode.GetChildren()[0]
			}
		}
		v.fsPanel.SetCurrentNode(currentFSNode)
		v.podPanel.SetText(podText)
		_, _, imageWidth, _ := v.imagePanel.GetInnerRect()
		imageRoot := buildGridImageTree(config, imageInfo, imageConfig, imageWidth)
		v.imagePanel.SetRoot(imageRoot)
		currentImageNode := imageRoot
		if children := imageRoot.GetChildren(); len(children) > 0 {
			currentImageNode = children[0]
		}
		v.imagePanel.SetCurrentNode(currentImageNode)
		v.processTree.SetRoot(processRoot)
		v.processTree.SetCurrentNode(processRoot)
		v.processTree.SetTitle(fmt.Sprintf(" %s%s %s ",
			components.Accent("Processes"),
			components.Dim("(p)"),
			components.Muted(fmt.Sprintf("%d total", count))))
		if v.focusPane == detailGridFocusFilesystem {
			v.updateDetailPanelForNode(detailGridFocusFilesystem, currentFSNode)
		} else if v.focusPane == detailGridFocusImage {
			v.updateDetailPanelForNode(detailGridFocusImage, currentImageNode)
		} else {
			v.updateDetailPanelForNode(detailGridFocusProcesses, processRoot)
		}
		v.updateFocusStyles()
	})
}

// GetFocusPrimitive returns the focusable primitive (process tree).
func (v *DetailGridView) GetFocusPrimitive() tview.Primitive {
	switch v.focusPane {
	case detailGridFocusFilesystem:
		return v.fsPanel
	case detailGridFocusImage:
		return v.imagePanel
	default:
		return v.processTree
	}
}

// HandleInput processes local keybindings.
func (v *DetailGridView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Rune() {
	case 'p', 'P':
		v.focusPane = detailGridFocusProcesses
		if v.app != nil {
			v.app.SetFocus(v.processTree)
		}
		v.updateFocusStyles()
		v.updateDetailPanelForNode(detailGridFocusProcesses, v.processTree.GetCurrentNode())
		return nil
	case 'f', 'F':
		v.focusPane = detailGridFocusFilesystem
		if v.app != nil {
			v.app.SetFocus(v.fsPanel)
		}
		v.updateFocusStyles()
		v.updateDetailPanelForNode(detailGridFocusFilesystem, v.fsPanel.GetCurrentNode())
		return nil
	case 'i', 'I':
		v.focusPane = detailGridFocusImage
		if v.app != nil {
			v.app.SetFocus(v.imagePanel)
		}
		v.updateFocusStyles()
		v.updateDetailPanelForNode(detailGridFocusImage, v.imagePanel.GetCurrentNode())
		return nil
	}
	switch v.focusPane {
	case detailGridFocusFilesystem:
		return components.HandleTreeInput(event, v.fsPanel, nil, nil)
	case detailGridFocusImage:
		return components.HandleTreeInput(event, v.imagePanel, nil, nil)
	default:
		return components.HandleTreeInput(event, v.processTree, v.expandAll, nil)
	}
}

func (v *DetailGridView) expandAll() {
	root := v.processTree.GetRoot()
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

func (v *DetailGridView) renderEmpty() {
	queueUpdateDraw(v.app, func() {
		empty := components.Muted("  Loading...")
		v.networkPanel.SetText(empty)
		v.stdioPanel.SetText(empty)
		v.nsPanel.SetText(empty)
		v.cgroupPanel.SetText(empty)
		fsRoot := components.NewTreeNode(components.Muted("Loading...")).SetSelectable(false)
		v.fsPanel.SetRoot(fsRoot)
		v.fsPanel.SetCurrentNode(fsRoot)
		v.podPanel.SetText(empty)
		imageRoot := components.NewTreeNode(components.Muted("Loading...")).SetSelectable(false)
		v.imagePanel.SetRoot(imageRoot)
		v.imagePanel.SetCurrentNode(imageRoot)
		v.processTree.SetRoot(components.NewTreeNode(components.Muted("Loading...")))
		v.detailPanel.SetText(empty)
		v.updateFocusStyles()
	})
}

func (v *DetailGridView) updateFocusStyles() {
	components.ApplyTreeFocusStyle(v.processTree, v.focusPane == detailGridFocusProcesses)
	components.ApplyTreeFocusStyle(v.fsPanel, v.focusPane == detailGridFocusFilesystem)
	components.ApplyTreeFocusStyle(v.imagePanel, v.focusPane == detailGridFocusImage)
}

func (v *DetailGridView) updateDetailPanelForNode(pane detailGridFocusPane, node *tview.TreeNode) {
	if v.detailPanel == nil || pane != v.focusPane {
		return
	}
	var title string
	var lines []string
	switch pane {
	case detailGridFocusFilesystem:
		title, lines = filesystemNodeDetail(node)
	case detailGridFocusImage:
		title, lines = imageNodeDetail(node)
	default:
		v.mu.Lock()
		hostPID := v.containerHostPID
		v.mu.Unlock()
		title, lines = processNodeDetail(node, hostPID)
	}
	v.detailPanel.SetTitle(fmt.Sprintf(" %s ", components.Accent(title)))
	v.detailPanel.SetText(strings.Join(lines, "\n"))
}

func processNodeDetail(node *tview.TreeNode, containerHostPID uint32) (string, []string) {
	if node == nil {
		return "Detail", []string{"  " + components.Muted("No process selected")}
	}
	process, ok := node.GetReference().(*runtime.Process)
	if !ok || process == nil {
		return "Process Detail", []string{
			"  " + components.Muted("Select a concrete process entry to inspect full values."),
		}
	}

	lines := []string{
		gridKV("Command", processCommandLine(process)),
	}

	threads := "-"
	fdSummary := "-"
	portsSummary := "-"

	if containerHostPID > 0 {
		procRoot := fmt.Sprintf("/proc/%d/root/proc", containerHostPID)
		reader := sysinfo.NewProcReaderWithRoot(procRoot)

		if n, err := reader.ReadThreadCount(process.PID); err == nil {
			threads = fmt.Sprintf("%d", n)
		}

		if fdMap, err := reader.ReadOpenFDsSummary(process.PID); err == nil {
			fdSummary = formatFDSummary(fdMap)
		}

		// Listening ports are network-namespace-wide; read from the container's
		// host PID which shares the same network namespace as all its processes.
		hostReader := sysinfo.NewProcReader()
		if ports, err := hostReader.ReadListeningPorts(int(containerHostPID)); err == nil {
			if len(ports) == 0 {
				portsSummary = "none"
			} else {
				portsSummary = strings.Join(ports, ", ")
			}
		}
	}

	lines = append(lines,
		gridKV("Threads", threads),
		gridKV("Open FDs", fdSummary),
		gridKV("Ports", portsSummary),
	)
	return "Process Detail", lines
}

// formatFDSummary converts a fd-type→count map to a compact string like
// "10(file) + 3(socket) + 2(pipe)".
func formatFDSummary(fdMap map[string]int) string {
	if len(fdMap) == 0 {
		return "0"
	}
	// Known types listed first in a fixed order, then everything else sorted.
	priority := []string{"file", "socket", "pipe"}
	seen := make(map[string]bool)
	var parts []string
	for _, k := range priority {
		if v, ok := fdMap[k]; ok {
			parts = append(parts, fmt.Sprintf("%d(%s)", v, k))
			seen[k] = true
		}
	}
	var others []string
	for k := range fdMap {
		if !seen[k] {
			others = append(others, k)
		}
	}
	sort.Strings(others)
	for _, k := range others {
		parts = append(parts, fmt.Sprintf("%d(%s)", fdMap[k], k))
	}
	return strings.Join(parts, " + ")
}

func filesystemNodeDetail(node *tview.TreeNode) (string, []string) {
	if node == nil {
		return "Detail", []string{"  " + components.Muted("No filesystem field selected")}
	}
	if detail, ok := node.GetReference().(*detailGridSelectionDetail); ok && detail != nil {
		return detail.Title, detail.Lines
	}
	return "Filesystem Detail", []string{
		"  " + components.Muted("Select a concrete filesystem field to inspect full values."),
	}
}

func imageNodeDetail(node *tview.TreeNode) (string, []string) {
	if node == nil {
		return "Detail", []string{"  " + components.Muted("No image field selected")}
	}
	if detail, ok := node.GetReference().(*detailGridSelectionDetail); ok && detail != nil {
		return detail.Title, detail.Lines
	}
	return "Image Detail", []string{
		"  " + components.Muted("Select Name or ID to inspect image paths and platforms."),
	}
}

func detailField(key, value string) detailGridField {
	return detailGridField{Key: key, Value: fallbackValue(value, "-")}
}

func detailLines(fields ...detailGridField) []string {
	if len(fields) == 0 {
		return []string{"  " + components.Muted("No detail available")}
	}
	if len(fields) > 4 {
		fields = fields[:4]
	}
	lines := make([]string, 0, len(fields))
	for _, field := range fields {
		lines = append(lines, gridKV(field.Key, field.Value))
	}
	return lines
}

func processCommandLine(process *runtime.Process) string {
	if process == nil {
		return "-"
	}
	parts := make([]string, 0, 1+len(process.Args))
	if process.Command != "" {
		parts = append(parts, process.Command)
	} else {
		parts = append(parts, processName(process))
	}
	parts = append(parts, process.Args...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

// ──────────────────────────────────────────────
// Panel builders
// ──────────────────────────────────────────────

func buildGridNetworkPanel(net *runtime.ContainerNetworkState) string {
	var lines []string

	if net == nil || net.PodNetwork == nil {
		lines = append(lines, gridKV("Interfaces", "-"))
		lines = append(lines, gridKV("Port Mappings", "-"))
		return strings.Join(lines, "\n")
	}

	pn := net.PodNetwork

	// Interfaces
	if len(pn.ObservedInterfaces) > 0 {
		lines = append(lines, fmt.Sprintf("  [%s::b]Interfaces[-:-:-]", components.ColorName(components.ColorFgAccent)))
		for _, iface := range pn.ObservedInterfaces {
			lines = append(lines, fmt.Sprintf("   %s", components.Bright(iface.Interface)))
		}
	} else {
		lines = append(lines, gridKV("Interfaces", "-"))
	}

	lines = append(lines, "")

	// Port mappings
	if len(pn.PortMappings) > 0 {
		lines = append(lines, fmt.Sprintf("  [%s::b]Port Mappings[-:-:-]", components.ColorName(components.ColorFgAccent)))
		for _, pm := range pn.PortMappings {
			host := fmt.Sprintf("%d", pm.HostPort)
			if pm.HostIP != "" {
				host = fmt.Sprintf("%s:%d", pm.HostIP, pm.HostPort)
			}
			proto := pm.Protocol
			if proto == "" {
				proto = "tcp"
			}
			lines = append(lines, fmt.Sprintf("   %s → %s/%s",
				components.Bright(host),
				components.Accent(fmt.Sprintf("%d", pm.ContainerPort)),
				components.Muted(proto)))
		}
	} else {
		lines = append(lines, gridKV("Port Mappings", "none"))
	}

	return strings.Join(lines, "\n")
}

func buildGridStdioPanel(stdio *runtime.ContainerStdio) (string, []string) {
	if stdio == nil {
		placeholders := []string{"Stdio.stdin", "Stdio.stdout", "Stdio.stderr"}
		lines := []string{
			gridKV("stdin", components.Muted("(not available)")),
			gridKV("stdout", components.Muted("(not available)")),
			gridKV("stderr", components.Muted("(not available)")),
		}
		return strings.Join(lines, "\n"), placeholders
	}

	var lines []string

	// TTY & Stdin status line.
	ttyStr := components.Muted("unknown")
	if stdio.TTY != nil {
		if *stdio.TTY {
			ttyStr = components.Bright("yes")
		} else {
			ttyStr = "no"
		}
	}
	lines = append(lines, gridKV("TTY", ttyStr))

	stdinStr := components.Muted("unknown")
	if stdio.OpenStdin != nil {
		if *stdio.OpenStdin {
			stdinStr = components.Bright("open")
			if stdio.StdinOnce != nil && *stdio.StdinOnce {
				stdinStr += components.Muted(" (once)")
			}
		} else {
			stdinStr = "closed"
		}
	}
	lines = append(lines, gridKV("Stdin", stdinStr))
	lines = append(lines, "")

	// Log path.
	if stdio.LogPath != "" {
		lines = append(lines, gridKV("Log", truncatePath(stdio.LogPath, 40)))
	}

	lines = append(lines, "")

	// Stream endpoints.
	renderEndpoint := func(name string, ep *string) {
		if ep == nil || *ep == "" {
			lines = append(lines, gridKV(name, components.Muted("-")))
			return
		}
		lines = append(lines, gridKV(name, truncatePath(*ep, 35)))
	}

	renderEndpoint("stdin", stdio.Stdin)
	renderEndpoint("stdout", stdio.Stdout)
	renderEndpoint("stderr", stdio.Stderr)

	// Attach info.
	if stdio.Attach != nil {
		lines = append(lines, "")
		lines = append(lines, gridKV("Attach", components.Bright("yes")))
		if stdio.Attach.Socket != "" {
			lines = append(lines, gridKV("Socket", truncatePath(stdio.Attach.Socket, 35)))
		}
	}

	return strings.Join(lines, "\n"), nil
}

// truncatePath shortens a file path for display if it exceeds maxLen.
func truncatePath(p string, maxLen int) string {
	if len(p) <= maxLen {
		return p
	}
	return "…" + p[len(p)-maxLen+1:]
}

// buildGridNamespacePanel renders the namespace indicator panel.
//
// Namespace status rules (based on ContainerConfig.Namespaces map[string]string):
//   - Key exists, value is non-empty path → Pod shared (cyan ●)
//   - Key exists, value is empty string   → Container private (green ●)
//   - Key does NOT exist                  → Host shared (orange ●)
func buildGridNamespacePanel(config *runtime.ContainerConfig) string {
	nsTypes := []string{"mount", "uts", "ipc", "pid", "network", "user", "cgroup", "time"}
	displayNames := map[string]string{
		"mount": "Mount", "uts": "UTS", "ipc": "IPC", "pid": "PID",
		"network": "Network", "user": "User", "cgroup": "Cgroup", "time": "Time",
	}
	type namespaceGroup struct {
		title string
	}
	type namespaceEntry struct {
		name     string
		display  string
		color    tcell.Color
		sortRank int
	}
	groups := map[int]namespaceGroup{
		0: {title: "Private"},
		1: {title: "Pod Shared"},
		2: {title: "Host Shared"},
	}

	var lines []string

	if config == nil || config.Namespaces == nil {
		// No config at all — can't determine, show dimmed section title
		lines = append(lines, "  "+components.Dim("Unknown"))
		lines = append(lines, "")
		for _, ns := range nsTypes {
			lines = append(lines, fmt.Sprintf("  [%s]●[-] %s",
				components.ColorName(components.ColorFgMuted),
				displayNames[ns]))
		}
		return strings.Join(lines, "\n")
	}

	entries := make([]namespaceEntry, 0, len(nsTypes))
	for _, ns := range nsTypes {
		display := displayNames[ns]
		path, exists := config.Namespaces[ns]

		entry := namespaceEntry{name: ns, display: display}

		switch {
		case exists && path == "":
			// Key present, empty path → Container private
			entry.color = components.ColorNsPrivate
			entry.sortRank = 0
		case exists && path != "":
			// Key present with path → Pod shared
			entry.color = components.ColorNsPod
			entry.sortRank = 1
		default:
			// Key missing → Host shared
			entry.color = components.ColorNsHost
			entry.sortRank = 2
		}

		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].sortRank != entries[j].sortRank {
			return entries[i].sortRank < entries[j].sortRank
		}
		return entries[i].name < entries[j].name
	})

	currentRank := -1
	for _, entry := range entries {
		if entry.sortRank != currentRank {
			if currentRank != -1 {
				lines = append(lines, "")
			}
			group := groups[entry.sortRank]
			lines = append(lines, "  "+components.Dim(group.title))
			currentRank = entry.sortRank
		}
		lines = append(lines, fmt.Sprintf("  [%s]●[-] %s",
			components.ColorName(entry.color), entry.display))
	}

	return strings.Join(lines, "\n")
}

func buildGridProcessTree(c runtime.Container, ctx context.Context, processes []*runtime.Process) *tview.TreeNode {
	rootLabel := processTreeRootLabel(c, ctx)
	rootNode := components.NewTreeNode(rootLabel).SetSelectable(true).SetExpanded(true)

	if len(processes) == 0 {
		rootNode.AddChild(components.NewTreeNode(components.Muted("no processes")).SetSelectable(true))
		return rootNode
	}

	// Build parent lookup
	procMap := make(map[int]*runtime.Process)
	for _, p := range processes {
		procMap[p.PID] = p
	}
	var roots []*runtime.Process
	for _, p := range processes {
		if _, hasParent := procMap[p.PPID]; !hasParent {
			roots = append(roots, p)
		}
	}
	sort.Slice(roots, func(i, j int) bool { return roots[i].PID < roots[j].PID })

	childMap := make(map[int][]*runtime.Process)
	for _, p := range processes {
		childMap[p.PPID] = append(childMap[p.PPID], p)
	}

	for _, root := range roots {
		rootNode.AddChild(buildProcessNode(root, childMap))
	}

	return rootNode
}

func buildGridCgroupPanel(config *runtime.ContainerConfig) (string, []string) {
	var lines []string
	var placeholders []string

	if config != nil {
		lines = append(lines, gridKV("Driver", fallbackValue(config.CGroupDriver, "-")))
		if config.CGroupVersion > 0 {
			lines = append(lines, gridKV("Version", fmt.Sprintf("v%d", config.CGroupVersion)))
		} else {
			lines = append(lines, gridKV("Version", "-"))
		}
		lines = append(lines, gridKVBlock("Path", fallbackValue(config.CGroupPath, "-")))
	} else {
		lines = append(lines, gridKV("Driver", "-"))
		lines = append(lines, gridKV("Version", "-"))
		lines = append(lines, gridKVBlock("Path", "-"))
	}

	// CPU / Memory / PIDs limits and usage are NOT available in current runtime interface.
	placeholders = append(placeholders,
		"Cgroup.CPULimit", "Cgroup.CPUUsage",
		"Cgroup.MemoryLimit", "Cgroup.MemoryUsage",
		"Cgroup.PIDsLimit", "Cgroup.PIDsCurrent")
	lines = append(lines, gridKVBlock("CPU", components.Muted("(not available)")))
	lines = append(lines, gridKVBlock("Memory", components.Muted("(not available)")))
	lines = append(lines, gridKVBlock("PIDs", components.Muted("(not available)")))

	return joinGridBlocks(lines), placeholders
}

func buildGridFilesystemTree(config *runtime.ContainerConfig, storage *runtime.ContainerStorage, mounts []*runtime.Mount) *tview.TreeNode {
	root := components.NewTreeNode(components.Accent("Filesystem")).SetSelectable(false).SetExpanded(true)

	root.AddChild(buildFilesystemMountsNode(mounts))
	root.AddChild(buildFilesystemRootfsNode(config, storage))
	return root
}

func buildGridPodPanel(info *runtime.ContainerInfo, rt *runtime.RuntimeProfile) string {
	var lines []string

	if info == nil || (info.PodName == "" && info.PodNamespace == "" && info.PodUID == "") {
		lines = append(lines, gridKVBlock("Pod", components.Muted("(not in a pod)")))
		return joinGridBlocks(lines)
	}

	lines = append(lines, gridKVBlock("Name", fallbackValue(info.PodName, "-")))
	lines = append(lines, gridKVBlock("Namespace", fallbackValue(info.PodNamespace, "-")))
	lines = append(lines, gridKVBlock("UID", fallbackValue(info.PodUID, "-")))

	sandboxID := resolveSandboxID(rt)
	lines = append(lines, gridKVBlock("Sandbox", fallbackValue(sandboxID, "-")))

	return joinGridBlocks(lines)
}

func buildGridImageTree(config *runtime.ContainerConfig, imgInfo *runtime.ImageInfo, imgConfig *runtime.ImageConfigInfo, width int) *tview.TreeNode {
	root := components.NewTreeNode(components.Accent("Image")).SetSelectable(false).SetExpanded(true)

	name := imagePanelName(config, imgInfo)
	id := imagePanelID(imgInfo)
	fullID := imageDetailFullID(imgInfo)
	size := "-"
	if imgInfo != nil && imgInfo.Size > 0 {
		size = formatBytes(imgInfo.Size)
	}
	kindSchema := imageKindSchemaSummary(imgConfig)

	detail := buildGridImageDetail(name, fullID, imageDetailOtherNames(config, imgInfo), imgConfig)
	root.AddChild(imageLeafNode("Name", name, true, detail, width, true))
	root.AddChild(imageLeafNode("ID", id, true, detail, width, false))
	root.AddChild(imageLeafNode("Kind/Schema", kindSchema, true, detail, width, false))
	root.AddChild(imageLeafNode("Size", size, false, nil, width, false))
	return root
}

func imageLeafNode(key, value string, selectable bool, detail *detailGridSelectionDetail, width int, wrapValue bool) *tview.TreeNode {
	node := components.NewTreeNode(imageLeafText(key, value, width, wrapValue)).SetSelectable(selectable)
	if detail != nil {
		node.SetReference(detail)
	}
	return node
}

func imageLeafText(key, value string, width int, wrapValue bool) string {
	value = fallbackValue(value, "-")
	if !wrapValue {
		return fmt.Sprintf("[%s]%s[-] [%s]%s[-]",
			components.ColorName(components.ColorFgMuted), key+":",
			components.ColorName(components.ColorFgBright), value,
		)
	}
	wrapped := wrapImageValue(value, width-len(key)-4)
	if len(wrapped) == 0 {
		wrapped = []string{"-"}
	}
	lines := []string{fmt.Sprintf("[%s]%s[-] [%s]%s[-]",
		components.ColorName(components.ColorFgMuted), key+":",
		components.ColorName(components.ColorFgBright), wrapped[0],
	)}
	indent := strings.Repeat(" ", len(key)+2)
	for _, line := range wrapped[1:] {
		lines = append(lines, fmt.Sprintf("[%s]%s[-] [%s]%s[-]",
			components.ColorName(components.ColorFgMuted), indent,
			components.ColorName(components.ColorFgBright), line,
		))
	}
	return strings.Join(lines, "\n")
}

func wrapImageValue(value string, width int) []string {
	if width < 8 {
		width = 8
	}
	runes := []rune(value)
	if len(runes) <= width {
		return []string{value}
	}
	lines := make([]string, 0, (len(runes)/width)+1)
	for start := 0; start < len(runes); start += width {
		end := start + width
		if end > len(runes) {
			end = len(runes)
		}
		lines = append(lines, string(runes[start:end]))
	}
	return lines
}

func imagePanelName(config *runtime.ContainerConfig, imgInfo *runtime.ImageInfo) string {
	if config != nil && strings.TrimSpace(config.ImageName) != "" {
		return config.ImageName
	}
	if imgInfo != nil && len(imgInfo.Names) > 0 && strings.TrimSpace(imgInfo.Names[0]) != "" {
		return imgInfo.Names[0]
	}
	return "-"
}

func imagePanelID(imgInfo *runtime.ImageInfo) string {
	if imgInfo == nil || strings.TrimSpace(imgInfo.Digest) == "" {
		return "-"
	}
	return shortID(imgInfo.Digest)
}

func imageDetailFullID(imgInfo *runtime.ImageInfo) string {
	if imgInfo == nil {
		return "-"
	}
	if value := strings.TrimSpace(imgInfo.Digest); value != "" {
		return value
	}
	return "-"
}

func imageDetailOtherNames(config *runtime.ContainerConfig, imgInfo *runtime.ImageInfo) string {
	if imgInfo == nil || len(imgInfo.Names) == 0 {
		return "-"
	}
	primary := imagePanelName(config, imgInfo)
	others := make([]string, 0, len(imgInfo.Names))
	seen := make(map[string]bool)
	for _, name := range imgInfo.Names {
		name = strings.TrimSpace(name)
		if name == "" || name == primary || seen[name] {
			continue
		}
		seen[name] = true
		others = append(others, name)
	}
	if len(others) == 0 {
		return "-"
	}
	return strings.Join(others, ", ")
}

func imageKindSchemaSummary(imgConfig *runtime.ImageConfigInfo) string {
	if imgConfig == nil {
		return "-"
	}
	kind := strings.TrimSpace(imgConfig.TargetKind)
	schema := strings.TrimSpace(imgConfig.Schema)
	value := strings.TrimSpace(kind + " / " + schema)
	value = strings.Trim(value, " /")
	if value == "" {
		return "-"
	}
	return value
}

func buildGridImageDetail(name, fullID, otherNames string, imgConfig *runtime.ImageConfigInfo) *detailGridSelectionDetail {
	manifestPath := "-"
	configPath := "-"
	if imgConfig != nil && imgConfig.Manifest != nil {
		manifestPath = fallbackValue(imgConfig.Manifest.Path, "-")
		configPath = fallbackValue(imgConfig.Manifest.ConfigPath, "-")
	}
	lines := []string{
		gridKV("Name", fallbackValue(name, "-")),
		gridKV("Full ID", fallbackValue(fullID, "-")),
		gridKV("Other Names", fallbackValue(otherNames, "-")),
		gridKV("Index Path", fallbackValue(imageIndexPath(imgConfig), "-")),
		gridKV("Manifest Path", manifestPath),
		gridKV("Config Path", configPath),
		gridKV("Platforms", imageSupportedPlatformsSummary(imgConfig)),
	}
	return &detailGridSelectionDetail{Title: "Image Detail", Lines: lines}
}

func imageIndexPath(imgConfig *runtime.ImageConfigInfo) string {
	if imgConfig == nil {
		return ""
	}
	return strings.TrimSpace(imgConfig.IndexPath)
}

func imageSupportedPlatformsSummary(imgConfig *runtime.ImageConfigInfo) string {
	if imgConfig == nil {
		return "-"
	}
	current := imageCurrentPlatform(imgConfig)
	manifestList := imgConfig.Manifests
	if len(manifestList) == 0 && imgConfig.Manifest != nil {
		manifestList = []*runtime.ImageManifest{imgConfig.Manifest}
	}
	if len(manifestList) == 0 {
		return "-"
	}
	seen := make(map[string]bool)
	parts := make([]string, 0, len(manifestList))
	for _, manifest := range manifestList {
		platform := "(unknown)"
		rawPlatform := ""
		if manifest != nil {
			rawPlatform = strings.TrimSpace(manifest.Platform)
			if rawPlatform != "" {
				platform = rawPlatform
			}
		}
		if seen[platform] {
			continue
		}
		seen[platform] = true
		if rawPlatform != "" && rawPlatform == current {
			parts = append(parts, fmt.Sprintf("[%s::b]%s[-:-:-]%s",
				components.ColorName(components.ColorFgAccentAlt),
				platform,
				components.Muted("(current)"),
			))
			continue
		}
		parts = append(parts, components.Bright(platform))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

// gridKV renders a compact key-value pair for grid panels.
func gridKV(key, value string) string {
	return fmt.Sprintf("  [%s]%-12s[-] [%s]%s[-]",
		components.ColorName(components.ColorFgMuted), key,
		components.ColorName(components.ColorFgBright), value)
}

func gridKVBlock(key, value string) string {
	return fmt.Sprintf("  [%s]%s[-]\n    [%s]%s[-]",
		components.ColorName(components.ColorFgMuted), key,
		components.ColorName(components.ColorFgBright), value)
}

func layerTreeID(layer *runtime.ImageLayer) string {
	if layer == nil {
		return "-"
	}
	if layer.CompressedDigest != "" {
		return layer.CompressedDigest
	}
	if layer.UncompressedDigest != "" {
		return layer.UncompressedDigest
	}
	if layer.Crio != nil && layer.Crio.ID != "" {
		return layer.Crio.ID
	}
	if layer.Docker != nil && layer.Docker.CacheID != "" {
		return layer.Docker.CacheID
	}
	if layer.Containerd != nil && layer.Containerd.SnapshotKey != "" {
		return layer.Containerd.SnapshotKey
	}
	return "-"
}

func buildFilesystemMountsNode(mounts []*runtime.Mount) *tview.TreeNode {
	rootMount, criMounts, runtimeMounts, otherMounts := splitMounts(mounts)
	rootTarget := "/"
	if rootMount != nil && rootMount.Destination != "" {
		rootTarget = rootMount.Destination
	}
	node := components.NewTreeNode(components.Dim("Mounts")).SetSelectable(true).SetExpanded(true)
	node.SetReference(&detailGridSelectionDetail{
		Title: "Filesystem Mounts",
		Lines: detailLines(
			detailField("Total", fmt.Sprintf("%d", len(mounts))),
			detailField("Root", rootTarget),
			detailField("CRI", fmt.Sprintf("%d", len(criMounts))),
			detailField("Runtime/Other", fmt.Sprintf("%d / %d", len(runtimeMounts), len(otherMounts))),
		),
	})
	for _, child := range []*tview.TreeNode{
		filesystemLeafNode("Total", fmt.Sprintf("%d", len(mounts)), "Mount Count", detailLines(
			detailField("Count", fmt.Sprintf("%d", len(mounts))),
			detailField("Root", rootTarget),
			detailField("CRI", fmt.Sprintf("%d", len(criMounts))),
			detailField("Runtime", fmt.Sprintf("%d", len(runtimeMounts))),
		)),
		filesystemLeafNode("Root", rootTarget, "Root Mount", detailLines(
			detailField("Destination", rootTarget),
			detailField("Type", "Root mount"),
			detailField("Scope", "Container filesystem"),
			detailField("Source", fallbackValue(mountSource(rootMount), "-")),
		)),
		filesystemLeafNode("CRI", fmt.Sprintf("%d", len(criMounts)), "CRI Mounts", detailLines(
			detailField("Count", fmt.Sprintf("%d", len(criMounts))),
			detailField("Type", "CRI"),
			detailField("Source", "Config / status"),
			detailField("Scope", "Declared mounts"),
		)),
		filesystemLeafNode("Runtime", fmt.Sprintf("%d", len(runtimeMounts)), "Runtime Mounts", detailLines(
			detailField("Count", fmt.Sprintf("%d", len(runtimeMounts))),
			detailField("Type", "Runtime"),
			detailField("Source", "OCI / runtime state"),
			detailField("Scope", "Default mounts"),
		)),
		filesystemLeafNode("Other", fmt.Sprintf("%d", len(otherMounts)), "Other Mounts", detailLines(
			detailField("Count", fmt.Sprintf("%d", len(otherMounts))),
			detailField("Type", "Other"),
			detailField("Source", "Live kernel state"),
			detailField("Scope", "Residual mounts"),
		)),
	} {
		node.AddChild(child)
	}
	return node
}

func buildFilesystemRootfsNode(config *runtime.ContainerConfig, storage *runtime.ContainerStorage) *tview.TreeNode {
	backend := "-"
	if storage != nil && storage.Backend != nil {
		backend = formatLayerBackendV1(storage.Backend)
	} else if config != nil && config.Backend != nil {
		backend = formatLayerBackendV1(config.Backend)
	}

	rwLayer := "-"
	if storage != nil {
		switch {
		case storage.Containerd != nil && storage.Containerd.RWSnapshotKey != "":
			rwLayer = storage.Containerd.RWSnapshotKey
		case storage.Docker != nil && storage.Docker.RWLayerID != "":
			rwLayer = storage.Docker.RWLayerID
		case storage.Docker != nil && storage.Docker.RWSnapshotKey != "":
			rwLayer = storage.Docker.RWSnapshotKey
		case storage.Crio != nil && storage.Crio.RWLayerID != "":
			rwLayer = storage.Crio.RWLayerID
		}
	}
	if rwLayer == "-" && config != nil && config.SnapshotKey != "" {
		rwLayer = config.SnapshotKey
	}

	node := components.NewTreeNode(components.Dim("Rootfs")).SetSelectable(true).SetExpanded(true)
	node.SetReference(&detailGridSelectionDetail{
		Title: "Rootfs",
		Lines: detailLines(
			detailField("Backend", backend),
			detailField("RW Layer", fallbackValue(rwLayer, "-")),
			detailField("Image Layers", fmt.Sprintf("%d", filesystemLayerCount(storage))),
			detailField("Scope", "Writable + read-only layers"),
		),
	})
	node.AddChild(filesystemLeafNode("Backend", backend, "Rootfs Backend", detailLines(
		detailField("Backend", backend),
		detailField("Scope", "Rootfs"),
		detailField("Kind", "Storage backend"),
		detailField("Origin", "Runtime metadata"),
	)))
	node.AddChild(filesystemLeafNode("RW Layer", truncateForCard(rwLayer, 30), "RW Layer", detailLines(
		detailField("ID / Key", fallbackValue(rwLayer, "-")),
		detailField("Scope", "Writable layer"),
		detailField("Backend", backend),
		detailField("Origin", "Container storage"),
	)))

	layersNode := components.NewTreeNode(components.Dim(fmt.Sprintf("Image Layers (%d)", filesystemLayerCount(storage)))).SetSelectable(true).SetExpanded(true)
	layersNode.SetReference(&detailGridSelectionDetail{
		Title: "Image Layers",
		Lines: detailLines(
			detailField("Count", fmt.Sprintf("%d", filesystemLayerCount(storage))),
			detailField("Order", "Reverse"),
			detailField("Type", "Read-only"),
			detailField("Selection", "Choose a layer for full metadata"),
		),
	})
	if storage != nil && len(storage.ReadOnlyLayers) > 0 {
		for i := len(storage.ReadOnlyLayers) - 1; i >= 0; i-- {
			layer := storage.ReadOnlyLayers[i]
			layersNode.AddChild(filesystemLeafNode(
				fmt.Sprintf("Layer %d", layer.Index),
				truncateForCard(layerTreeID(layer), 24),
				fmt.Sprintf("Image Layer %d", layer.Index),
				filesystemLayerDetail(layer),
			))
		}
	} else {
		emptyNode := components.NewTreeNode(components.Muted("none")).SetSelectable(true)
		emptyNode.SetReference(&detailGridSelectionDetail{Title: "Image Layers", Lines: detailLines(
			detailField("Count", "0"),
			detailField("Type", "Read-only"),
			detailField("Order", "Reverse"),
			detailField("State", "No layers available"),
		)})
		layersNode.AddChild(emptyNode)
	}
	node.AddChild(layersNode)

	return node
}

func filesystemLeafNode(key, value string, title string, detailLines []string) *tview.TreeNode {
	node := components.NewTreeNode(fmt.Sprintf("[%s]%s[-] [%s]%s[-]",
		components.ColorName(components.ColorFgMuted), key,
		components.ColorName(components.ColorFgBright), value)).SetSelectable(true)
	node.SetReference(&detailGridSelectionDetail{Title: title, Lines: detailLines})
	return node
}

func filesystemLayerCount(storage *runtime.ContainerStorage) int {
	if storage == nil {
		return 0
	}
	return len(storage.ReadOnlyLayers)
}

func filesystemLayerDetail(layer *runtime.ImageLayer) []string {
	if layer == nil {
		return []string{"  " + components.Muted("No layer metadata available")}
	}
	usage := "-"
	switch {
	case layer.UsageSize > 0 && layer.UsageInodes > 0:
		usage = fmt.Sprintf("%s, %d inodes", formatBytes(layer.UsageSize), layer.UsageInodes)
	case layer.UsageSize > 0:
		usage = formatBytes(layer.UsageSize)
	case layer.UsageInodes > 0:
		usage = fmt.Sprintf("%d inodes", layer.UsageInodes)
	}
	return detailLines(
		detailField("ID", layerTreeID(layer)),
		detailField("Path", fallbackValue(layer.Path, "-")),
		detailField("Usage", usage),
	)
}

func mountSource(mount *runtime.Mount) string {
	if mount == nil {
		return "-"
	}
	if mount.Source != "" {
		return mount.Source
	}
	if mount.HostPath != "" {
		return mount.HostPath
	}
	return "-"
}

func joinGridBlocks(blocks []string) string {
	return strings.Join(blocks, "\n\n")
}
