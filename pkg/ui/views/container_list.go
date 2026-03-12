package views

import (
	"context"
	"fmt"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// ContainerListView displays a list of containers
type ContainerListView struct {
	*tview.Flex

	table      *components.Table
	statusBar  *tview.TextView
	rt         runtime.Runtime
	containers []*models.Container

	// Extended columns toggle
	showExtended bool

	// Callback when a container is selected
	onSelect func(containerID string)
}

var containerBasicColumns = []components.Column{
	{Title: "ID", Width: 14},
	{Title: "NAME", Width: 0},
	{Title: "IMAGE", Width: 0},
	{Title: "STATUS", Width: 10},
	{Title: "PID", Width: 8, Align: tview.AlignRight},
	{Title: "AGE", Width: 12},
}

var containerExtendedColumns = []components.Column{
	{Title: "ID", Width: 14},
	{Title: "NAME", Width: 0},
	{Title: "IMAGE", Width: 0},
	{Title: "STATUS", Width: 10},
	{Title: "PID", Width: 8, Align: tview.AlignRight},
	{Title: "AGE", Width: 12},
	{Title: "POD", Width: 0},
	{Title: "NAMESPACE", Width: 14},
}

// NewContainerListView creates a new container list view
func NewContainerListView(rt runtime.Runtime) *ContainerListView {
	v := &ContainerListView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		rt:   rt,
	}

	v.table = components.NewTable(containerBasicColumns)
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.table.SetSelectedFunc(func(row int) {
		if row >= 0 && row < len(v.containers) && v.onSelect != nil {
			v.onSelect(v.containers[row].ID)
		}
	})

	v.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'e', 'E':
			v.toggleExtended()
			return nil
		}
		return event
	})

	return v
}

// SetSelectedFunc sets the callback for container selection
func (v *ContainerListView) SetSelectedFunc(handler func(containerID string)) {
	v.onSelect = handler
}

// Refresh loads and displays container data
func (v *ContainerListView) Refresh(ctx context.Context) error {
	containers, err := v.rt.ListContainers(ctx)
	if err != nil {
		return err
	}

	v.containers = containers
	v.render()
	return nil
}

// render updates the table with current container data
func (v *ContainerListView) render() {
	v.table.ClearData()

	for _, c := range v.containers {
		age := formatAge(c.CreatedAt)
		status := string(c.Status)
		pid := fmt.Sprintf("%d", c.PID)
		if c.PID == 0 {
			pid = "-"
		}

		id := c.ID
		if len(id) > 12 {
			id = id[:12]
		}

		if v.showExtended {
			v.table.AddRow(id, c.Name, c.Image, status, pid, age, c.PodName, c.PodNamespace)
		} else {
			v.table.AddRow(id, c.Name, c.Image, status, pid, age)
		}

		// Color rows based on status
		row := v.table.DataRowCount() - 1
		switch c.Status {
		case models.ContainerStatusRunning:
			v.table.SetRowColor(row, tcell.ColorGreen)
		case models.ContainerStatusPaused:
			v.table.SetRowColor(row, tcell.ColorYellow)
		case models.ContainerStatusStopped:
			v.table.SetRowColor(row, tcell.ColorRed)
		case models.ContainerStatusCreated:
			v.table.SetRowColor(row, tcell.ColorDarkCyan)
		}
	}

	v.updateStatusBar()
}

// toggleExtended toggles the extended columns
func (v *ContainerListView) toggleExtended() {
	v.showExtended = !v.showExtended
	if v.showExtended {
		v.table.SetColumns(containerExtendedColumns)
	} else {
		v.table.SetColumns(containerBasicColumns)
	}
	v.render()
}

// updateStatusBar updates the status bar text
func (v *ContainerListView) updateStatusBar() {
	total := len(v.containers)
	running := 0
	for _, c := range v.containers {
		if c.Status == models.ContainerStatusRunning {
			running++
		}
	}

	extendedHint := "[yellow]e[white]:extended"
	if v.showExtended {
		extendedHint = "[yellow]e[white]:basic"
	}

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Containers: [green]%d[white] total, [green]%d[white] running  |  %s  [yellow]Enter[white]:detail  [yellow]r[white]:refresh",
		total, running, extendedHint,
	))
}

// GetTable returns the underlying table component
func (v *ContainerListView) GetTable() *components.Table {
	return v.table
}

// formatAge formats a time.Time into a human-readable age string
func formatAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := time.Since(t)

	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh%dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours() / 24)
	if days < 30 {
		return fmt.Sprintf("%dd", days)
	}
	return fmt.Sprintf("%dd", days)
}
