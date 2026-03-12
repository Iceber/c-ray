package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// SortField represents the field to sort by in top view
type SortField int

const (
	SortByCPU SortField = iota
	SortByMem
	SortByPID
	SortByIO
)

// TopView displays top-like process information for a container
type TopView struct {
	*tview.Flex

	app *tview.Application
	rt  runtime.Runtime
	ctx context.Context

	table     *components.Table
	netBar    *tview.TextView
	statusBar *tview.TextView

	containerID string
	topData     *models.ProcessTop
	sortField   SortField

	// Auto-refresh
	refreshInterval time.Duration
	refreshStop     chan struct{}
	refreshRunning  bool
	mu              sync.Mutex
}

var topColumns = []components.Column{
	{Title: "PID", Width: 8, Align: tview.AlignRight},
	{Title: "PPID", Width: 8, Align: tview.AlignRight},
	{Title: "STATE", Width: 6},
	{Title: "CPU%", Width: 8, Align: tview.AlignRight},
	{Title: "MEM%", Width: 8, Align: tview.AlignRight},
	{Title: "RSS", Width: 10, Align: tview.AlignRight},
	{Title: "R/s", Width: 10, Align: tview.AlignRight},
	{Title: "W/s", Width: 10, Align: tview.AlignRight},
	{Title: "READ", Width: 10, Align: tview.AlignRight},
	{Title: "WRITE", Width: 10, Align: tview.AlignRight},
	{Title: "COMMAND", Width: 0},
}

// NewTopView creates a new top view
func NewTopView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *TopView {
	v := &TopView{
		Flex:            tview.NewFlex().SetDirection(tview.FlexRow),
		app:             app,
		rt:              rt,
		ctx:             ctx,
		sortField:       SortByCPU,
		refreshInterval: 2 * time.Second,
	}

	v.netBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)
	v.netBar.SetBackgroundColor(tcell.ColorDarkSlateGray)

	v.table = components.NewTable(topColumns)
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.netBar, 1, 0, false)
	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.updateNetBar()
	v.updateStatusBar()
	return v
}

// SetContainer sets the container ID to monitor
func (v *TopView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.mu.Unlock()
}

// Refresh loads and displays process top data
func (v *TopView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	id := v.containerID
	v.mu.Unlock()

	if id == "" {
		return nil
	}

	top, err := v.rt.GetContainerTop(ctx, id)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.topData = top
	v.mu.Unlock()

	v.render()
	return nil
}

// render updates the table with current process data
func (v *TopView) render() {
	v.mu.Lock()
	if v.topData == nil {
		v.mu.Unlock()
		return
	}
	procs := make([]*models.Process, len(v.topData.Processes))
	copy(procs, v.topData.Processes)
	netIO := v.topData.NetworkIO
	cpuCores := v.topData.CPUCores
	memLimit := v.topData.MemoryLimit
	sf := v.sortField
	v.mu.Unlock()

	// Sort processes
	sortProcesses(procs, sf)

	v.app.QueueUpdateDraw(func() {
		v.table.ClearData()

		for _, p := range procs {
			cmd := p.Command
			if len(p.Args) > 0 {
				cmd = p.Command + " " + strings.Join(p.Args, " ")
			}
			if len(cmd) > 60 {
				cmd = cmd[:57] + "..."
			}

			// Format CPU% with limit context
			cpuStr := fmt.Sprintf("%.1f", p.CPUPercent)

			// Format MEM% with limit context
			memStr := fmt.Sprintf("%.1f", p.MemoryPercent)

			v.table.AddRow(
				fmt.Sprintf("%d", p.PID),
				fmt.Sprintf("%d", p.PPID),
				p.State,
				cpuStr,
				memStr,
				formatBytes(int64(p.MemoryRSS)),
				formatRate(p.ReadBytesPerSec),
				formatRate(p.WriteBytesPerSec),
				formatBytes(int64(p.ReadBytes)),
				formatBytes(int64(p.WriteBytes)),
				cmd,
			)
		}

		v.updateNetBarData(netIO, cpuCores, memLimit)
		v.updateStatusBar()
	})
}

// sortProcesses sorts processes by the given field
func sortProcesses(procs []*models.Process, field SortField) {
	sort.Slice(procs, func(i, j int) bool {
		switch field {
		case SortByCPU:
			return procs[i].CPUPercent > procs[j].CPUPercent
		case SortByMem:
			return procs[i].MemoryPercent > procs[j].MemoryPercent
		case SortByPID:
			return procs[i].PID < procs[j].PID
		case SortByIO:
			totalI := procs[i].ReadBytesPerSec + procs[i].WriteBytesPerSec
			totalJ := procs[j].ReadBytesPerSec + procs[j].WriteBytesPerSec
			return totalI > totalJ
		default:
			return procs[i].CPUPercent > procs[j].CPUPercent
		}
	})
}

// HandleInput processes key events for the top view
func (v *TopView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	// Always allow Ctrl+C to propagate for global quit
	if event.Key() == tcell.KeyCtrlC {
		return event
	}
	switch event.Rune() {
	case 'c', 'C':
		v.setSortField(SortByCPU)
		return nil
	case 'm', 'M':
		v.setSortField(SortByMem)
		return nil
	case 'p', 'P':
		v.setSortField(SortByPID)
		return nil
	case 'i', 'I':
		v.setSortField(SortByIO)
		return nil
	}
	return event
}

// setSortField changes the sort field and re-renders
func (v *TopView) setSortField(field SortField) {
	v.mu.Lock()
	v.sortField = field
	v.mu.Unlock()
	v.render()
}

// StartAutoRefresh starts periodic refresh. It is idempotent —
// calling it while a refresh loop is already running is a no-op.
func (v *TopView) StartAutoRefresh() {
	v.mu.Lock()
	if v.refreshRunning {
		v.mu.Unlock()
		return
	}
	v.refreshRunning = true
	v.refreshStop = make(chan struct{})
	v.mu.Unlock()

	go func() {
		ticker := time.NewTicker(v.refreshInterval)
		defer ticker.Stop()
		defer func() {
			v.mu.Lock()
			v.refreshRunning = false
			v.mu.Unlock()
		}()

		for {
			select {
			case <-ticker.C:
				v.Refresh(v.ctx)
			case <-v.refreshStop:
				return
			case <-v.ctx.Done():
				return
			}
		}
	}()
}

// StopAutoRefresh stops the auto-refresh loop.
func (v *TopView) StopAutoRefresh() {
	v.mu.Lock()
	if v.refreshRunning {
		close(v.refreshStop)
	}
	v.mu.Unlock()
}

// GetFocusPrimitive returns the focusable primitive (table)
func (v *TopView) GetFocusPrimitive() tview.Primitive {
	return v.table
}

// updateNetBar initializes the network bar with placeholder text
func (v *TopView) updateNetBar() {
	v.netBar.SetText(" [gray]Network: waiting for data...[-]")
}

// updateNetBarData renders the network bar with live data
func (v *TopView) updateNetBarData(netIO []*models.NetworkStats, cpuCores float64, memLimit int64) {
	var parts []string

	// Show limits
	if cpuCores > 0 {
		parts = append(parts, fmt.Sprintf("[gray]CPU Limit:[aqua]%.2f cores[-]", cpuCores))
	}
	if memLimit > 0 {
		parts = append(parts, fmt.Sprintf("[gray]Mem Limit:[aqua]%s[-]", formatBytes(memLimit)))
	}

	// Show per-interface network IO
	for _, ns := range netIO {
		parts = append(parts, fmt.Sprintf(
			"[gray]%s [green]↓[white]%s[gray](%s) [red]↑[white]%s[gray](%s)[-]",
			ns.Interface,
			formatRate(ns.RxBytesPerSec), formatBytes(int64(ns.RxBytes)),
			formatRate(ns.TxBytesPerSec), formatBytes(int64(ns.TxBytes)),
		))
	}

	if len(parts) == 0 {
		v.netBar.SetText(" [gray]Network: no active interfaces[-]")
		return
	}

	v.netBar.SetText(" " + strings.Join(parts, "  "))
}

// updateStatusBar renders the status bar
func (v *TopView) updateStatusBar() {
	v.mu.Lock()
	sf := v.sortField
	count := 0
	if v.topData != nil {
		count = len(v.topData.Processes)
	}
	v.mu.Unlock()

	sortLabel := "CPU"
	switch sf {
	case SortByMem:
		sortLabel = "MEM"
	case SortByPID:
		sortLabel = "PID"
	case SortByIO:
		sortLabel = "I/O"
	}

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Processes: [green]%d[white]  |  Sort: [aqua]%s[white]  |  [yellow]c[white]:cpu [yellow]m[white]:mem [yellow]p[white]:pid [yellow]i[white]:io",
		count, sortLabel,
	))
}

// formatRate formats a bytes/sec value as a human-readable rate string
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
