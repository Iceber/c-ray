package views

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// DetailTab represents the active tab in the container detail view.
type DetailTab int

const (
	DetailTabSummary DetailTab = iota
	DetailTabProcesses
	DetailTabFilesystem
	DetailTabRuntime
	DetailTabNetwork
)

// ContainerDetailView displays detailed information about a container.
type ContainerDetailView struct {
	*tview.Flex

	app *tview.Application
	ctx context.Context

	container   runtime.Container
	info        *runtime.ContainerInfo
	state       *runtime.ContainerState
	refreshedAt time.Time

	headerLine1 *tview.TextView
	headerLine2 *tview.TextView
	contextLine *tview.TextView
	tabBar      *tview.TextView
	content     *tview.Pages
	statusBar   *tview.TextView

	summaryView    *DetailSummaryView
	processesView  *ProcessesView
	filesystemView *StorageView
	runtimeView    *RuntimeInfoView
	networkView    *NetworkInfoView

	activeTab DetailTab
	onBack    func()
}

// NewContainerDetailView creates a new container detail view.
func NewContainerDetailView(app *tview.Application, ctx context.Context) *ContainerDetailView {
	v := &ContainerDetailView{
		Flex:      tview.NewFlex().SetDirection(tview.FlexRow),
		app:       app,
		ctx:       ctx,
		activeTab: DetailTabSummary,
	}
	v.setupLayout()
	return v
}

func (v *ContainerDetailView) setupLayout() {
	v.headerLine1 = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.headerLine2 = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.contextLine = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.tabBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.headerLine1.SetBackgroundColor(tcell.ColorDarkSlateGray)
	v.headerLine2.SetBackgroundColor(tcell.ColorBlack)
	v.contextLine.SetBackgroundColor(tcell.ColorBlack)
	v.tabBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	v.summaryView = NewDetailSummaryView(v.app)
	v.processesView = NewProcessesView(v.app, v.ctx)
	v.filesystemView = NewStorageView(v.app, v.ctx)
	v.runtimeView = NewRuntimeInfoView(v.app)
	v.networkView = NewNetworkInfoView(v.app)

	v.content = tview.NewPages()
	v.content.AddPage("summary", v.summaryView, true, true)
	v.content.AddPage("processes", v.processesView, true, false)
	v.content.AddPage("filesystem", v.filesystemView, true, false)
	v.content.AddPage("runtime", v.runtimeView, true, false)
	v.content.AddPage("network", v.networkView, true, false)

	v.Flex.AddItem(v.headerLine1, 1, 0, false)
	v.Flex.AddItem(v.headerLine2, 1, 0, false)
	v.Flex.AddItem(v.contextLine, 1, 0, false)
	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.content, 0, 1, false)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.renderHeader()
	v.updateTabBar()
	v.updateStatusBar()
}

// SetContainer sets the container handle to display and loads initial data.
func (v *ContainerDetailView) SetContainer(c runtime.Container) {
	v.container = c
	v.info = nil
	v.state = nil
	v.refreshedAt = time.Time{}

	v.summaryView.SetContainer(c)
	v.processesView.SetContainer(c)
	v.filesystemView.SetContainer(c)
	v.runtimeView.SetContainer(c)
	v.networkView.SetContainer(c)

	queueUpdateDraw(v.app, func() {
		v.renderHeader()
		v.updateStatusBar()
	})

	go v.Refresh()
}

// SetBackFunc sets the callback for when user navigates back.
func (v *ContainerDetailView) SetBackFunc(handler func()) {
	v.onBack = handler
}

// Leave is called when navigating away from the detail view.
func (v *ContainerDetailView) Leave() {
	v.processesView.StopAutoRefresh()
}

// GetFocusPrimitive returns the currently active focus primitive.
func (v *ContainerDetailView) GetFocusPrimitive() tview.Primitive {
	switch v.activeTab {
	case DetailTabProcesses:
		return v.processesView.GetFocusPrimitive()
	case DetailTabFilesystem:
		return v.filesystemView.GetFocusPrimitive()
	case DetailTabRuntime:
		return v.runtimeView.GetFocusPrimitive()
	case DetailTabNetwork:
		return v.networkView.GetFocusPrimitive()
	default:
		return v.summaryView.GetFocusPrimitive()
	}
}

// Refresh loads header data and refreshes the active tab.
func (v *ContainerDetailView) Refresh() {
	if v.container == nil {
		return
	}

	info, err := v.container.Info(v.ctx)
	if err != nil {
		queueUpdateDraw(v.app, func() {
			v.headerLine1.SetText(fmt.Sprintf(" [red]Failed to load container: %v[-]", err))
			v.headerLine2.SetText(" ")
			v.contextLine.SetText(" ")
		})
		return
	}

	state, _ := v.container.State(v.ctx)

	v.info = info
	v.state = state
	v.refreshedAt = time.Now()

	queueUpdateDraw(v.app, func() {
		v.renderHeader()
		v.updateStatusBar()
	})

	v.refreshActiveTab()
}

func (v *ContainerDetailView) refreshActiveTab() {
	switch v.activeTab {
	case DetailTabSummary:
		v.summaryView.Refresh(v.ctx)
	case DetailTabProcesses:
		v.processesView.Refresh(v.ctx)
	case DetailTabFilesystem:
		v.filesystemView.Refresh(v.ctx)
	case DetailTabRuntime:
		v.runtimeView.Refresh(v.ctx)
	case DetailTabNetwork:
		v.networkView.Refresh(v.ctx)
	}
}

func (v *ContainerDetailView) renderHeader() {
	if v.info == nil {
		v.headerLine1.SetText(" [gray]Loading container detail...[-]")
		v.headerLine2.SetText(" ")
		v.contextLine.SetText(" ")
		return
	}

	name := v.info.Name
	if name == "" {
		name = shortID(v.info.ID)
	}

	v.headerLine1.SetText(fmt.Sprintf(
		" [white::b]%s[-:-:-] [gray](%s)[-]   %s   [gray]Created[-] %s",
		name, shortID(v.info.ID), detailStateHeadline(v.info, v.state),
		detailTimeLabel(v.info.CreatedAt),
	))
	v.headerLine2.SetText(" " + detailSecondaryLine(v.state))

	if v.info.PodNamespace != "" || v.info.PodName != "" {
		v.contextLine.SetText(fmt.Sprintf(" [gray]Pod[-] [white]%s/%s[-]",
			fallbackValue(v.info.PodNamespace, "?"), fallbackValue(v.info.PodName, "?")))
	} else {
		v.contextLine.SetText(" ")
	}
}

// HandleInput processes key events for the detail view.
func (v *ContainerDetailView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC || event.Rune() == 'Q' {
		return event
	}

	switch event.Key() {
	case tcell.KeyEscape:
		if v.onBack != nil {
			v.Leave()
			v.onBack()
		}
		return nil
	case tcell.KeyTab:
		v.switchTab((v.activeTab + 1) % 5)
		return nil
	case tcell.KeyBacktab:
		v.switchTab((v.activeTab + 4) % 5)
		return nil
	}

	switch event.Rune() {
	case 'q':
		if v.onBack != nil {
			v.Leave()
			v.onBack()
		}
		return nil
	case 'r', 'R':
		go v.Refresh()
		return nil
	case '1':
		v.switchTab(DetailTabSummary)
		return nil
	case '2':
		v.switchTab(DetailTabProcesses)
		return nil
	case '3':
		v.switchTab(DetailTabFilesystem)
		return nil
	case '4':
		v.switchTab(DetailTabRuntime)
		return nil
	case '5':
		v.switchTab(DetailTabNetwork)
		return nil
	}

	// Delegate to active tab.
	switch v.activeTab {
	case DetailTabProcesses:
		return v.processesView.HandleInput(event)
	case DetailTabFilesystem:
		return v.filesystemView.HandleInput(event)
	case DetailTabRuntime:
		return v.runtimeView.HandleInput(event)
	case DetailTabNetwork:
		return v.networkView.HandleInput(event)
	default:
		return v.summaryView.HandleInput(event)
	}
}

func (v *ContainerDetailView) switchTab(tab DetailTab) {
	if v.activeTab == DetailTabProcesses && tab != DetailTabProcesses {
		v.processesView.StopAutoRefresh()
	}

	v.activeTab = tab
	switch tab {
	case DetailTabSummary:
		v.content.SwitchToPage("summary")
		go v.summaryView.Refresh(v.ctx)
		v.app.SetFocus(v.summaryView.GetFocusPrimitive())
	case DetailTabProcesses:
		v.content.SwitchToPage("processes")
		v.processesView.StartAutoRefresh()
		go v.processesView.Refresh(v.ctx)
		v.app.SetFocus(v.processesView.GetFocusPrimitive())
	case DetailTabFilesystem:
		v.content.SwitchToPage("filesystem")
		go v.filesystemView.Refresh(v.ctx)
		v.app.SetFocus(v.filesystemView.GetFocusPrimitive())
	case DetailTabRuntime:
		v.content.SwitchToPage("runtime")
		go v.runtimeView.Refresh(v.ctx)
		v.app.SetFocus(v.runtimeView.GetFocusPrimitive())
	case DetailTabNetwork:
		v.content.SwitchToPage("network")
		go v.networkView.Refresh(v.ctx)
		v.app.SetFocus(v.networkView.GetFocusPrimitive())
	}

	v.updateTabBar()
	v.updateStatusBar()
}

func (v *ContainerDetailView) updateTabBar() {
	tabs := []struct {
		label string
		key   string
	}{
		{"Summary", "1"},
		{"Processes", "2"},
		{"Filesystem", "3"},
		{"Runtime", "4"},
		{"Network", "5"},
	}
	text := " "
	for i, tab := range tabs {
		if DetailTab(i) == v.activeTab {
			text += fmt.Sprintf("[black:aqua] %s(%s) [-:-] ", tab.label, tab.key)
		} else {
			text += fmt.Sprintf("[white:darkslategray] %s(%s) [-:-] ", tab.label, tab.key)
		}
	}
	v.tabBar.SetText(text)
}

func (v *ContainerDetailView) updateStatusBar() {
	text := " [yellow]Esc/q[white]:back  [yellow]1-5[white]:pages  [yellow]Tab[white]:next page  [yellow]r[white]:refresh"
	switch v.activeTab {
	case DetailTabProcesses:
		text += "  [yellow]s/g/t[white]:process tabs  [yellow][/][white]:cycle"
	case DetailTabFilesystem:
		text += "  [yellow]m/l[white]:filesystem tabs"
	case DetailTabRuntime, DetailTabNetwork:
		text += "  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse"
	}
	v.statusBar.SetText(text)
}

// --- Header helpers ---

func detailStateHeadline(info *runtime.ContainerInfo, state *runtime.ContainerState) string {
	status := info.Status
	if state != nil {
		status = state.Status
	}
	switch status {
	case runtime.ContainerStatusRunning:
		pid := info.PID
		if state != nil && state.PID > 0 {
			pid = state.PID
		}
		if pid > 0 {
			return fmt.Sprintf("[green]PID %d[-]", pid)
		}
		return "[green]Running[-]"
	case runtime.ContainerStatusStopped:
		if state != nil && state.ExitCode != nil {
			return fmt.Sprintf("[red]Exit %d[-]", *state.ExitCode)
		}
		return "[red]Exit unknown[-]"
	case runtime.ContainerStatusCreated:
		return "[darkcyan]Created[-]"
	case runtime.ContainerStatusPaused:
		pid := info.PID
		if state != nil && state.PID > 0 {
			pid = state.PID
		}
		if pid > 0 {
			return fmt.Sprintf("[yellow]Paused PID %d[-]", pid)
		}
		return "[yellow]Paused[-]"
	default:
		return "[white]Unknown[-]"
	}
}

func detailSecondaryLine(state *runtime.ContainerState) string {
	if state == nil {
		return "State unknown"
	}
	switch state.Status {
	case runtime.ContainerStatusRunning:
		if !state.StartedAt.IsZero() {
			return fmt.Sprintf("Started %s", detailTimeLabel(state.StartedAt))
		}
		return "Started unknown"
	case runtime.ContainerStatusStopped:
		exited := "Exited time unknown"
		if !state.ExitedAt.IsZero() {
			exited = fmt.Sprintf("Exited %s", detailTimeLabel(state.ExitedAt))
		}
		if state.ExitReason != "" {
			return exited + "  Reason " + state.ExitReason
		}
		return exited + "  Reason unknown"
	case runtime.ContainerStatusCreated:
		return "Not started"
	case runtime.ContainerStatusPaused:
		if !state.StartedAt.IsZero() {
			return fmt.Sprintf("Paused after start %s", detailTimeLabel(state.StartedAt))
		}
		return "Paused"
	default:
		return "State unknown"
	}
}

func detailTimeLabel(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	return fmt.Sprintf("%s ago", formatAge(ts))
}
