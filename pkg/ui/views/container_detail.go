package views

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
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
	rt  runtime.Runtime
	ctx context.Context

	containerID string
	detail      *models.ContainerDetail
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
func NewContainerDetailView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *ContainerDetailView {
	v := &ContainerDetailView{
		Flex:      tview.NewFlex().SetDirection(tview.FlexRow),
		app:       app,
		rt:        rt,
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
	v.processesView = NewProcessesView(v.app, v.rt, v.ctx)
	v.filesystemView = NewStorageView(v.app, v.rt, v.ctx)
	v.runtimeView = NewRuntimeInfoView(v.rt)
	v.networkView = NewNetworkInfoView(v.rt)

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

// SetContainer sets the container to display and loads its details.
func (v *ContainerDetailView) SetContainer(containerID string) {
	v.containerID = containerID
	v.detail = nil
	v.refreshedAt = time.Time{}

	v.summaryView.SetDetail(nil)
	v.processesView.SetContainer(containerID)
	v.processesView.SetDetail(nil)
	v.filesystemView.SetContainer(containerID)
	v.filesystemView.SetDetail(nil)
	v.runtimeView.SetContainer(containerID)
	v.networkView.SetContainer(containerID)

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

// Refresh loads and displays container detail data.
func (v *ContainerDetailView) Refresh() {
	if v.containerID == "" {
		return
	}

	detail, err := v.rt.GetContainerDetail(v.ctx, v.containerID)
	if err != nil {
		queueUpdateDraw(v.app, func() {
			v.headerLine1.SetText(fmt.Sprintf(" [red]Failed to load container detail: %v[-]", err))
			v.headerLine2.SetText(" ")
			v.contextLine.SetText(" ")
		})
		return
	}

	v.enrichDetail(detail)

	v.detail = detail
	v.refreshedAt = time.Now()
	v.summaryView.SetDetail(detail)
	v.processesView.SetDetail(detail)
	v.filesystemView.SetDetail(detail)

	queueUpdateDraw(v.app, func() {
		v.renderHeader()
		v.updateStatusBar()
	})
}

func (v *ContainerDetailView) enrichDetail(detail *models.ContainerDetail) {
	if detail == nil {
		return
	}

	if runtimeDetail, err := v.rt.GetContainerRuntimeInfo(v.ctx, v.containerID); err == nil && runtimeDetail != nil {
		mergeRuntimeDetail(detail, runtimeDetail)
	}

	imageRef := detail.Image
	if imageRef == "" {
		imageRef = detail.ImageName
	}
	if imageRef == "" {
		return
	}

	if image, err := v.rt.GetImage(v.ctx, imageRef); err == nil && image != nil {
		if detail.ImageName == "" {
			detail.ImageName = image.Name
		}
		if detail.ImageID == "" {
			detail.ImageID = image.Digest
		}
	}

	if configInfo, err := v.rt.GetImageConfigInfo(v.ctx, imageRef); err == nil && configInfo != nil {
		detail.ImageConfig = configInfo
	}
}

func (v *ContainerDetailView) renderHeader() {
	if v.detail == nil {
		v.headerLine1.SetText(" [gray]Loading container detail...[-]")
		v.headerLine2.SetText(" ")
		v.contextLine.SetText(" ")
		return
	}

	name := v.detail.Name
	if name == "" {
		name = shortID(v.detail.ID)
	}

	v.headerLine1.SetText(fmt.Sprintf(
		" [white::b]%s[-:-:-] [gray](%s)[-]   %s   [gray]Created[-] %s",
		name,
		shortID(v.detail.ID),
		detailRuntimeHeadline(v.detail),
		detailTimeLabel(v.detail.CreatedAt),
	))
	v.headerLine2.SetText(" " + detailSecondaryHeadline(v.detail))

	if v.detail.PodNamespace != "" || v.detail.PodName != "" {
		v.contextLine.SetText(fmt.Sprintf(" [gray]Pod[-] [white]%s/%s[-]", fallbackValue(v.detail.PodNamespace, "?"), fallbackValue(v.detail.PodName, "?")))
	} else {
		v.contextLine.SetText(" ")
	}
}

// HandleInput processes key events for the detail view.
func (v *ContainerDetailView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	if event.Rune() == 'Q' {
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
		if v.activeTab == DetailTabProcesses {
			go v.processesView.Refresh(v.ctx)
		} else if v.activeTab == DetailTabFilesystem {
			go v.filesystemView.Refresh(v.ctx)
		} else if v.activeTab == DetailTabRuntime {
			go v.runtimeView.Refresh(v.ctx)
		} else if v.activeTab == DetailTabNetwork {
			go v.networkView.Refresh(v.ctx)
		}
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
	if v.activeTab == DetailTabProcesses {
		text += "  [yellow]s/g/t[white]:process tabs  [yellow][/[white]]:cycle"
	} else if v.activeTab == DetailTabFilesystem {
		text += "  [yellow]m/l[white]:filesystem tabs"
	} else if v.activeTab == DetailTabRuntime || v.activeTab == DetailTabNetwork {
		text += "  [yellow]e[white]:toggle  [yellow]a[white]:expand/collapse"
	}
	v.statusBar.SetText(text)
}

func detailRuntimeHeadline(detail *models.ContainerDetail) string {
	switch detail.Status {
	case models.ContainerStatusRunning:
		if detail.PID > 0 {
			return fmt.Sprintf("[green]PID %d[-]", detail.PID)
		}
		return "[green]Running[-]"
	case models.ContainerStatusStopped:
		if detail.ExitCode != nil {
			return fmt.Sprintf("[red]Exit %d[-]", *detail.ExitCode)
		}
		return "[red]Exit unknown[-]"
	case models.ContainerStatusCreated:
		return "[darkcyan]Created[-]"
	case models.ContainerStatusPaused:
		if detail.PID > 0 {
			return fmt.Sprintf("[yellow]Paused PID %d[-]", detail.PID)
		}
		return "[yellow]Paused[-]"
	default:
		return "[white]Unknown[-]"
	}
}

func detailSecondaryHeadline(detail *models.ContainerDetail) string {
	switch detail.Status {
	case models.ContainerStatusRunning:
		if !detail.StartedAt.IsZero() {
			return fmt.Sprintf("Started %s", detailTimeLabel(detail.StartedAt))
		}
		return "Started unknown"
	case models.ContainerStatusStopped:
		exited := "Exited time unknown"
		if !detail.ExitedAt.IsZero() {
			exited = fmt.Sprintf("Exited %s", detailTimeLabel(detail.ExitedAt))
		}
		if detail.ExitReason != "" {
			return exited + "  Reason " + detail.ExitReason
		}
		return exited + "  Reason unknown"
	case models.ContainerStatusCreated:
		return "Not started"
	case models.ContainerStatusPaused:
		if !detail.StartedAt.IsZero() {
			return fmt.Sprintf("Paused after start %s", detailTimeLabel(detail.StartedAt))
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

func fallbackValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func shortID(value string) string {
	if value == "" {
		return "unknown"
	}
	if len(value) <= 12 {
		return value
	}
	return value[:12]
}

func truncateForCard(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 3 {
		return value[:width]
	}
	return value[:width-3] + "..."
}

func mergeRuntimeDetail(base *models.ContainerDetail, runtimeDetail *models.ContainerDetail) {
	if base == nil || runtimeDetail == nil {
		return
	}

	if base.ImageName == "" {
		base.ImageName = runtimeDetail.ImageName
	}
	if base.ImageID == "" {
		base.ImageID = runtimeDetail.ImageID
	}
	if base.ImageConfig == nil {
		base.ImageConfig = runtimeDetail.ImageConfig
	}
	if base.SnapshotKey == "" {
		base.SnapshotKey = runtimeDetail.SnapshotKey
	}
	if base.Snapshotter == "" {
		base.Snapshotter = runtimeDetail.Snapshotter
	}
	if base.CGroupPath == "" {
		base.CGroupPath = runtimeDetail.CGroupPath
	}
	if base.CGroupVersion == 0 {
		base.CGroupVersion = runtimeDetail.CGroupVersion
	}
	if base.CGroupLimits == nil {
		base.CGroupLimits = runtimeDetail.CGroupLimits
	}
	if base.RuntimeProfile == nil {
		base.RuntimeProfile = runtimeDetail.RuntimeProfile
	}
	if base.PodNetwork == nil {
		base.PodNetwork = runtimeDetail.PodNetwork
	}
	if base.Namespaces == nil {
		base.Namespaces = runtimeDetail.Namespaces
	}
	if base.Mounts == nil {
		base.Mounts = runtimeDetail.Mounts
		base.MountCount = runtimeDetail.MountCount
	}
	if base.ProcessCount == 0 {
		base.ProcessCount = runtimeDetail.ProcessCount
	}
	if len(base.Environment) == 0 {
		base.Environment = runtimeDetail.Environment
	}
	if base.SharedPID == nil {
		base.SharedPID = runtimeDetail.SharedPID
	}
	if base.RestartCount == nil {
		base.RestartCount = runtimeDetail.RestartCount
	}
	if base.ExitedAt.IsZero() {
		base.ExitedAt = runtimeDetail.ExitedAt
	}
	if base.ExitCode == nil {
		base.ExitCode = runtimeDetail.ExitCode
	}
	if base.ExitReason == "" {
		base.ExitReason = runtimeDetail.ExitReason
	}
	if base.ShimPID == 0 {
		base.ShimPID = runtimeDetail.ShimPID
	}
	if base.OCIBundlePath == "" {
		base.OCIBundlePath = runtimeDetail.OCIBundlePath
	}
	if base.OCIRuntimeDir == "" {
		base.OCIRuntimeDir = runtimeDetail.OCIRuntimeDir
	}
	if base.WritableLayerPath == "" {
		base.WritableLayerPath = runtimeDetail.WritableLayerPath
	}
	if base.ReadOnlyLayerPath == "" {
		base.ReadOnlyLayerPath = runtimeDetail.ReadOnlyLayerPath
	}
	if base.RWLayerUsage == 0 {
		base.RWLayerUsage = runtimeDetail.RWLayerUsage
	}
	if base.RWLayerInodes == 0 {
		base.RWLayerInodes = runtimeDetail.RWLayerInodes
	}
	if base.IPAddress == "" {
		base.IPAddress = runtimeDetail.IPAddress
	}
	if len(base.PortMappings) == 0 {
		base.PortMappings = runtimeDetail.PortMappings
	}
}

// formatBytes formats bytes into a human-readable string.
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
