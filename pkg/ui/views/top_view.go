package views

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// SortField represents the field to sort by in top view.
type SortField int

const (
	SortByCPU SortField = iota
	SortByMem
	SortByPID
	SortByIO
)

// TopView displays top-like process information for a container.
type TopView struct {
	*tview.Flex

	app *tview.Application
	ctx context.Context

	table     *components.Table
	netBar    *tview.TextView
	statusBar *tview.TextView

	container runtime.Container
	flatProcs []*runtime.ProcessStats
	networkIO []*runtime.NetworkStats
	sortField SortField
	procCount int

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

// NewTopView creates a new top view.
func NewTopView(app *tview.Application, ctx context.Context) *TopView {
	v := &TopView{
		Flex:            tview.NewFlex().SetDirection(tview.FlexRow),
		app:             app,
		ctx:             ctx,
		sortField:       SortByCPU,
		refreshInterval: 2 * time.Second,
	}

	v.netBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.netBar.SetBackgroundColor(tcell.ColorDarkSlateGray)
	v.netBar.SetText(" [gray]Network: waiting for data...[-]")

	v.table = components.NewTable(topColumns)
	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.netBar, 1, 0, false)
	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	v.updateStatusBar()
	return v
}

// SetContainer sets the container handle to monitor.
func (v *TopView) SetContainer(c runtime.Container) {
	v.mu.Lock()
	v.container = c
	v.flatProcs = nil
	v.networkIO = nil
	v.procCount = 0
	v.mu.Unlock()
}

// Refresh loads and displays process top data.
func (v *TopView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	c := v.container
	v.mu.Unlock()

	if c == nil {
		return nil
	}

	stats, err := c.ProcessStats(ctx)
	if err != nil {
		return err
	}

	// Flatten the stats tree.
	var flat []*runtime.ProcessStats
	if stats != nil {
		flat = flattenProcessStats(stats)
	}

	// Get network data for the bar.
	var netIO []*runtime.NetworkStats
	if netState, err := c.Network(ctx); err == nil && netState != nil && netState.PodNetwork != nil {
		netIO = netState.PodNetwork.ObservedInterfaces
	}

	v.mu.Lock()
	v.flatProcs = flat
	v.networkIO = netIO
	v.procCount = len(flat)
	v.mu.Unlock()

	v.render()
	return nil
}

// flattenProcessStats recursively collects all ProcessStats into a flat slice.
func flattenProcessStats(root *runtime.ProcessStats) []*runtime.ProcessStats {
	var result []*runtime.ProcessStats
	result = append(result, root)
	for _, child := range root.Children {
		result = append(result, flattenProcessStats(child)...)
	}
	return result
}

func (v *TopView) render() {
	v.mu.Lock()
	procs := make([]*runtime.ProcessStats, len(v.flatProcs))
	copy(procs, v.flatProcs)
	netIO := v.networkIO
	sf := v.sortField
	v.mu.Unlock()

	sortProcessStats(procs, sf)

	queueUpdateDraw(v.app, func() {
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
				formatRate(p.ReadBytesPerSec),
				formatRate(p.WriteBytesPerSec),
				formatBytes(int64(p.ReadBytes)),
				formatBytes(int64(p.WriteBytes)),
				cmd,
			)
		}
		v.updateNetBar(netIO)
		v.updateStatusBar()
	})
}

func sortProcessStats(procs []*runtime.ProcessStats, field SortField) {
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

// HandleInput processes key events for the top view.
func (v *TopView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
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

func (v *TopView) setSortField(field SortField) {
	v.mu.Lock()
	v.sortField = field
	v.mu.Unlock()
	v.render()
}

// StartAutoRefresh starts periodic refresh.
func (v *TopView) StartAutoRefresh() {
	v.mu.Lock()
	defer v.mu.Unlock()

	if v.refreshRunning {
		return
	}
	v.refreshRunning = true
	v.refreshStop = make(chan struct{})

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
	if v.refreshRunning && v.refreshStop != nil {
		close(v.refreshStop)
		v.refreshRunning = false
		v.refreshStop = nil
	}
	v.mu.Unlock()
}

// GetFocusPrimitive returns the focusable primitive.
func (v *TopView) GetFocusPrimitive() tview.Primitive {
	return v.table
}

func (v *TopView) updateNetBar(netIO []*runtime.NetworkStats) {
	var parts []string
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

func (v *TopView) updateStatusBar() {
	v.mu.Lock()
	sf := v.sortField
	count := v.procCount
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
		" [white]Top: [green]%d[white]  |  [aqua]hotspot sort[-]: %s  |  [yellow]c[white]:cpu [yellow]m[white]:mem [yellow]p[white]:pid [yellow]i[white]:io",
		count, sortLabel,
	))
}
