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

// TabIndex represents the active tab.
type TabIndex int

const (
	TabContainers TabIndex = iota
	TabImages
	TabPods
)

// MainView is the top-level view containing tab navigation and list views.
type MainView struct {
	*tview.Flex

	app *tview.Application
	rt  runtime.Runtime
	ctx context.Context

	tabBar  *tview.TextView
	content *tview.Pages

	containerList *ContainerTreeView
	imageList     *ImageListView
	podList       *PodListView

	activeTab       TabIndex
	mu              sync.Mutex
	refreshInterval time.Duration
	refreshStop     chan struct{}
	refreshCtx      context.Context
	refreshCancel   context.CancelFunc

	onContainerSelect func(c runtime.Container)
}

// NewMainView creates the main view with tab switching.
func NewMainView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *MainView {
	childCtx, cancel := context.WithCancel(ctx)
	v := &MainView{
		Flex:            tview.NewFlex().SetDirection(tview.FlexRow),
		app:             app,
		rt:              rt,
		ctx:             childCtx,
		content:         tview.NewPages(),
		activeTab:       TabContainers,
		refreshInterval: 3 * time.Second,
		refreshStop:     make(chan struct{}),
		refreshCtx:      childCtx,
		refreshCancel:   cancel,
	}

	v.containerList = NewContainerTreeView(app, rt)
	v.imageList = NewImageListView(app, rt)
	v.podList = NewPodListView(app, rt)

	v.tabBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.tabBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	v.content.AddPage("containers", v.containerList, true, true)
	v.content.AddPage("images", v.imageList, true, false)
	v.content.AddPage("pods", v.podList, true, false)

	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.content, 0, 1, true)
	v.updateTabBar()
	return v
}

// SetContainerSelectFunc sets the callback when a container is selected.
func (v *MainView) SetContainerSelectFunc(handler func(c runtime.Container)) {
	v.onContainerSelect = handler
	v.containerList.SetSelectedFunc(handler)
}

// HandleInput processes key events for tab switching and refresh.
func (v *MainView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
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
		v.switchTab((v.activeTab + 1) % 3)
		return nil
	case tcell.KeyBacktab:
		v.switchTab((v.activeTab + 2) % 3)
		return nil
	}
	return event
}

func (v *MainView) switchTab(tab TabIndex) {
	v.mu.Lock()
	v.activeTab = tab
	v.mu.Unlock()

	switch tab {
	case TabContainers:
		v.content.SwitchToPage("containers")
		v.app.SetFocus(v.containerList.GetFocusPrimitive())
	case TabImages:
		v.content.SwitchToPage("images")
		v.app.SetFocus(v.imageList.GetFocusPrimitive())
	case TabPods:
		v.content.SwitchToPage("pods")
		v.app.SetFocus(v.podList.GetFocusPrimitive())
	}
	v.updateTabBar()
}

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

// RefreshAll loads data for the active tab.
func (v *MainView) RefreshAll() {
	v.refreshCurrentTab()
}

func (v *MainView) refreshCurrentTab() {
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
}

// StartAutoRefresh starts periodic refresh.
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

// StopAutoRefresh stops the auto-refresh goroutine.
func (v *MainView) StopAutoRefresh() {
	select {
	case v.refreshStop <- struct{}{}:
	default:
	}
	v.refreshCancel()
}

// GetFocusPrimitive returns the inner widget that should receive keyboard focus.
func (v *MainView) GetFocusPrimitive() tview.Primitive {
	switch v.activeTab {
	case TabImages:
		return v.imageList.GetFocusPrimitive()
	case TabPods:
		return v.podList.GetFocusPrimitive()
	default:
		return v.containerList.GetFocusPrimitive()
	}
}
