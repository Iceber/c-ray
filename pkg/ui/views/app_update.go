package views

import (
	"sync"
	"sync/atomic"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type appUpdateState struct {
	started atomic.Bool
}

var trackedApplications sync.Map

// TrackApplicationLifecycle marks a tview application as started after its first draw.
func TrackApplicationLifecycle(app *tview.Application) {
	if app == nil {
		return
	}

	state := appUpdateStateFor(app)
	previous := app.GetAfterDrawFunc()
	app.SetAfterDrawFunc(func(screen tcell.Screen) {
		state.started.Store(true)
		if previous != nil {
			previous(screen)
		}
	})
}

// UntrackApplicationLifecycle removes lifecycle state for a tview application.
func UntrackApplicationLifecycle(app *tview.Application) {
	if app == nil {
		return
	}
	trackedApplications.Delete(app)
}

func queueUpdateDraw(app *tview.Application, update func()) {
	if update == nil {
		return
	}
	if app == nil {
		update()
		return
	}
	if !appUpdateStateFor(app).started.Load() {
		update()
		return
	}
	go app.QueueUpdateDraw(update)
}

func appUpdateStateFor(app *tview.Application) *appUpdateState {
	state, _ := trackedApplications.LoadOrStore(app, &appUpdateState{})
	return state.(*appUpdateState)
}
