package views

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// processSummarySection holds one group of info for the process environment.
type processSummarySection struct {
	Title   string
	Summary string
	Rows    []string
}

// ProcessSummaryView renders the Summary sub-tab inside Processes.
type ProcessSummaryView struct {
	*tview.Flex

	app       *tview.Application
	panel     *tview.TextView
	container runtime.Container
	mu        sync.Mutex
}

// NewProcessSummaryView creates a new process summary view.
func NewProcessSummaryView(app *tview.Application) *ProcessSummaryView {
	v := &ProcessSummaryView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
	}

	v.panel = tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	v.panel.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.panel.SetTitle(fmt.Sprintf(" %s ", components.Accent("Process Environment"))).SetTitleAlign(tview.AlignLeft)
	v.panel.SetBackgroundColor(components.ColorBg)
	v.panel.SetText(fmt.Sprintf("  %s", components.Muted("No process summary")))

	v.Flex.AddItem(v.panel, 0, 1, true)
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
	return v.panel
}

// HandleInput processes local keybindings.
func (v *ProcessSummaryView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	return event
}

func (v *ProcessSummaryView) render(config *runtime.ContainerConfig) {
	sections := buildProcessSections(config)

	queueUpdateDraw(v.app, func() {
		if config == nil {
			v.panel.SetText(fmt.Sprintf("  %s", components.Muted("Waiting for process summary data...")))
			return
		}

		var b strings.Builder
		for i, s := range sections {
			if i > 0 {
				b.WriteString("\n")
			}
			title := s.Title
			if s.Summary != "" {
				title += " " + components.Muted(s.Summary)
			}
			b.WriteString(fmt.Sprintf("  [%s::b]%s[-:-:-]\n", components.ColorName(components.ColorFgAccent), title))
			for _, row := range s.Rows {
				b.WriteString(fmt.Sprintf("  %s\n", components.Muted(row)))
			}
		}
		v.panel.SetText(b.String())
	})
}

func buildProcessSections(config *runtime.ContainerConfig) []processSummarySection {
	if config == nil {
		return nil
	}
	return []processSummarySection{
		buildEnvironmentSection(config),
		buildCGroupSection(config),
		buildPIDNamespaceSection(config),
	}
}

func buildEnvironmentSection(config *runtime.ContainerConfig) processSummarySection {
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

	return processSummarySection{Title: "Environment", Summary: summary, Rows: rows}
}

func buildCGroupSection(config *runtime.ContainerConfig) processSummarySection {
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

	return processSummarySection{
		Title:   "CGroup",
		Summary: summary,
		Rows: []string{
			"Version: " + versionLabel,
			"Path: " + pathLabel,
			"Mount Path: " + mountLabel,
		},
	}
}

func buildPIDNamespaceSection(config *runtime.ContainerConfig) processSummarySection {
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

	return processSummarySection{
		Title:   "PID Namespace",
		Summary: summary,
		Rows:    []string{"Shared PID: " + sharedPID},
	}
}
