package views

import (
	"context"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// StorageMode represents the active filesystem workspace.
type StorageMode int

const (
	StorageModeLayers StorageMode = iota
	StorageModeMounts
)

// StorageView hosts the Filesystem page subviews.
type StorageView struct {
	*tview.Flex

	app        *tview.Application
	ctx        context.Context
	tabBar     *components.TabBar
	pages      *tview.Pages
	layersView *ImageLayersView
	mountsView *MountsView
	activeMode StorageMode
	container  runtime.Container
	mu         sync.Mutex
}

// NewStorageView creates the Filesystem workspace.
func NewStorageView(app *tview.Application, ctx context.Context) *StorageView {
	v := &StorageView{
		Flex:       tview.NewFlex().SetDirection(tview.FlexRow),
		app:        app,
		ctx:        ctx,
		layersView: NewImageLayersView(app, ctx),
		mountsView: NewMountsView(app),
		activeMode: StorageModeLayers,
	}

	v.tabBar = components.NewTabBar()

	v.pages = tview.NewPages()
	v.pages.AddPage("layers", v.layersView, true, true)
	v.pages.AddPage("mounts", v.mountsView, true, false)

	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.pages, 0, 1, true)
	v.updateTabBar()
	return v
}

// SetContainer updates the active container.
func (v *StorageView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.mu.Unlock()
	v.layersView.SetContainer(c)
	v.mountsView.SetContainer(c)
}

// Refresh refreshes the active filesystem subview.
func (v *StorageView) Refresh(ctx context.Context) error {
	if v.activeMode == StorageModeMounts {
		return v.mountsView.Refresh(ctx)
	}
	return v.layersView.Refresh(ctx)
}

// HandleInput switches between filesystem subviews and delegates input.
func (v *StorageView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil || event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Rune() {
	case 'l', 'L':
		v.switchMode(StorageModeLayers)
		return nil
	case 'm', 'M':
		v.switchMode(StorageModeMounts)
		return nil
	}
	if v.activeMode == StorageModeMounts {
		return v.mountsView.HandleInput(event)
	}
	return v.layersView.HandleInput(event)
}

// GetFocusPrimitive returns the focus target of the active subview.
func (v *StorageView) GetFocusPrimitive() tview.Primitive {
	if v.activeMode == StorageModeMounts {
		return v.mountsView.GetFocusPrimitive()
	}
	return v.layersView.GetFocusPrimitive()
}

func (v *StorageView) switchMode(mode StorageMode) {
	if v.activeMode == mode {
		return
	}
	v.activeMode = mode
	switch mode {
	case StorageModeMounts:
		v.pages.SwitchToPage("mounts")
		if v.app != nil {
			v.app.SetFocus(v.mountsView.GetFocusPrimitive())
		}
	default:
		v.pages.SwitchToPage("layers")
		if v.app != nil {
			v.app.SetFocus(v.layersView.GetFocusPrimitive())
		}
	}
	v.updateTabBar()
	go v.Refresh(v.ctx)
}

var storageTabs = []components.TabDef{
	{Label: "Rootfs Layers", Key: "l"},
	{Label: "Mounts", Key: "m"},
}

func (v *StorageView) updateTabBar() {
	v.tabBar.Update(storageTabs, int(v.activeMode))
}
