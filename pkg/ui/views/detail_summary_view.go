package views

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

type summarySection struct {
	Title    string
	Summary  string
	Expanded bool
	Rows     []string
}

// DetailSummaryView renders the Summary tab for container detail.
type DetailSummaryView struct {
	*tview.Flex

	app       *tview.Application
	tree      *tview.TreeView
	statusBar *tview.TextView
	container runtime.Container
	mu        sync.Mutex
}

// NewDetailSummaryView creates a new detail summary view.
func NewDetailSummaryView(app *tview.Application) *DetailSummaryView {
	v := &DetailSummaryView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.tree.SetRoot(tview.NewTreeNode("[gray]No summary data[-]").SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil && len(node.GetChildren()) > 0 {
			node.SetExpanded(!node.IsExpanded())
		}
	})

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.statusBar.SetText(" [yellow]Enter/Space[white]:expand  [yellow]j/k[white]:move")

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	return v
}

// SetContainer sets the container handle.
func (v *DetailSummaryView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
}

// Refresh loads summary data from the container handle.
func (v *DetailSummaryView) Refresh(ctx context.Context) {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		v.renderEmpty()
		return
	}

	info, _ := c.Info(ctx)
	state, _ := c.State(ctx)
	config, _ := c.Config(ctx)
	profile, _ := c.Runtime(ctx)

	var imageConfig *runtime.ImageConfigInfo
	if img, err := c.Image(ctx); err == nil && img != nil {
		imageConfig, _ = img.Config(ctx)
	}

	v.render(info, state, config, profile, imageConfig)
}

// GetFocusPrimitive returns the focusable primitive.
func (v *DetailSummaryView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

// HandleInput processes local keybindings.
func (v *DetailSummaryView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Key() {
	case tcell.KeyEnter:
		v.toggleCurrentNode()
		return nil
	case tcell.KeyRune:
		if event.Rune() == ' ' {
			v.toggleCurrentNode()
			return nil
		}
	}
	return event
}

func (v *DetailSummaryView) toggleCurrentNode() {
	if node := v.tree.GetCurrentNode(); node != nil && len(node.GetChildren()) > 0 {
		node.SetExpanded(!node.IsExpanded())
	}
}

func (v *DetailSummaryView) renderEmpty() {
	queueUpdateDraw(v.app, func() {
		root := tview.NewTreeNode("[gray]Loading container summary...[-]").SetSelectable(false)
		v.tree.SetRoot(root)
		v.tree.SetCurrentNode(root)
	})
}

func (v *DetailSummaryView) render(info *runtime.ContainerInfo, state *runtime.ContainerState, config *runtime.ContainerConfig, runtime *runtime.RuntimeProfile, imageConfig *runtime.ImageConfigInfo) {
	sections := buildSummarySections(info, state, config, runtime, imageConfig)

	queueUpdateDraw(v.app, func() {
		root := tview.NewTreeNode("[aqua::b]Container Summary[-:-:-]").SetSelectable(false).SetExpanded(true)
		for _, s := range sections {
			node := tview.NewTreeNode(sectionLabel(s.Title, s.Summary)).
				SetSelectable(true).SetExpanded(s.Expanded)
			for _, row := range s.Rows {
				node.AddChild(tview.NewTreeNode("[gray]" + row + "[-]").SetSelectable(false))
			}
			root.AddChild(node)
		}
		v.tree.SetRoot(root)
		if len(root.GetChildren()) > 0 {
			v.tree.SetCurrentNode(root.GetChildren()[0])
		} else {
			v.tree.SetCurrentNode(root)
		}
	})
}

func sectionLabel(title, summary string) string {
	if summary == "" {
		return fmt.Sprintf("[white::b]%s[-:-:-]", title)
	}
	return fmt.Sprintf("[white::b]%s[-:-:-] [gray]%s[-]", title, truncateForCard(summary, 52))
}

func buildSummarySections(info *runtime.ContainerInfo, state *runtime.ContainerState, config *runtime.ContainerConfig, runtime *runtime.RuntimeProfile, imageConfig *runtime.ImageConfigInfo) []summarySection {
	sections := []summarySection{
		buildStatusSection(info, state),
	}

	if info != nil && (info.PodNamespace != "" || info.PodName != "" || info.PodUID != "") {
		sections = append(sections, summarySection{
			Title:    "Pod",
			Summary:  fmt.Sprintf("%s/%s", fallbackValue(info.PodNamespace, "?"), fallbackValue(info.PodName, "?")),
			Expanded: true,
			Rows: []string{
				"Namespace: " + fallbackValue(info.PodNamespace, "unknown"),
				"Name: " + fallbackValue(info.PodName, "unknown"),
				"UID: " + fallbackValue(info.PodUID, "unknown"),
			},
		})
	}

	sandboxID := resolveSandboxID(runtime)
	sections = append(sections, summarySection{
		Title:    "Sandbox",
		Summary:  shortID(sandboxID),
		Expanded: false,
		Rows:     []string{"Sandbox ID: " + fallbackValue(sandboxID, "unknown")},
	})

	sections = append(sections, buildImageSection(info, config, imageConfig))
	sections = append(sections, buildSnapshotterSection(config))

	return sections
}

func buildStatusSection(info *runtime.ContainerInfo, state *runtime.ContainerState) summarySection {
	status := "unknown"
	if state != nil {
		status = string(state.Status)
	} else if info != nil {
		status = string(info.Status)
	}

	rows := []string{}
	if info != nil {
		rows = append(rows, "Created At: "+formatSummaryTime(info.CreatedAt))
	}
	if state != nil {
		rows = append(rows, "Started At: "+formatSummaryTime(state.StartedAt))
		if state.PID > 0 {
			rows = append(rows, fmt.Sprintf("PID: %d", state.PID))
		}
		if state.PPID > 0 {
			rows = append(rows, fmt.Sprintf("Shim PID: %d", state.PPID))
		}
		if state.Status == runtime.ContainerStatusStopped {
			rows = append(rows, "Exited At: "+formatSummaryTime(state.ExitedAt))
			exitCode := "unknown"
			if state.ExitCode != nil {
				exitCode = fmt.Sprintf("%d", *state.ExitCode)
			}
			rows = append(rows, "Exit Code: "+exitCode)
			rows = append(rows, "Exit Reason: "+fallbackValue(state.ExitReason, "unknown"))
		}
		restartCount := "unknown"
		if state.RestartCount != nil {
			restartCount = fmt.Sprintf("%d", *state.RestartCount)
		}
		rows = append(rows, "Restart Count: "+restartCount)
	}

	return summarySection{Title: "Status", Summary: status, Expanded: true, Rows: rows}
}

func buildImageSection(info *runtime.ContainerInfo, config *runtime.ContainerConfig, imageConfig *runtime.ImageConfigInfo) summarySection {
	imageName := "unknown"
	if config != nil && config.ImageName != "" {
		imageName = config.ImageName
	} else if info != nil && info.Image != "" {
		imageName = info.Image
	}

	imageID := "unknown"
	if info != nil && info.Image != "" {
		imageID = info.Image
	}

	mediaType := "unknown"
	configPath := "unknown"
	if imageConfig != nil {
		if imageConfig.TargetKind != "" || imageConfig.Schema != "" {
			mediaType = strings.TrimSpace(imageConfig.TargetKind + " / " + imageConfig.Schema)
			mediaType = strings.Trim(mediaType, " /")
		}
		if imageConfig.ContentPath != "" {
			configPath = imageConfig.ContentPath
		}
	}

	rows := []string{
		"Name: " + imageName,
		"ID: " + imageID,
		"Media Type: " + mediaType,
		"Config Path: " + configPath,
		"Manifest: Reserved for future manifest view",
	}

	if imageConfig != nil && imageConfig.TargetMediaType != "" {
		rows = append(rows, "Raw Media Type: "+imageConfig.TargetMediaType)
	}

	return summarySection{
		Title:    "Image",
		Summary:  fallbackValue(imageName, "unknown"),
		Expanded: true,
		Rows:     rows,
	}
}

func buildSnapshotterSection(config *runtime.ContainerConfig) summarySection {
	summary := "unknown"
	snapshotter := "unknown"
	key := "unknown"
	if config != nil {
		if config.Snapshotter != "" {
			snapshotter = config.Snapshotter
		}
		if config.SnapshotKey != "" {
			key = config.SnapshotKey
		}
		if config.Snapshotter != "" && config.SnapshotKey != "" {
			summary = config.Snapshotter + "/" + config.SnapshotKey
		} else if config.Snapshotter != "" {
			summary = config.Snapshotter
		} else if config.SnapshotKey != "" {
			summary = config.SnapshotKey
		}
	}

	return summarySection{
		Title:    "Snapshotter",
		Summary:  summary,
		Expanded: false,
		Rows: []string{
			"Key: " + key,
			"Snapshotter: " + snapshotter,
		},
	}
}

func resolveSandboxID(runtime *runtime.RuntimeProfile) string {
	if runtime != nil && runtime.OCI != nil && runtime.OCI.SandboxID != "" {
		return runtime.OCI.SandboxID
	}
	return ""
}
