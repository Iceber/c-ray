package views

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// --- Application lifecycle tracking ---

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

// --- Formatting helpers ---

func formatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

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

func formatRate(bytesPerSec float64) string {
	if bytesPerSec < 1 {
		return "0 B/s"
	}
	const (
		KB = 1024.0
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytesPerSec >= GB:
		return fmt.Sprintf("%.1f G/s", bytesPerSec/GB)
	case bytesPerSec >= MB:
		return fmt.Sprintf("%.1f M/s", bytesPerSec/MB)
	case bytesPerSec >= KB:
		return fmt.Sprintf("%.1f K/s", bytesPerSec/KB)
	default:
		return fmt.Sprintf("%.0f B/s", bytesPerSec)
	}
}

func formatSize(bytes int64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
	)
	switch {
	case bytes >= GB:
		return fmt.Sprintf("%.1f GB", float64(bytes)/float64(GB))
	case bytes >= MB:
		return fmt.Sprintf("%.1f MB", float64(bytes)/float64(MB))
	case bytes >= KB:
		return fmt.Sprintf("%.1f KB", float64(bytes)/float64(KB))
	default:
		return fmt.Sprintf("%d B", bytes)
	}
}

func formatSummaryTime(ts time.Time) string {
	if ts.IsZero() {
		return "unknown"
	}
	return ts.Format("2006-01-02 15:04:05")
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

func fallbackValue(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func getColorName(c tcell.Color) string {
	switch c {
	case tcell.ColorGreen:
		return "green"
	case tcell.ColorRed:
		return "red"
	case tcell.ColorYellow:
		return "yellow"
	case tcell.ColorAqua:
		return "aqua"
	case tcell.ColorWhite:
		return "white"
	case tcell.ColorGray:
		return "gray"
	default:
		return "white"
	}
}
