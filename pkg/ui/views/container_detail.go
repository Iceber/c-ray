package views

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// DetailTab represents the active tab in the detail view's lower half
type DetailTab int

const (
	DetailTabTop DetailTab = iota
	DetailTabProcessTree
	DetailTabMounts
	DetailTabImageLayers
	DetailTabRuntime
)

// ContainerDetailView displays detailed information about a container
type ContainerDetailView struct {
	*tview.Flex

	app *tview.Application
	rt  runtime.Runtime
	ctx context.Context

	// Current container
	containerID string
	detail      *models.ContainerDetail

	// Layout components
	titleBar   *tview.TextView
	overview   *components.InfoPanel
	tabBar     *tview.TextView
	tabContent *tview.Pages
	statusBar  *tview.TextView

	// Sub-views for tabs
	topView         *TopView
	processTreeView *ProcessTreeView
	mountsView      *MountsView
	imageLayersView *ImageLayersView
	runtimeInfoView *RuntimeInfoView

	// Tab state
	activeTab DetailTab

	// Callbacks
	onBack func()
}

// NewContainerDetailView creates a new container detail view
func NewContainerDetailView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *ContainerDetailView {
	v := &ContainerDetailView{
		Flex:      tview.NewFlex().SetDirection(tview.FlexRow),
		app:       app,
		rt:        rt,
		ctx:       ctx,
		activeTab: DetailTabTop,
	}

	v.setupLayout()
	return v
}

// setupLayout creates the detail view layout
func (v *ContainerDetailView) setupLayout() {
	// Title bar - shows container name and ID
	v.titleBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	v.titleBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	// Overview panel - upper half with container information
	v.overview = components.NewInfoPanel()
	v.overview.SetBorder(true).
		SetBorderColor(tcell.ColorDarkCyan).
		SetTitle(" Overview ").
		SetTitleColor(tcell.ColorAqua)

	// Tab bar for lower half
	v.tabBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	v.tabBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	// Tab content area
	v.tabContent = tview.NewPages()

	// Create sub-views for tabs
	v.topView = NewTopView(v.app, v.rt, v.ctx)
	v.processTreeView = NewProcessTreeView(v.app, v.rt, v.ctx)
	v.mountsView = NewMountsView(v.app, v.rt, v.ctx)
	v.imageLayersView = NewImageLayersView(v.app, v.rt, v.ctx)
	v.runtimeInfoView = NewRuntimeInfoView(v.rt)

	v.tabContent.AddPage("top", v.topView, true, true)
	v.tabContent.AddPage("process_tree", v.processTreeView, true, false)
	v.tabContent.AddPage("mounts", v.mountsView, true, false)
	v.tabContent.AddPage("image_layers", v.imageLayersView, true, false)
	v.tabContent.AddPage("runtime", v.runtimeInfoView, true, false)

	// Status bar
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	// Assemble layout: title(1) + overview(flex) + tabBar(1) + tabContent(flex) + statusBar(1)
	v.Flex.AddItem(v.titleBar, 1, 0, false)
	v.Flex.AddItem(v.overview, 0, 1, false)
	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.tabContent, 0, 1, false)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.updateTabBar()
	v.updateStatusBar()
}

// SetContainer sets the container to display and loads its details
func (v *ContainerDetailView) SetContainer(containerID string) {
	v.containerID = containerID

	// Update sub-views with container ID
	v.topView.SetContainer(containerID)
	v.processTreeView.SetContainer(containerID)
	v.mountsView.SetContainer(containerID)
	v.imageLayersView.SetContainer(containerID)
	v.runtimeInfoView.SetContainer(containerID)

	// Load overview data asynchronously to avoid blocking UI
	go v.Refresh()

	// Load initial top data and start auto-refresh
	go v.topView.Refresh(v.ctx)
	v.topView.StartAutoRefresh()
}

// SetBackFunc sets the callback for when user navigates back
func (v *ContainerDetailView) SetBackFunc(handler func()) {
	v.onBack = handler
}

// Leave is called when navigating away from the detail view
func (v *ContainerDetailView) Leave() {
	v.topView.StopAutoRefresh()
}

// Refresh loads and displays container detail data
func (v *ContainerDetailView) Refresh() {
	if v.containerID == "" {
		return
	}

	detail, err := v.rt.GetContainerDetail(v.ctx, v.containerID)
	if err != nil {
		v.app.QueueUpdateDraw(func() {
			v.overview.SetItems([]components.InfoItem{
				{Label: "Error", Value: fmt.Sprintf("Failed to load: %v", err), Color: tcell.ColorRed},
			})
		})
		return
	}

	v.detail = detail

	v.imageLayersView.SetDetail(detail)

	v.app.QueueUpdateDraw(func() {
		v.renderTitle()
		v.renderOverview()
	})
}

// renderTitle updates the title bar
func (v *ContainerDetailView) renderTitle() {
	if v.detail == nil {
		return
	}

	name := v.detail.Name
	if name == "" {
		name = v.detail.ID
	}

	id := v.detail.ID
	if len(id) > 12 {
		id = id[:12]
	}

	statusColor := "white"
	switch v.detail.Status {
	case models.ContainerStatusRunning:
		statusColor = "green"
	case models.ContainerStatusPaused:
		statusColor = "yellow"
	case models.ContainerStatusStopped:
		statusColor = "red"
	case models.ContainerStatusCreated:
		statusColor = "darkcyan"
	}

	v.titleBar.SetText(fmt.Sprintf(
		" [aqua::b]Container[-:-:-]  [white]%s[gray] (%s)  [%s]%s[-]",
		name, id, statusColor, string(v.detail.Status),
	))
}

// renderOverview renders the overview information panel
func (v *ContainerDetailView) renderOverview() {
	if v.detail == nil {
		return
	}

	sections := []components.InfoSection{}

	// Section 1: Basic Info
	basicItems := []components.InfoItem{
		{Label: "Container ID:", Value: v.detail.ID},
		{Label: "Name:", Value: v.detail.Name},
		{Label: "Status:", Value: string(v.detail.Status), Color: statusColor(v.detail.Status)},
		{Label: "Created:", Value: v.detail.CreatedAt.Format("2006-01-02 15:04:05")},
	}
	if !v.detail.StartedAt.IsZero() {
		basicItems = append(basicItems, components.InfoItem{
			Label: "Started:", Value: v.detail.StartedAt.Format("2006-01-02 15:04:05"),
		})
		basicItems = append(basicItems, components.InfoItem{
			Label: "Uptime:", Value: formatAge(v.detail.StartedAt),
		})
	}
	sections = append(sections, components.InfoSection{Title: "Basic", Items: basicItems})

	// Section 2: Process Info
	processItems := []components.InfoItem{
		{Label: "Host PID:", Value: fmt.Sprintf("%d", v.detail.PID)},
	}
	sections = append(sections, components.InfoSection{Title: "Process", Items: processItems})

	// Section 3: Image Info
	imageItems := []components.InfoItem{
		{Label: "Image:", Value: v.detail.ImageName},
	}
	sections = append(sections, components.InfoSection{Title: "Image", Items: imageItems})

	// Section 4: Pod Info (if applicable)
	if v.detail.PodName != "" {
		podItems := []components.InfoItem{
			{Label: "Pod Name:", Value: v.detail.PodName},
			{Label: "Pod Namespace:", Value: v.detail.PodNamespace},
		}
		if v.detail.PodUID != "" {
			podItems = append(podItems, components.InfoItem{
				Label: "Pod UID:", Value: v.detail.PodUID,
			})
		}
		sections = append(sections, components.InfoSection{Title: "Pod", Items: podItems})
	}

	v.overview.SetSections(sections)
}

// HandleInput processes key events for the detail view
func (v *ContainerDetailView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	// Global quit - always allow exit
	if event.Key() == tcell.KeyCtrlC {
		return event // Let parent handle it
	}
	// Exit application with 'Q' (uppercase)
	if event.Rune() == 'Q' {
		return event // Let parent handle global quit
	}

	// Back navigation
	switch event.Key() {
	case tcell.KeyEscape:
		if v.onBack != nil {
			v.Leave()
			v.onBack()
		}
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
		switch v.activeTab {
		case DetailTabTop:
			go v.topView.Refresh(v.ctx)
		case DetailTabProcessTree:
			go v.processTreeView.Refresh(v.ctx)
		case DetailTabMounts:
			go v.mountsView.Refresh(v.ctx)
		case DetailTabImageLayers:
			go v.imageLayersView.Refresh(v.ctx)
		case DetailTabRuntime:
			go v.runtimeInfoView.Refresh(v.ctx)
		}
		return nil
	// Tab switching
	case '1':
		v.switchTab(DetailTabTop)
		return nil
	case '2':
		v.switchTab(DetailTabProcessTree)
		return nil
	case '3':
		v.switchTab(DetailTabMounts)
		return nil
	case '4':
		v.switchTab(DetailTabImageLayers)
		return nil
	case '5':
		v.switchTab(DetailTabRuntime)
		return nil
	}

	// Delegate to active tab's input handler
	switch v.activeTab {
	case DetailTabTop:
		return v.topView.HandleInput(event)
	case DetailTabProcessTree:
		return v.processTreeView.HandleInput(event)
	case DetailTabMounts:
		return v.mountsView.HandleInput(event)
	case DetailTabImageLayers:
		return v.imageLayersView.HandleInput(event)
	case DetailTabRuntime:
		return v.runtimeInfoView.HandleInput(event)
	}

	return event
}

// switchTab switches the active detail tab
func (v *ContainerDetailView) switchTab(tab DetailTab) {
	v.activeTab = tab
	switch tab {
	case DetailTabTop:
		v.tabContent.SwitchToPage("top")
		v.app.SetFocus(v.topView.GetFocusPrimitive())
	case DetailTabProcessTree:
		v.tabContent.SwitchToPage("process_tree")
		go v.processTreeView.Refresh(v.ctx)
		v.app.SetFocus(v.processTreeView.GetFocusPrimitive())
	case DetailTabMounts:
		v.tabContent.SwitchToPage("mounts")
		go v.mountsView.Refresh(v.ctx)
		v.app.SetFocus(v.mountsView.GetFocusPrimitive())
	case DetailTabImageLayers:
		v.tabContent.SwitchToPage("image_layers")
		go v.imageLayersView.Refresh(v.ctx)
		v.app.SetFocus(v.imageLayersView.GetFocusPrimitive())
	case DetailTabRuntime:
		v.tabContent.SwitchToPage("runtime")
		go v.runtimeInfoView.Refresh(v.ctx)
		v.app.SetFocus(v.runtimeInfoView.GetFocusPrimitive())
	}
	v.updateTabBar()
}

// updateTabBar renders the tab bar
func (v *ContainerDetailView) updateTabBar() {
	tabs := []struct {
		label string
		key   string
	}{
		{"Top", "1"},
		{"Processes", "2"},
		{"Mounts", "3"},
		{"Layers", "4"},
		{"Runtime", "5"},
	}

	text := " "
	for i, t := range tabs {
		if DetailTab(i) == v.activeTab {
			text += fmt.Sprintf("[black:aqua] %s(%s) [-:-] ", t.label, t.key)
		} else {
			text += fmt.Sprintf("[white:darkslategray] %s(%s) [-:-] ", t.label, t.key)
		}
	}

	v.tabBar.SetText(text)
}

// updateStatusBar renders the status bar
func (v *ContainerDetailView) updateStatusBar() {
	v.statusBar.SetText(" [yellow]Esc/q[white]:back  [yellow]1-5[white]:tabs  [yellow]r[white]:refresh  [yellow]?[white]:help")
}

// statusColor returns the tcell color for a container status
func statusColor(status models.ContainerStatus) tcell.Color {
	switch status {
	case models.ContainerStatusRunning:
		return tcell.ColorGreen
	case models.ContainerStatusPaused:
		return tcell.ColorYellow
	case models.ContainerStatusStopped:
		return tcell.ColorRed
	case models.ContainerStatusCreated:
		return tcell.ColorDarkCyan
	default:
		return tcell.ColorWhite
	}
}

// formatBytes formats bytes into a human-readable string
func formatBytes(b int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case b >= GB:
		return fmt.Sprintf("%.2f GiB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.2f MiB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.2f KiB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
