package views

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
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

	headerBar  *tview.TextView
	contextBar *tview.TextView
	tabBar     *components.TabBar
	content    *tview.Pages
	footer     *components.Footer

	summaryView    *DetailGridView
	processesView  *ProcessesView
	filesystemView *StorageView
	runtimeView    *RuntimeInfoView
	networkView    *NetworkInfoView

	activeTab  DetailTab
	onBack     func()
	refreshGen uint64
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
	// Header band: container identity + status
	v.headerBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.headerBar.SetBackgroundColor(components.ColorBgHeader)

	// Context bar: pod / image / timing context
	v.contextBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.contextBar.SetBackgroundColor(components.ColorBgHeader)

	// Tab bar
	v.tabBar = components.NewTabBar()

	// Footer
	v.footer = components.NewFooter()

	v.summaryView = NewDetailGridView(v.app)
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

	v.Flex.AddItem(v.headerBar, 1, 0, false)
	v.Flex.AddItem(v.contextBar, 1, 0, false)
	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.content, 0, 1, false)
	v.Flex.AddItem(v.footer, 1, 0, false)

	v.renderHeader()
	v.updateTabBar()
	v.updateFooter()
}

// SetContainer sets the container handle to display and loads initial data.
func (v *ContainerDetailView) SetContainer(c runtime.Container) {
	atomic.AddUint64(&v.refreshGen, 1)
	v.container = c
	v.info = nil
	v.state = nil
	v.refreshedAt = time.Time{}

	v.summaryView.SetContainer(c)
	v.processesView.SetContainer(c)
	v.filesystemView.SetContainer(c)
	v.filesystemView.SetContainerPID(0)
	v.runtimeView.SetContainer(c)
	v.networkView.SetContainer(c)

	queueUpdateDraw(v.app, func() {
		v.renderHeader()
		v.updateFooter()
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

	gen := atomic.LoadUint64(&v.refreshGen)

	info, err := v.container.Info(v.ctx)
	if err != nil {
		if atomic.LoadUint64(&v.refreshGen) != gen {
			return
		}
		queueUpdateDraw(v.app, func() {
			v.headerBar.SetText(fmt.Sprintf(" [%s]Failed to load container: %v[-]", components.ColorName(components.ColorFgError), err))
			v.contextBar.SetText(" ")
		})
		return
	}

	state, _ := v.container.State(v.ctx)

	// Discard stale result if container was switched while we were loading.
	if atomic.LoadUint64(&v.refreshGen) != gen {
		return
	}

	v.info = info
	v.state = state
	v.refreshedAt = time.Now()
	v.filesystemView.SetContainerPID(info.PID)

	queueUpdateDraw(v.app, func() {
		v.renderHeader()
		v.updateFooter()
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
		v.headerBar.SetText(fmt.Sprintf(" %s Loading container detail...", components.Muted("⏳")))
		v.contextBar.SetText(" ")
		return
	}

	name := v.info.Name
	if name == "" {
		name = shortID(v.info.ID)
	}

	v.headerBar.SetText(fmt.Sprintf(
		" %s  %s  %s",
		components.Bright(name),
		components.Muted(shortID(v.info.ID)),
		detailStateTag(v.info, v.state),
	))

	var ctx []string
	if v.info.PodNamespace != "" || v.info.PodName != "" {
		ctx = append(ctx, components.KV("pod ", fallbackValue(v.info.PodNamespace, "?")+"/"+fallbackValue(v.info.PodName, "?")))
	}
	if v.info.Image != "" {
		ctx = append(ctx, components.KV("image ", truncateForCard(v.info.Image, 40)))
	}
	ctx = append(ctx, components.KV("created ", detailTimeLabel(v.info.CreatedAt)))
	if !v.refreshedAt.IsZero() {
		ctx = append(ctx, components.Dim("refreshed "+v.refreshedAt.Format("15:04:05")))
	}
	v.contextBar.SetText(" " + joinSpaced(ctx))
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
		// Give the active subview first chance to consume Tab (e.g. layer browser pane switch).
		if v.activeTab == DetailTabFilesystem && v.filesystemView.HandleInput(event) == nil {
			return nil
		}
		v.switchTab((v.activeTab + 1) % 5)
		return nil
	case tcell.KeyBacktab:
		if v.activeTab == DetailTabFilesystem && v.filesystemView.HandleInput(event) == nil {
			return nil
		}
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
	v.updateFooter()
}

var detailTabs = []components.TabDef{
	{Label: "Info", Key: "1"},
	{Label: "Processes", Key: "2"},
	{Label: "Filesystem", Key: "3"},
	{Label: "Runtime", Key: "4"},
	{Label: "Network", Key: "5"},
}

func (v *ContainerDetailView) updateTabBar() {
	v.tabBar.Update(detailTabs, int(v.activeTab))
}

func (v *ContainerDetailView) updateFooter() {
	hints := []components.FooterHint{
		{Key: "Esc", Action: "back"},
		{Key: "1-5", Action: "pages"},
		{Key: "Tab", Action: "next"},
		{Key: "r", Action: "refresh"},
	}
	switch v.activeTab {
	case DetailTabSummary:
		hints = append(hints, components.FooterHint{Key: "p/f", Action: "focus panes"}, components.FooterHint{Key: "e", Action: "toggle"})
	case DetailTabProcesses:
		hints = append(hints, components.FooterHint{Key: "s/g/t", Action: "process tabs"}, components.FooterHint{Key: "[/]", Action: "cycle"})
	case DetailTabFilesystem:
		hints = append(hints, components.FooterHint{Key: "l/m", Action: "filesystem tabs"})
	case DetailTabRuntime, DetailTabNetwork:
		hints = append(hints, components.FooterHint{Key: "e", Action: "toggle"}, components.FooterHint{Key: "a", Action: "expand/collapse"})
	}
	v.footer.Update(hints)
}

// --- Header helpers ---

func detailStateTag(info *runtime.ContainerInfo, state *runtime.ContainerState) string {
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
			return components.StatusTag(fmt.Sprintf("RUNNING PID %d", pid), components.ColorFgTag, components.ColorBgTagRun)
		}
		return components.StatusTag("RUNNING", components.ColorFgTag, components.ColorBgTagRun)
	case runtime.ContainerStatusStopped:
		if state != nil && state.ExitCode != nil {
			return components.StatusTag(fmt.Sprintf("EXITED %d", *state.ExitCode), components.ColorFgTag, components.ColorBgTagStop)
		}
		return components.StatusTag("EXITED", components.ColorFgTag, components.ColorBgTagStop)
	case runtime.ContainerStatusCreated:
		return components.StatusTag("CREATED", components.ColorFgTag, components.ColorBgTagInfo)
	case runtime.ContainerStatusPaused:
		return components.StatusTag("PAUSED", components.ColorFgTag, components.ColorBgTagWarn)
	default:
		return components.StatusTag("UNKNOWN", components.ColorFgMuted, components.ColorBgTabOff)
	}
}

func joinSpaced(parts []string) string {
	result := ""
	for i, p := range parts {
		if i > 0 {
			result += "   "
		}
		result += p
	}
	return result
}

func detailTimeLabel(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	return fmt.Sprintf("%s ago", formatAge(ts))
}
