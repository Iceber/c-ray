package ui

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/icebergu/c-ray/pkg/ui/views"
	"github.com/rivo/tview"
)

// App represents the main TUI application backed by a runtime.
type App struct {
	tviewApp *tview.Application
	pages    *tview.Pages
	runtime  runtime.Runtime
	ctx      context.Context
	cancel   context.CancelFunc
	nav      *Navigator
	stopOnce sync.Once
	stopping atomic.Bool

	mainView   *views.MainView
	detailView *views.ContainerDetailView
	imageView  *views.ImageDetailView
}

// NewApp creates a new TUI application.
func NewApp(rt runtime.Runtime) *App {
	ctx, cancel := context.WithCancel(context.Background())
	app := &App{
		tviewApp: tview.NewApplication(),
		pages:    tview.NewPages(),
		runtime:  rt,
		ctx:      ctx,
		cancel:   cancel,
	}
	views.TrackApplicationLifecycle(app.tviewApp)
	app.stopAfterFirstDraw()
	app.nav = NewNavigator(app.tviewApp, app.pages)
	app.setupUI()
	app.setupKeybindings()
	return app
}

func (a *App) stopAfterFirstDraw() {
	previous := a.tviewApp.GetAfterDrawFunc()
	a.tviewApp.SetAfterDrawFunc(func(screen tcell.Screen) {
		if previous != nil {
			previous(screen)
		}
		if a.stopping.Load() {
			go a.tviewApp.Stop()
		}
	})
}

func (a *App) setupUI() {
	a.tviewApp.SetRoot(a.pages, true)

	a.mainView = views.NewMainView(a.tviewApp, a.runtime, a.ctx)
	a.detailView = views.NewContainerDetailView(a.tviewApp, a.ctx)
	a.imageView = views.NewImageDetailView(a.tviewApp, a.runtime, a.ctx)

	a.mainView.SetContainerSelectFunc(func(c runtime.Container) {
		a.detailView.SetContainer(c)
		a.nav.NavigateToAndFocus(PageContainerDetail, a.detailView.GetFocusPrimitive())
	})
	a.mainView.SetImageSelectFunc(func(img runtime.Image) {
		a.imageView.SetImage(img)
		a.nav.NavigateToAndFocus(PageImageDetail, a.imageView.GetFocusPrimitive())
	})

	a.detailView.SetBackFunc(func() {
		a.nav.Back()
		// Re-resolve focus dynamically: the registered focus for PageMain is a static
		// snapshot captured at startup (containerList). If the user navigated back from
		// a different tab, we must override with the currently active tab's widget.
		a.tviewApp.SetFocus(a.mainView.GetFocusPrimitive())
	})
	a.imageView.SetBackFunc(func() {
		a.nav.Back()
		a.tviewApp.SetFocus(a.mainView.GetFocusPrimitive())
	})

	a.pages.AddPage(string(PageMain), a.mainView, true, true)
	a.pages.AddPage(string(PageContainerDetail), a.detailView, true, false)
	a.pages.AddPage(string(PageImageDetail), a.imageView, true, false)

	a.nav.RegisterFocus(PageMain, a.mainView.GetFocusPrimitive())
	a.nav.RegisterFocus(PageContainerDetail, a.detailView)
	a.nav.RegisterFocus(PageImageDetail, a.imageView)
	a.nav.NavigateTo(PageMain)
}

func (a *App) setupKeybindings() {
	a.tviewApp.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyCtrlC {
			a.Stop()
			return nil
		}
		if event.Rune() == '?' {
			a.showHelp()
			return nil
		}

		switch a.nav.CurrentPage() {
		case PageMain:
			if event.Rune() == 'q' || event.Rune() == 'Q' {
				a.Stop()
				return nil
			}
			return a.mainView.HandleInput(event)
		case PageContainerDetail:
			if event.Rune() == 'Q' {
				a.Stop()
				return nil
			}
			return a.detailView.HandleInput(event)
		case PageImageDetail:
			if event.Rune() == 'Q' {
				a.Stop()
				return nil
			}
			return a.imageView.HandleInput(event)
		default:
			if event.Rune() == 'q' || event.Rune() == 'Q' {
				a.Stop()
				return nil
			}
		}
		return event
	})
}

func (a *App) showHelp() {
	accentTag := fmt.Sprintf("[%s::b]", components.ColorName(components.ColorFgAccent))
	keyTag := fmt.Sprintf("[%s]", components.ColorName(components.ColorFgKey))
	sectionTag := fmt.Sprintf("[%s::b]", components.ColorName(components.ColorFgAccentAlt))
	mutedTag := fmt.Sprintf("[%s]", components.ColorName(components.ColorFgMuted))
	reset := "[-:-:-]"
	resetFg := "[-]"

	helpText := accentTag + "c-ray v1 - Keybindings" + reset + `

` + sectionTag + "Global" + reset + `
  ` + keyTag + `?` + resetFg + `           Show this help
  ` + keyTag + `q/Ctrl+C` + resetFg + `    Quit / Back

` + sectionTag + "Main View" + reset + `
  ` + keyTag + `1/2/3` + resetFg + `       Switch tab (Containers/Images/Pods)
  ` + keyTag + `Tab` + resetFg + `         Next tab
  ` + keyTag + `Shift+Tab` + resetFg + `   Previous tab
	` + keyTag + `Enter` + resetFg + `       Open selected resource detail
  ` + keyTag + `e` + resetFg + `           Toggle expand/collapse
  ` + keyTag + `a` + resetFg + `           Expand/collapse all pods
  ` + keyTag + `r` + resetFg + `           Refresh data

` + sectionTag + "Detail View" + reset + `
  ` + keyTag + `Esc/q` + resetFg + `       Back to list
	` + keyTag + `1-5` + resetFg + `         Switch page in detail view
  ` + keyTag + `Tab` + resetFg + `         Next page
  ` + keyTag + `Shift+Tab` + resetFg + `   Previous page
  ` + keyTag + `r` + resetFg + `           Refresh data

` + sectionTag + "Processes Workspace" + reset + `
  ` + keyTag + `s` + resetFg + `           Switch to summary mode
  ` + keyTag + `g` + resetFg + `           Switch to tree mode
  ` + keyTag + `t` + resetFg + `           Switch to top mode
  ` + keyTag + `[ / ]` + resetFg + `       Cycle process sub-tabs
  ` + keyTag + `c` + resetFg + `           Sort by CPU
  ` + keyTag + `m` + resetFg + `           Sort by Memory
  ` + keyTag + `p` + resetFg + `           Sort by PID
  ` + keyTag + `i` + resetFg + `           Sort by I/O

` + sectionTag + "Filesystem Workspace" + reset + `
  ` + keyTag + `l` + resetFg + `           Switch to Rootfs Layers
  ` + keyTag + `m` + resetFg + `           Switch to Mounts
  ` + keyTag + `i` + resetFg + `           Toggle layer file browser in Rootfs Layers

` + sectionTag + "Tree Views" + reset + `
  ` + keyTag + `e` + resetFg + `           Toggle expand/collapse
  ` + keyTag + `a` + resetFg + `           Expand/collapse all

` + mutedTag + `Press Esc or Enter to close` + resetFg

	modal := tview.NewModal().
		SetText(helpText).
		AddButtons([]string{"Close"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("help")
		})
	modal.SetBackgroundColor(components.ColorBgHeader)
	a.pages.AddPage("help", modal, true, true)
}

// Run starts the application.
func (a *App) Run() error {
	if err := a.runtime.Connect(a.ctx); err != nil {
		return fmt.Errorf("failed to connect to runtime: %w", err)
	}
	defer a.Stop()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGQUIT)
	defer func() {
		signal.Stop(sigCh)
		close(sigCh)
	}()
	go func() {
		for range sigCh {
			a.Stop()
			return
		}
	}()
	if a.stopping.Load() {
		return nil
	}

	a.mainView.StartAutoRefresh()
	go a.mainView.RefreshAll()
	return a.tviewApp.Run()
}

// Stop stops the application.
func (a *App) Stop() {
	a.stopping.Store(true)
	a.stopOnce.Do(func() {
		if a.mainView != nil {
			a.mainView.StopAutoRefresh()
		}
		if a.detailView != nil {
			a.detailView.Leave()
		}
		a.cancel()
		a.runtime.Close()
		views.UntrackApplicationLifecycle(a.tviewApp)
	})
	a.tviewApp.Stop()
}
