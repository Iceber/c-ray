package views

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// ProcessTab represents the active sub-tab in the Processes workspace.
type ProcessTab int

const (
	ProcessTabSummary ProcessTab = iota
	ProcessTabTree
	ProcessTabTop
)

// ProcessesView groups process summary, tree, and top inspection into one workspace.
type ProcessesView struct {
	*tview.Flex

	app         *tview.Application
	ctx         context.Context
	tabBar      *tview.TextView
	pages       *tview.Pages
	summaryView *ProcessSummaryView
	treeView    *ProcessTreeView
	topView     *TopView

	activeTab   ProcessTab
	lastRefresh time.Time
	mu          sync.Mutex
}

// NewProcessesView creates a combined process workspace.
func NewProcessesView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *ProcessesView {
	v := &ProcessesView{
		Flex:        tview.NewFlex().SetDirection(tview.FlexRow),
		app:         app,
		ctx:         ctx,
		summaryView: NewProcessSummaryView(app),
		treeView:    NewProcessTreeView(app, rt, ctx),
		topView:     NewTopView(app, rt, ctx),
		activeTab:   ProcessTabSummary,
	}

	v.tabBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.tabBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	v.pages = tview.NewPages()
	v.pages.AddPage("summary", v.summaryView, true, true)
	v.pages.AddPage("tree", v.treeView, true, false)
	v.pages.AddPage("top", v.topView, true, false)

	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.pages, 0, 1, true)
	v.updateTabBar()
	return v
}

// SetContainer sets the container ID for process subviews.
func (v *ProcessesView) SetContainer(containerID string) {
	v.topView.SetContainer(containerID)
	v.treeView.SetContainer(containerID)
	v.mu.Lock()
	v.lastRefresh = time.Time{}
	v.mu.Unlock()
}

// SetDetail updates summary and tree context.
func (v *ProcessesView) SetDetail(detail *models.ContainerDetail) {
	v.summaryView.SetDetail(detail)
	v.treeView.SetDetail(detail)
}

// Refresh refreshes the active process sub-tab.
func (v *ProcessesView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	v.lastRefresh = time.Now()
	activeTab := v.activeTab
	v.mu.Unlock()

	switch activeTab {
	case ProcessTabTree:
		return v.treeView.Refresh(ctx)
	case ProcessTabTop:
		return v.topView.Refresh(ctx)
	default:
		v.summaryView.Refresh()
		return nil
	}
}

// StartAutoRefresh starts auto refresh if the active process tab needs it.
func (v *ProcessesView) StartAutoRefresh() {
	if v.activeTab == ProcessTabTop {
		v.topView.StartAutoRefresh()
	}
}

// StopAutoRefresh stops any active refresh loop.
func (v *ProcessesView) StopAutoRefresh() {
	v.topView.StopAutoRefresh()
}

// HandleInput processes tab switching and delegates tab-specific keys.
func (v *ProcessesView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		return event
	}

	if event.Key() == tcell.KeyRune {
		switch event.Rune() {
		case 's', 'S':
			v.switchTab(ProcessTabSummary)
			return nil
		case 'g', 'G':
			v.switchTab(ProcessTabTree)
			return nil
		case 't', 'T':
			v.switchTab(ProcessTabTop)
			return nil
		case '[':
			v.switchTab((v.activeTab + 2) % 3)
			return nil
		case ']':
			v.switchTab((v.activeTab + 1) % 3)
			return nil
		}
	}

	switch v.activeTab {
	case ProcessTabTree:
		return v.treeView.HandleInput(event)
	case ProcessTabTop:
		return v.topView.HandleInput(event)
	default:
		return v.summaryView.HandleInput(event)
	}
}

// GetFocusPrimitive returns the focus primitive for the active sub-tab.
func (v *ProcessesView) GetFocusPrimitive() tview.Primitive {
	switch v.activeTab {
	case ProcessTabTree:
		return v.treeView.GetFocusPrimitive()
	case ProcessTabTop:
		return v.topView.GetFocusPrimitive()
	default:
		return v.summaryView.GetFocusPrimitive()
	}
}

func (v *ProcessesView) switchTab(tab ProcessTab) {
	v.topView.StopAutoRefresh()
	v.mu.Lock()
	v.activeTab = tab
	v.mu.Unlock()

	switch tab {
	case ProcessTabSummary:
		v.pages.SwitchToPage("summary")
		v.summaryView.Refresh()
		v.app.SetFocus(v.summaryView.GetFocusPrimitive())
	case ProcessTabTree:
		v.pages.SwitchToPage("tree")
		go v.treeView.Refresh(v.ctx)
		v.app.SetFocus(v.treeView.GetFocusPrimitive())
	case ProcessTabTop:
		v.pages.SwitchToPage("top")
		v.topView.StartAutoRefresh()
		go v.topView.Refresh(v.ctx)
		v.app.SetFocus(v.topView.GetFocusPrimitive())
	}

	v.updateTabBar()
}

func (v *ProcessesView) updateTabBar() {
	tabs := []struct {
		label string
		key   string
	}{
		{"Summary", "s"},
		{"Tree", "g"},
		{"Top", "t"},
	}

	text := " "
	for i, tab := range tabs {
		if ProcessTab(i) == v.activeTab {
			text += fmt.Sprintf("[black:aqua] %s(%s) [-:-] ", tab.label, tab.key)
		} else {
			text += fmt.Sprintf("[white:darkslategray] %s(%s) [-:-] ", tab.label, tab.key)
		}
	}
	v.tabBar.SetText(text)
}
