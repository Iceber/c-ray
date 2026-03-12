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
	statusBar *tview.TextView

	containerID string
	processes   []*models.Process
	sortField   SortField

	// Auto-refresh
	refreshInterval time.Duration
	refreshStop     chan struct{}
	mu              sync.Mutex
}

var topColumns = []components.Column{
	{Title: "PID", Width: 8, Align: tview.AlignRight},
	{Title: "PPID", Width: 8, Align: tview.AlignRight},
	{Title: "STATE", Width: 6},
	{Title: "CPU%", Width: 8, Align: tview.AlignRight},
	{Title: "MEM%", Width: 8, Align: tview.AlignRight},
	{Title: "RSS", Width: 10, Align: tview.AlignRight},
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
		refreshStop:     make(chan struct{}),
	}

	v.table = components.NewTable(topColumns)
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

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
	v.processes = top.Processes
	v.mu.Unlock()

	v.render()
	return nil
}

// render updates the table with current process data
func (v *TopView) render() {
	v.mu.Lock()
	procs := make([]*models.Process, len(v.processes))
	copy(procs, v.processes)
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

			v.table.AddRow(
				fmt.Sprintf("%d", p.PID),
				fmt.Sprintf("%d", p.PPID),
				p.State,
				fmt.Sprintf("%.1f", p.CPUPercent),
				fmt.Sprintf("%.1f", p.MemoryPercent),
				formatBytes(int64(p.MemoryRSS)),
				formatBytes(int64(p.ReadBytes)),
				formatBytes(int64(p.WriteBytes)),
				cmd,
			)
		}

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
			totalI := procs[i].ReadBytes + procs[i].WriteBytes
			totalJ := procs[j].ReadBytes + procs[j].WriteBytes
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

// StartAutoRefresh starts periodic refresh
func (v *TopView) StartAutoRefresh() {
	go func() {
		ticker := time.NewTicker(v.refreshInterval)
		defer ticker.Stop()

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

// StopAutoRefresh stops the auto-refresh
func (v *TopView) StopAutoRefresh() {
	select {
	case v.refreshStop <- struct{}{}:
	default:
	}
}

// GetFocusPrimitive returns the focusable primitive (table)
func (v *TopView) GetFocusPrimitive() tview.Primitive {
	return v.table
}

// updateStatusBar renders the status bar
func (v *TopView) updateStatusBar() {
	v.mu.Lock()
	sf := v.sortField
	count := len(v.processes)
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
