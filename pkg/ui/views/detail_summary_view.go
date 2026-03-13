package views

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/rivo/tview"
)

type detailSection struct {
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

	detail *models.ContainerDetail
	mu     sync.Mutex
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

// SetDetail updates the rendered container detail.
func (v *DetailSummaryView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
	v.render()
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

func (v *DetailSummaryView) render() {
	v.mu.Lock()
	detail := v.detail
	v.mu.Unlock()

	queueUpdateDraw(v.app, func() {
		if detail == nil {
			root := tview.NewTreeNode("[gray]Loading container summary...[-]").SetSelectable(false)
			v.tree.SetRoot(root)
			v.tree.SetCurrentNode(root)
			return
		}

		root := tview.NewTreeNode("[aqua::b]Container Summary[-:-:-]").SetSelectable(false).SetExpanded(true)
		for _, section := range buildDetailSections(detail) {
			node := tview.NewTreeNode(summarySectionLabel(section.Title, section.Summary)).
				SetSelectable(true).
				SetExpanded(section.Expanded)
			for _, row := range section.Rows {
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

func (v *DetailSummaryView) toggleCurrentNode() {
	if node := v.tree.GetCurrentNode(); node != nil && len(node.GetChildren()) > 0 {
		node.SetExpanded(!node.IsExpanded())
	}
}

func buildDetailSections(detail *models.ContainerDetail) []detailSection {
	sections := []detailSection{
		{
			Title:    "Status",
			Summary:  string(detail.Status),
			Expanded: true,
			Rows:     buildStatusRows(detail),
		},
	}

	if detail.PodNamespace != "" || detail.PodName != "" || detail.PodUID != "" {
		sections = append(sections, detailSection{
			Title:    "Pod",
			Summary:  fmt.Sprintf("%s/%s", fallbackValue(detail.PodNamespace, "?"), fallbackValue(detail.PodName, "?")),
			Expanded: true,
			Rows: []string{
				fmt.Sprintf("Namespace: %s", fallbackValue(detail.PodNamespace, "unknown")),
				fmt.Sprintf("Name: %s", fallbackValue(detail.PodName, "unknown")),
				fmt.Sprintf("UID: %s", fallbackValue(detail.PodUID, "unknown")),
			},
		})
	}

	sandboxID := detailSandboxID(detail)
	sections = append(sections,
		detailSection{
			Title:    "Sandbox",
			Summary:  shortID(sandboxID),
			Expanded: false,
			Rows: []string{
				fmt.Sprintf("Sandbox ID: %s", fallbackValue(sandboxID, "unknown")),
			},
		},
		detailSection{
			Title:    "Image",
			Summary:  fallbackValue(detail.ImageName, fallbackValue(detail.Image, "unknown")),
			Expanded: true,
			Rows:     buildImageRows(detail),
		},
		detailSection{
			Title:    "Snapshotter",
			Summary:  snapshotterSummary(detail),
			Expanded: false,
			Rows: []string{
				fmt.Sprintf("Key: %s", fallbackValue(detail.SnapshotKey, "unknown")),
				fmt.Sprintf("Snapshotter: %s", fallbackValue(detail.Snapshotter, "unknown")),
			},
		},
	)

	return sections
}

func buildStatusRows(detail *models.ContainerDetail) []string {
	rows := []string{
		fmt.Sprintf("Created At: %s", formatSummaryTime(detail.CreatedAt)),
		fmt.Sprintf("Started At: %s", formatSummaryTime(detail.StartedAt)),
	}
	if detail.PID > 0 {
		rows = append(rows, fmt.Sprintf("PID: %d", detail.PID))
	}
	if detail.ShimPID > 0 {
		rows = append(rows, fmt.Sprintf("Shim PID: %d", detail.ShimPID))
	}
	if detail.Status == models.ContainerStatusStopped {
		exitedAt := "unknown"
		if !detail.ExitedAt.IsZero() {
			exitedAt = formatSummaryTime(detail.ExitedAt)
		}
		exitCode := "unknown"
		if detail.ExitCode != nil {
			exitCode = fmt.Sprintf("%d", *detail.ExitCode)
		}
		exitReason := fallbackValue(detail.ExitReason, "unknown")
		rows = append(rows,
			fmt.Sprintf("Exited At: %s", exitedAt),
			fmt.Sprintf("Exit Code: %s", exitCode),
			fmt.Sprintf("Exit Reason: %s", exitReason),
		)
	}
	restartCount := "unknown"
	if detail.RestartCount != nil {
		restartCount = fmt.Sprintf("%d", *detail.RestartCount)
	}
	rows = append(rows, fmt.Sprintf("Restart Count: %s", restartCount))
	return rows
}

func summarySectionLabel(title string, summary string) string {
	if summary == "" {
		return fmt.Sprintf("[white::b]%s[-:-:-]", title)
	}
	return fmt.Sprintf("[white::b]%s[-:-:-] [gray]%s[-]", title, truncateForCard(summary, 52))
}

func detailSandboxID(detail *models.ContainerDetail) string {
	if detail.PodNetwork != nil && detail.PodNetwork.SandboxID != "" {
		return detail.PodNetwork.SandboxID
	}
	if detail.RuntimeProfile != nil && detail.RuntimeProfile.OCI != nil {
		return detail.RuntimeProfile.OCI.SandboxID
	}
	return ""
}

func snapshotterSummary(detail *models.ContainerDetail) string {
	if detail.Snapshotter == "" && detail.SnapshotKey == "" {
		return "unknown"
	}
	if detail.Snapshotter == "" {
		return detail.SnapshotKey
	}
	if detail.SnapshotKey == "" {
		return detail.Snapshotter
	}
	return detail.Snapshotter + "/" + detail.SnapshotKey
}

func formatSummaryTime(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	return ts.Format("2006-01-02 15:04:05")
}

func buildImageRows(detail *models.ContainerDetail) []string {
	imageID := detail.ImageID
	if imageID == "" {
		imageID = detail.Image
	}

	mediaType := "unknown"
	configPath := "unknown"
	if detail.ImageConfig != nil {
		if detail.ImageConfig.TargetKind != "" || detail.ImageConfig.Schema != "" {
			mediaType = strings.TrimSpace(detail.ImageConfig.TargetKind + " / " + detail.ImageConfig.Schema)
			mediaType = strings.Trim(mediaType, " /")
		}
		if detail.ImageConfig.ContentPath != "" {
			configPath = detail.ImageConfig.ContentPath
		}
	}

	rows := []string{
		fmt.Sprintf("Name: %s", fallbackValue(detail.ImageName, "unknown")),
		fmt.Sprintf("ID: %s", fallbackValue(imageID, "unknown")),
		fmt.Sprintf("Media Type: %s", mediaType),
		fmt.Sprintf("Config Path: %s", configPath),
		"Manifest: Reserved for future manifest view",
	}

	if detail.ImageConfig != nil && detail.ImageConfig.TargetMediaType != "" {
		rows = append(rows, fmt.Sprintf("Raw Media Type: %s", detail.ImageConfig.TargetMediaType))
	}

	return rows
}
