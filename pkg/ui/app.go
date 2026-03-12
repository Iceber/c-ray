package ui

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/views"
	"github.com/rivo/tview"
)

// App represents the main TUI application
type App struct {
	tviewApp *tview.Application
	pages    *tview.Pages
	runtime  runtime.Runtime
	ctx      context.Context
	cancel   context.CancelFunc

	// Navigation
	nav *Navigator

	// Views
	mainView   *views.MainView
	detailView *views.ContainerDetailView
}

// NewApp creates a new TUI application
func NewApp(rt runtime.Runtime) *App {
	ctx, cancel := context.WithCancel(context.Background())

	app := &App{
		tviewApp: tview.NewApplication(),
		pages:    tview.NewPages(),
		runtime:  rt,
		ctx:      ctx,
		cancel:   cancel,
	}

	app.nav = NewNavigator(app.tviewApp, app.pages)
	app.setupUI()
	app.setupKeybindings()

	return app
}

// setupUI initializes the UI components
func (a *App) setupUI() {
	// Set the root primitive
	a.tviewApp.SetRoot(a.pages, true)

	// Create the main list view with tab switching
	a.mainView = views.NewMainView(a.tviewApp, a.runtime, a.ctx)

	// Create the container detail view
	a.detailView = views.NewContainerDetailView(a.tviewApp, a.runtime, a.ctx)

	// Set container selection handler - navigate to detail page
	a.mainView.SetContainerSelectFunc(func(containerID string) {
		a.detailView.SetContainer(containerID)
		a.nav.NavigateTo(PageContainerDetail)
	})

	// Set back handler on detail view
	a.detailView.SetBackFunc(func() {
		a.nav.Back()
	})

	a.pages.AddPage(string(PageMain), a.mainView, true, true)
	a.pages.AddPage(string(PageContainerDetail), a.detailView, true, false)

	// Register focus primitives for each page
	a.nav.RegisterFocus(PageMain, a.mainView.GetFocusPrimitive())
	a.nav.RegisterFocus(PageContainerDetail, a.detailView)

	a.nav.NavigateTo(PageMain)
}

// setupKeybindings sets up global keybindings
func (a *App) setupKeybindings() {
	a.tviewApp.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		// Global quit keys - check first before anything else
		switch event.Key() {
		case tcell.KeyCtrlC:
			a.Stop()
			return nil
		}

		// Help popup
		if event.Rune() == '?' {
			a.showHelp()
			return nil
		}

		// Check current page for context-aware keybindings
		currentPage := a.nav.CurrentPage()

		switch currentPage {
		case PageMain:
			// Delegate to main view for tab switching and refresh
			if event.Rune() == 'q' || event.Rune() == 'Q' {
				a.Stop()
				return nil
			}
			return a.mainView.HandleInput(event)
		case PageContainerDetail:
			// Check for global quit first
			if event.Rune() == 'Q' {
				a.Stop()
				return nil
			}
			// Delegate to detail view for tab switching and navigation
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

// showHelp displays a help popup with keybinding information
func (a *App) showHelp() {
	helpText := `[aqua::b]c-ray - Keybindings[-:-:-]

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
  [yellow]1-5[-]         Switch tab (Top/Processes/Mounts/Layers/Runtime)
  [yellow]r[-]           Refresh data

[yellow::b]Top Tab[-:-:-]
  [yellow]c[-]           Sort by CPU
  [yellow]m[-]           Sort by Memory
  [yellow]p[-]           Sort by PID
  [yellow]i[-]           Sort by I/O

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

// Run starts the application
func (a *App) Run() error {
	// Connect to runtime
	if err := a.runtime.Connect(a.ctx); err != nil {
		return fmt.Errorf("failed to connect to runtime: %w", err)
	}

	// Initial data load (before event loop starts - don't use QueueUpdateDraw here)
	a.mainView.RefreshAll()

	// Start auto-refresh
	a.mainView.StartAutoRefresh()

	// Run the TUI application
	return a.tviewApp.Run()
}

// Stop stops the application
func (a *App) Stop() {
	if a.mainView != nil {
		a.mainView.StopAutoRefresh()
	}
	if a.detailView != nil {
		a.detailView.Leave()
	}
	a.cancel()
	a.runtime.Close()
	a.tviewApp.Stop()
}

// GetContext returns the application context
func (a *App) GetContext() context.Context {
	return a.ctx
}

// GetRuntime returns the runtime instance
func (a *App) GetRuntime() runtime.Runtime {
	return a.runtime
}

// GetPages returns the pages container
func (a *App) GetPages() *tview.Pages {
	return a.pages
}

// Refresh forces a UI refresh
func (a *App) Refresh() {
	a.tviewApp.Draw()
}
