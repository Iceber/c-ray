package views

import (
	"context"
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// PodListView displays a list of pods.
type PodListView struct {
	*tview.Flex

	app       *tview.Application
	rt        runtime.Runtime
	table     *components.Table
	statusBar *tview.TextView

	pods []podEntry
}

type podEntry struct {
	info       *runtime.PodInfo
	containers []runtime.Container
}

type podSelection struct {
	uid string
}

var podColumns = []components.Column{
	{Title: "NAME", Width: 0},
	{Title: "NAMESPACE", Width: 16},
	{Title: "UID", Width: 38},
	{Title: "CONTAINERS", Width: 12, Align: tview.AlignRight},
}

// NewPodListView creates a new pod list view.
func NewPodListView(app *tview.Application, rt runtime.Runtime) *PodListView {
	v := &PodListView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
	}
	v.table = components.NewTable(podColumns)
	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.table.AddRow("[gray]Loading pods...[-]", "", "", "")

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	return v
}

// GetFocusPrimitive returns the inner table that should receive keyboard focus.
func (v *PodListView) GetFocusPrimitive() tview.Primitive {
	return v.table
}

// Refresh loads and displays pod data.
func (v *PodListView) Refresh(ctx context.Context) error {
	savedSelection := v.getSelection()

	pods, err := v.rt.ListPods(ctx)
	if err != nil {
		return err
	}

	entries := make([]podEntry, 0, len(pods))
	for _, p := range pods {
		info, err := p.Info(ctx)
		if err != nil {
			continue
		}
		containers, _ := p.Containers(ctx)
		entries = append(entries, podEntry{info: info, containers: containers})
	}

	v.pods = entries
	queueUpdateDraw(v.app, func() {
		v.render()
		v.restoreSelection(savedSelection)
	})
	return nil
}

func (v *PodListView) render() {
	v.table.ClearData()
	for _, pe := range v.pods {
		running := 0
		for _, c := range pe.containers {
			info, err := c.Info(context.Background())
			if err == nil && info.Status == runtime.ContainerStatusRunning {
				running++
			}
		}
		total := len(pe.containers)
		containers := fmt.Sprintf("%d/%d", running, total)

		uid := pe.info.UID
		if len(uid) > 36 {
			uid = uid[:36]
		}

		v.table.AddRow(pe.info.Name, pe.info.Namespace, uid, containers)

		row := v.table.DataRowCount() - 1
		switch {
		case running == total && total > 0:
			v.table.SetRowColor(row, tcell.ColorGreen)
		case running > 0:
			v.table.SetRowColor(row, tcell.ColorYellow)
		default:
			v.table.SetRowColor(row, tcell.ColorRed)
		}
	}
	v.updateStatusBar()
}

func (v *PodListView) getSelection() podSelection {
	row, _ := v.table.GetSelection()
	dataRow := row - 1
	if dataRow < 0 || dataRow >= len(v.pods) {
		return podSelection{}
	}
	return podSelection{uid: v.pods[dataRow].info.UID}
}

func (v *PodListView) restoreSelection(saved podSelection) {
	if v.table.DataRowCount() == 0 {
		return
	}
	for idx, pe := range v.pods {
		if saved.uid != "" && pe.info.UID == saved.uid {
			v.table.Select(idx+1, 0)
			return
		}
	}
	v.table.Select(1, 0)
}

func (v *PodListView) updateStatusBar() {
	totalContainers := 0
	for _, pe := range v.pods {
		totalContainers += len(pe.containers)
	}
	v.statusBar.SetText(fmt.Sprintf(
		" [white]Pods: [green]%d[white] total, %d containers  |  [yellow]r[white]:refresh",
		len(v.pods), totalContainers,
	))
}
