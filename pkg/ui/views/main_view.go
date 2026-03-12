package views

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/rivo/tview"
)

// TabIndex represents the active tab
type TabIndex int

const (
	TabContainers TabIndex = iota
	TabImages
	TabPods
)

// MainView is the top-level view containing tab navigation and list views
type MainView struct {
	*tview.Flex

	app    *tview.Application
	rt     runtime.Runtime
	ctx    context.Context
	cancel context.CancelFunc

	// Tab bar
	tabBar *tview.TextView

	// Content area (pages for tab panels)
	content *tview.Pages

	// Sub-views
	containerList *ContainerTreeView
	imageList     *ImageListView
	podList       *PodListView

	// State
	activeTab TabIndex
	mu        sync.Mutex

	// Refresh
	refreshInterval time.Duration
	refreshStop     chan struct{}

	// Callbacks
	onContainerSelect func(containerID string)
}

// NewMainView creates the main view with tab switching
func NewMainView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *MainView {
	childCtx, cancel := context.WithCancel(ctx)

	v := &MainView{
		Flex:            tview.NewFlex().SetDirection(tview.FlexRow),
		app:             app,
		rt:              rt,
		ctx:             childCtx,
		cancel:          cancel,
		content:         tview.NewPages(),
		activeTab:       TabContainers,
		refreshInterval: 3 * time.Second,
		refreshStop:     make(chan struct{}),
	}

	// Create sub-views
	v.containerList = NewContainerTreeView(app, rt)
	v.imageList = NewImageListView(app, rt)
	v.podList = NewPodListView(app, rt)

	// Tab bar
	v.tabBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	v.tabBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	// Register tab pages
	v.content.AddPage("containers", v.containerList, true, true)
	v.content.AddPage("images", v.imageList, true, false)
	v.content.AddPage("pods", v.podList, true, false)

	// Layout: tabBar (1 row) + content (flex)
	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.content, 0, 1, true)

	v.updateTabBar()

	return v
}

// SetContainerSelectFunc sets the callback when a container is selected
func (v *MainView) SetContainerSelectFunc(handler func(containerID string)) {
	v.onContainerSelect = handler
	v.containerList.SetSelectedFunc(handler)
}

// HandleInput processes key events for tab switching and refresh
func (v *MainView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	// Always allow Ctrl+C to propagate for global quit
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Rune() {
	case '1':
		v.switchTab(TabContainers)
		return nil
	case '2':
		v.switchTab(TabImages)
		return nil
	case '3':
		v.switchTab(TabPods)
		return nil
	case 'r', 'R':
		go v.refreshCurrentTab()
		return nil
	}

	switch event.Key() {
	case tcell.KeyTab:
		v.nextTab()
		return nil
	case tcell.KeyBacktab:
		v.prevTab()
		return nil
	}

	return event
}

// switchTab switches to the given tab
func (v *MainView) switchTab(tab TabIndex) {
	v.mu.Lock()
	v.activeTab = tab
	v.mu.Unlock()

	switch tab {
	case TabContainers:
		v.content.SwitchToPage("containers")
	case TabImages:
		v.content.SwitchToPage("images")
	case TabPods:
		v.content.SwitchToPage("pods")
	}
	v.updateTabBar()
}

// nextTab cycles to the next tab
func (v *MainView) nextTab() {
	next := (v.activeTab + 1) % 3
	v.switchTab(next)
}

// prevTab cycles to the previous tab
func (v *MainView) prevTab() {
	prev := (v.activeTab + 2) % 3
	v.switchTab(prev)
}

// updateTabBar renders the tab bar with active tab highlighted
func (v *MainView) updateTabBar() {
	tabs := []struct {
		label string
		key   string
	}{
		{"Containers", "1"},
		{"Images", "2"},
		{"Pods", "3"},
	}

	text := " "
	for i, t := range tabs {
		if TabIndex(i) == v.activeTab {
			text += fmt.Sprintf("[black:aqua] %s(%s) [-:-] ", t.label, t.key)
		} else {
			text += fmt.Sprintf("[white:darkslategray] %s(%s) [-:-] ", t.label, t.key)
		}
	}
	text += "  [darkgray]Tab[white]:next  [darkgray]?[white]:help  [darkgray]q[white]:quit"

	v.tabBar.SetText(text)
}

// RefreshAll loads data for all tabs (sync version for initial load)
func (v *MainView) RefreshAll() {
	v.mu.Lock()
	tab := v.activeTab
	v.mu.Unlock()

	switch tab {
	case TabContainers:
		v.containerList.Refresh(v.ctx)
	case TabImages:
		v.imageList.Refresh(v.ctx)
	case TabPods:
		v.podList.Refresh(v.ctx)
	}
	// Note: Don't call QueueUpdateDraw here - it blocks before Run() starts
}

// refreshCurrentTab refreshes only the active tab's data
func (v *MainView) refreshCurrentTab() {
	v.mu.Lock()
	tab := v.activeTab
	v.mu.Unlock()

	var err error
	switch tab {
	case TabContainers:
		err = v.containerList.Refresh(v.ctx)
	case TabImages:
		err = v.imageList.Refresh(v.ctx)
	case TabPods:
		err = v.podList.Refresh(v.ctx)
	}

	if err != nil {
		// Views handle their own error display in status bars
		return
	}
}

// StartAutoRefresh starts a background goroutine to periodically refresh data
func (v *MainView) StartAutoRefresh() {
	go func() {
		ticker := time.NewTicker(v.refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				v.refreshCurrentTab()
			case <-v.refreshStop:
				return
			case <-v.ctx.Done():
				return
			}
		}
	}()
}

// StopAutoRefresh stops the auto-refresh goroutine
func (v *MainView) StopAutoRefresh() {
	select {
	case v.refreshStop <- struct{}{}:
	default:
	}
	v.cancel()
}

// GetActiveTab returns the current active tab
func (v *MainView) GetActiveTab() TabIndex {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.activeTab
}

// GetFocusPrimitive returns the primitive that should receive focus
func (v *MainView) GetFocusPrimitive() tview.Primitive {
	return v.containerList
}
