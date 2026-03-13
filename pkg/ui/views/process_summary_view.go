package views

import (
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/rivo/tview"
)

// ProcessSummaryView renders the Summary sub-tab inside Processes.
type ProcessSummaryView struct {
	*tview.Flex

	app       *tview.Application
	tree      *tview.TreeView
	statusBar *tview.TextView

	detail *models.ContainerDetail
	mu     sync.Mutex
}

// NewProcessSummaryView creates a new process summary view.
func NewProcessSummaryView(app *tview.Application) *ProcessSummaryView {
	v := &ProcessSummaryView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	v.tree = tview.NewTreeView()
	v.tree.SetBorder(false)
	v.tree.SetGraphics(true)
	v.tree.SetGraphicsColor(tcell.ColorDarkCyan)
	v.tree.SetRoot(tview.NewTreeNode("[gray]No process summary[-]").SetSelectable(false))
	v.tree.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil && len(node.GetChildren()) > 0 {
			node.SetExpanded(!node.IsExpanded())
		}
	})

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.statusBar.SetText(" [yellow]Enter/Space[white]:expand  [yellow]s/g/t[white]:switch process views")

	v.Flex.AddItem(v.tree, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	return v
}

// SetDetail updates the process summary source.
func (v *ProcessSummaryView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
	v.render()
}

// Refresh re-renders current data.
func (v *ProcessSummaryView) Refresh() {
	v.render()
}

// GetFocusPrimitive returns the focus primitive.
func (v *ProcessSummaryView) GetFocusPrimitive() tview.Primitive {
	return v.tree
}

// HandleInput processes local keybindings.
func (v *ProcessSummaryView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
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

func (v *ProcessSummaryView) render() {
	v.mu.Lock()
	detail := v.detail
	v.mu.Unlock()

	queueUpdateDraw(v.app, func() {
		if detail == nil {
			root := tview.NewTreeNode("[gray]Waiting for process summary data...[-]").SetSelectable(false)
			v.tree.SetRoot(root)
			v.tree.SetCurrentNode(root)
			return
		}

		root := tview.NewTreeNode("[aqua::b]Process Summary[-:-:-]").SetSelectable(false).SetExpanded(true)
		for _, section := range buildProcessSummarySections(detail) {
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

func (v *ProcessSummaryView) toggleCurrentNode() {
	if node := v.tree.GetCurrentNode(); node != nil && len(node.GetChildren()) > 0 {
		node.SetExpanded(!node.IsExpanded())
	}
}

func buildProcessSummarySections(detail *models.ContainerDetail) []detailSection {
	return []detailSection{
		{
			Title:    "Environment",
			Summary:  environmentSummary(detail),
			Expanded: true,
			Rows:     buildEnvironmentRows(detail),
		},
		{
			Title:    "CGroup",
			Summary:  cgroupSummary(detail),
			Expanded: true,
			Rows: []string{
				fmt.Sprintf("Version: %s", cgroupVersionLabel(detail)),
				fmt.Sprintf("Path: %s", cgroupPathLabel(detail)),
				fmt.Sprintf("Mount Path: %s", cgroupMountPathLabel(detail)),
			},
		},
		{
			Title:    "PID Namespace",
			Summary:  pidNamespaceSummary(detail),
			Expanded: true,
			Rows:     buildPIDNamespaceRows(detail),
		},
	}
}

func cgroupSummary(detail *models.ContainerDetail) string {
	if detail.CGroupVersion > 0 {
		return fmt.Sprintf("v%d", detail.CGroupVersion)
	}
	return "unknown"
}

func cgroupVersionLabel(detail *models.ContainerDetail) string {
	if detail.CGroupVersion <= 0 {
		return "unknown"
	}
	return fmt.Sprintf("v%d", detail.CGroupVersion)
}

func cgroupPathLabel(detail *models.ContainerDetail) string {
	if detail.CGroupPath != "" {
		return detail.CGroupPath
	}
	if detail.RuntimeProfile != nil && detail.RuntimeProfile.CGroup != nil && detail.RuntimeProfile.CGroup.RelativePath != "" {
		return detail.RuntimeProfile.CGroup.RelativePath
	}
	return "unknown"
}

func cgroupMountPathLabel(detail *models.ContainerDetail) string {
	if detail.RuntimeProfile != nil && detail.RuntimeProfile.CGroup != nil && detail.RuntimeProfile.CGroup.AbsolutePath != "" {
		return detail.RuntimeProfile.CGroup.AbsolutePath
	}
	return "unknown"
}

func pidNamespaceSummary(detail *models.ContainerDetail) string {
	shared, _, present := resolvedSharedPID(detail)
	if shared != nil {
		if *shared {
			return "shared"
		}
		return "private"
	}
	if present {
		return "private"
	}
	return "unknown"
}

func buildPIDNamespaceRows(detail *models.ContainerDetail) []string {
	rows := []string{"Shared PID: unknown"}
	shared, _, _ := resolvedSharedPID(detail)
	if shared != nil {
		if *shared {
			rows[0] = "Shared PID: true"
		} else {
			rows[0] = "Shared PID: false"
		}
	}
	if detail.ProcessCount > 0 {
		rows = append(rows, fmt.Sprintf("Observed Processes: %d", detail.ProcessCount))
	}
	return rows
}

func resolvedSharedPID(detail *models.ContainerDetail) (*bool, string, bool) {
	if detail == nil {
		return nil, "", false
	}
	path, present := pidNamespacePath(detail)
	if detail.SharedPID != nil {
		return detail.SharedPID, path, present
	}
	if !present {
		return nil, "", false
	}
	shared := strings.TrimSpace(path) != ""
	return &shared, path, true
}

func pidNamespacePath(detail *models.ContainerDetail) (string, bool) {
	if detail == nil || detail.Namespaces == nil {
		return "", false
	}
	path, ok := detail.Namespaces["pid"]
	return path, ok
}

func environmentSummary(detail *models.ContainerDetail) string {
	if len(detail.Environment) > 0 {
		return fmt.Sprintf("%d vars", len(detail.Environment))
	}
	return "unknown vars"
}

func buildEnvironmentRows(detail *models.ContainerDetail) []string {
	rows := []string{}
	if len(detail.Environment) == 0 {
		return []string{"Count: unknown", "Environment variables unavailable"}
	}

	limit := len(detail.Environment)
	if limit > 12 {
		limit = 12
	}
	for _, env := range detail.Environment[:limit] {
		prefix := "-"
		if env.IsKubernetes {
			prefix = "◇"
		}
		rows = append(rows, fmt.Sprintf("%s %s: %s", prefix, env.Key, env.Value))
	}
	if len(detail.Environment) > limit {
		rows = append(rows, fmt.Sprintf("... %d more", len(detail.Environment)-limit))
	}
	return rows
}
