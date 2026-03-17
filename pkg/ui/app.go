package ui

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
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

	mainView   *views.MainView
	detailView *views.ContainerDetailView
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
	app.nav = NewNavigator(app.tviewApp, app.pages)
	app.setupUI()
	app.setupKeybindings()
	return app
}

func (a *App) setupUI() {
	a.tviewApp.SetRoot(a.pages, true)

	a.mainView = views.NewMainView(a.tviewApp, a.runtime, a.ctx)
	a.detailView = views.NewContainerDetailView(a.tviewApp, a.ctx)

	a.mainView.SetContainerSelectFunc(func(c runtime.Container) {
		a.detailView.SetContainer(c)
		a.nav.NavigateToAndFocus(PageContainerDetail, a.detailView.GetFocusPrimitive())
	})

	a.detailView.SetBackFunc(func() {
		a.nav.Back()
	})

	a.pages.AddPage(string(PageMain), a.mainView, true, true)
	a.pages.AddPage(string(PageContainerDetail), a.detailView, true, false)

	a.nav.RegisterFocus(PageMain, a.mainView.GetFocusPrimitive())
	a.nav.RegisterFocus(PageContainerDetail, a.detailView)
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
	helpText := `[aqua::b]c-ray v1 - Keybindings[-:-:-]

[yellow::b]Global[-:-:-]
  [yellow]?[-]           Show this help
  [yellow]q/Ctrl+C[-]    Quit / Back

[yellow::b]Main View[-:-:-]
  [yellow]1/2/3[-]       Switch tab (Containers/Images/Pods)
  [yellow]Tab[-]         Next tab
  [yellow]Shift+Tab[-]   Previous tab
  [yellow]Enter[-]       Open container detail
  [yellow]e[-]           Toggle expand/collapse
  [yellow]a[-]           Expand/collapse all pods
  [yellow]r[-]           Refresh data

[yellow::b]Detail View[-:-:-]
  [yellow]Esc/q[-]       Back to list
  [yellow]1-5[-]         Switch page (Summary/Processes/Filesystem/Runtime/Network)
  [yellow]Tab[-]         Next page
  [yellow]Shift+Tab[-]   Previous page
  [yellow]r[-]           Refresh data

[yellow::b]Processes Workspace[-:-:-]
  [yellow]s[-]           Switch to summary mode
  [yellow]g[-]           Switch to tree mode
  [yellow]t[-]           Switch to top mode
  [yellow][ / ][-]       Cycle process sub-tabs
  [yellow]c[-]           Sort by CPU
  [yellow]m[-]           Sort by Memory
  [yellow]p[-]           Sort by PID
  [yellow]i[-]           Sort by I/O

[yellow::b]Filesystem Workspace[-:-:-]
  [yellow]l[-]           Switch to Rootfs Layers
  [yellow]m[-]           Switch to Mounts
  [yellow]i[-]           Toggle layer file browser in Rootfs Layers

[yellow::b]Tree Views[-:-:-]
  [yellow]e[-]           Toggle expand/collapse
  [yellow]a[-]           Expand/collapse all

[gray]Press Esc or Enter to close[-]`

	modal := tview.NewModal().
		SetText(helpText).
		AddButtons([]string{"Close"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			a.pages.RemovePage("help")
		})
	modal.SetBackgroundColor(tcell.ColorDarkSlateGray)
	a.pages.AddPage("help", modal, true, true)
}

// Run starts the application.
func (a *App) Run() error {
	if err := a.runtime.Connect(a.ctx); err != nil {
		return fmt.Errorf("failed to connect to runtime: %w", err)
	}
	a.mainView.StartAutoRefresh()
	go a.mainView.RefreshAll()
	return a.tviewApp.Run()
}

// Stop stops the application.
func (a *App) Stop() {
	if a.mainView != nil {
		a.mainView.StopAutoRefresh()
	}
	if a.detailView != nil {
		a.detailView.Leave()
	}
	a.cancel()
	a.runtime.Close()
	views.UntrackApplicationLifecycle(a.tviewApp)
	a.tviewApp.Stop()
}
