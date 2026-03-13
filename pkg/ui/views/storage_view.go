package views

import (
	"context"
	"fmt"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
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

	app         *tview.Application
	rt          runtime.Runtime
	ctx         context.Context
	tabBar      *tview.TextView
	pages       *tview.Pages
	layersView  *ImageLayersView
	mountsView  *MountsView
	activeMode  StorageMode
	containerID string
	detail      *models.ContainerDetail
	runtimeInfo *models.ContainerDetail
	mu          sync.Mutex
}

// NewStorageView creates the Filesystem workspace.
func NewStorageView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *StorageView {
	v := &StorageView{
		Flex:       tview.NewFlex().SetDirection(tview.FlexRow),
		app:        app,
		rt:         rt,
		ctx:        ctx,
		layersView: NewImageLayersView(app, rt, ctx),
		mountsView: NewMountsView(app, rt, ctx),
		activeMode: StorageModeLayers,
	}

	v.tabBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	v.tabBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	v.pages = tview.NewPages()
	v.pages.AddPage("layers", v.layersView, true, true)
	v.pages.AddPage("mounts", v.mountsView, true, false)

	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.pages, 0, 1, true)
	v.updateTabBar()

	return v
}

// SetContainer updates the active container.
func (v *StorageView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.runtimeInfo = nil
	v.detail = nil
	v.mu.Unlock()

	v.layersView.SetContainer(containerID)
	v.mountsView.SetContainer(containerID)
}

// SetDetail updates the overview detail used by subviews.
func (v *StorageView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
	activeMode := v.activeMode
	v.mu.Unlock()
	v.layersView.SetDetail(detail)
	if detail != nil && activeMode == StorageModeLayers {
		go v.layersView.Refresh(v.ctx)
	}
}

// Refresh refreshes runtime context and the active filesystem subview.
func (v *StorageView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	containerID := v.containerID
	detail := v.detail
	v.mu.Unlock()

	if containerID != "" {
		runtimeInfo, err := v.rt.GetContainerStorageInfo(ctx, containerID)
		if err == nil {
			v.mu.Lock()
			v.runtimeInfo = runtimeInfo
			v.mu.Unlock()
			v.layersView.SetRuntimeInfo(runtimeInfo)
			v.mountsView.SetRuntimeInfo(runtimeInfo)
		}
	}

	if detail != nil {
		v.layersView.SetDetail(detail)
	}

	if v.activeMode == StorageModeMounts {
		return v.mountsView.Refresh(ctx)
	}
	return v.layersView.Refresh(ctx)
}

// HandleInput switches between filesystem subviews and delegates input.
func (v *StorageView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event == nil {
		return nil
	}
	if event.Key() == tcell.KeyCtrlC {
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
	if mode == StorageModeMounts {
		v.pages.SwitchToPage("mounts")
		v.updateTabBar()
		if v.app != nil {
			v.app.SetFocus(v.mountsView.GetFocusPrimitive())
		}
		go v.Refresh(v.ctx)
		return
	}

	v.pages.SwitchToPage("layers")
	v.updateTabBar()
	if v.app != nil {
		v.app.SetFocus(v.layersView.GetFocusPrimitive())
	}
	go v.Refresh(v.ctx)
}

func (v *StorageView) updateTabBar() {
	modes := []struct {
		mode  StorageMode
		label string
		key   string
	}{
		{StorageModeLayers, "Rootfs Layers", "l"},
		{StorageModeMounts, "Mounts", "m"},
	}

	text := " [white]Filesystem:[-] "
	for _, mode := range modes {
		if mode.mode == v.activeMode {
			text += fmt.Sprintf("[black:aqua] %s(%s) [-:-] ", mode.label, mode.key)
		} else {
			text += fmt.Sprintf("[white:darkslategray] %s(%s) [-:-] ", mode.label, mode.key)
		}
	}
	v.tabBar.SetText(text)
}
