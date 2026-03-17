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

// ProcessSummaryView renders the Summary sub-tab inside Processes.
type ProcessSummaryView struct {
	*tview.Flex

	app       *tview.Application
	tree      *tview.TreeView
	statusBar *tview.TextView
	container runtime.Container
	mu        sync.Mutex
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

// SetContainer sets the container handle.
func (v *ProcessSummaryView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
}

// Refresh loads data from the container handle and re-renders.
func (v *ProcessSummaryView) Refresh(ctx context.Context) {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		return
	}

	config, _ := c.Config(ctx)
	v.render(config)
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

func (v *ProcessSummaryView) toggleCurrentNode() {
	if node := v.tree.GetCurrentNode(); node != nil && len(node.GetChildren()) > 0 {
		node.SetExpanded(!node.IsExpanded())
	}
}

func (v *ProcessSummaryView) render(config *runtime.ContainerConfig) {
	sections := buildProcessSections(config)

	queueUpdateDraw(v.app, func() {
		if config == nil {
			root := tview.NewTreeNode("[gray]Waiting for process summary data...[-]").SetSelectable(false)
			v.tree.SetRoot(root)
			v.tree.SetCurrentNode(root)
			return
		}

		root := tview.NewTreeNode("[aqua::b]Process Summary[-:-:-]").SetSelectable(false).SetExpanded(true)
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

func buildProcessSections(config *runtime.ContainerConfig) []summarySection {
	if config == nil {
		return nil
	}
	return []summarySection{
		buildEnvironmentSection(config),
		buildCGroupSection(config),
		buildPIDNamespaceSection(config),
	}
}

func buildEnvironmentSection(config *runtime.ContainerConfig) summarySection {
	summary := "unknown vars"
	if len(config.Environment) > 0 {
		summary = fmt.Sprintf("%d vars", len(config.Environment))
	}

	rows := []string{}
	if len(config.Environment) == 0 {
		rows = append(rows, "Count: unknown", "Environment variables unavailable")
	} else {
		limit := len(config.Environment)
		if limit > 12 {
			limit = 12
		}
		for _, env := range config.Environment[:limit] {
			prefix := "-"
			if env.IsKubernetes {
				prefix = "◇"
			}
			rows = append(rows, fmt.Sprintf("%s %s: %s", prefix, env.Key, env.Value))
		}
		if len(config.Environment) > limit {
			rows = append(rows, fmt.Sprintf("... %d more", len(config.Environment)-limit))
		}
	}

	return summarySection{Title: "Environment", Summary: summary, Expanded: true, Rows: rows}
}

func buildCGroupSection(config *runtime.ContainerConfig) summarySection {
	summary := "unknown"
	if config.CGroupVersion > 0 {
		summary = fmt.Sprintf("v%d", config.CGroupVersion)
	}

	versionLabel := "unknown"
	if config.CGroupVersion > 0 {
		versionLabel = fmt.Sprintf("v%d", config.CGroupVersion)
	}
	pathLabel := fallbackValue(config.CGroupPath, "unknown")
	mountLabel := fallbackValue(config.CGroupMountedPath, "unknown")

	return summarySection{
		Title:    "CGroup",
		Summary:  summary,
		Expanded: true,
		Rows: []string{
			"Version: " + versionLabel,
			"Path: " + pathLabel,
			"Mount Path: " + mountLabel,
		},
	}
}

func buildPIDNamespaceSection(config *runtime.ContainerConfig) summarySection {
	summary := "unknown"
	sharedPID := "unknown"

	if config.Namespaces != nil {
		pidPath, ok := config.Namespaces["pid"]
		if ok {
			if strings.TrimSpace(pidPath) != "" {
				summary = "shared"
				sharedPID = "true"
			} else {
				summary = "private"
				sharedPID = "false"
			}
		}
	}

	return summarySection{
		Title:    "PID Namespace",
		Summary:  summary,
		Expanded: true,
		Rows:     []string{"Shared PID: " + sharedPID},
	}
}
