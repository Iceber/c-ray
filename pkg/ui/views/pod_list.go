package views

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// PodListView displays a list of pods
type PodListView struct {
	*tview.Flex

	app       *tview.Application
	table     *components.Table
	statusBar *tview.TextView
	rt        runtime.Runtime
	pods      []*models.Pod
}

var podColumns = []components.Column{
	{Title: "NAME", Width: 0},
	{Title: "NAMESPACE", Width: 16},
	{Title: "UID", Width: 38},
	{Title: "CONTAINERS", Width: 12, Align: tview.AlignRight},
}

// NewPodListView creates a new pod list view
func NewPodListView(app *tview.Application, rt runtime.Runtime) *PodListView {
	v := &PodListView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
	}

	v.table = components.NewTable(podColumns)
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	// Show loading indicator initially
	v.table.AddRow("[gray]Loading pods...[-]", "", "", "")

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	return v
}

// Refresh loads and displays pod data
func (v *PodListView) Refresh(ctx context.Context) error {
	pods, err := v.rt.ListPods(ctx)
	if err != nil {
		return err
	}

	v.pods = pods

	// UI updates must be done on the main thread
	if v.app != nil {
		queueUpdateDraw(v.app, func() {
			v.render()
		})
	} else {
		v.render()
	}
	return nil
}

// render updates the table with current pod data
func (v *PodListView) render() {
	v.table.ClearData()

	for _, pod := range v.pods {
		runningCount := 0
		for _, c := range pod.Containers {
			if c.Status == models.ContainerStatusRunning {
				runningCount++
			}
		}
		containers := fmt.Sprintf("%d/%d", runningCount, len(pod.Containers))

		uid := pod.UID
		if len(uid) > 36 {
			uid = uid[:36]
		}

		v.table.AddRow(pod.Name, pod.Namespace, uid, containers)

		// Color row based on container readiness
		row := v.table.DataRowCount() - 1
		if runningCount == len(pod.Containers) && len(pod.Containers) > 0 {
			v.table.SetRowColor(row, tcell.ColorGreen)
		} else if runningCount > 0 {
			v.table.SetRowColor(row, tcell.ColorYellow)
		} else {
			v.table.SetRowColor(row, tcell.ColorRed)
		}
	}

	v.updateStatusBar()
}

// updateStatusBar updates the status bar text
func (v *PodListView) updateStatusBar() {
	totalContainers := 0
	for _, pod := range v.pods {
		totalContainers += len(pod.Containers)
	}

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Pods: [green]%d[white] total, %d containers  |  [yellow]r[white]:refresh",
		len(v.pods), totalContainers,
	))
}

// GetTable returns the underlying table component
func (v *PodListView) GetTable() *components.Table {
	return v.table
}
