package views

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// MountsView displays mount information for a container
type MountsView struct {
	*tview.Flex

	app *tview.Application
	rt  runtime.Runtime
	ctx context.Context

	table     *components.Table
	statusBar *tview.TextView

	containerID string
	mounts      []*models.Mount
	detail      *models.ContainerDetail
	mu          sync.Mutex
}

var mountColumns = []components.Column{
	{Title: "DESTINATION", Width: 0},
	{Title: "SOURCE", Width: 0},
	{Title: "TYPE", Width: 12},
	{Title: "OPTIONS", Width: 0},
}

// NewMountsView creates a new mounts view
func NewMountsView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *MountsView {
	v := &MountsView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
		ctx:  ctx,
	}

	v.table = components.NewTable(mountColumns)
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	v.updateStatusBar()
	return v
}

// SetContainer sets the container ID
func (v *MountsView) SetContainer(containerID string) {
	v.mu.Lock()
	v.containerID = containerID
	v.mu.Unlock()
}

// SetDetail sets the container detail (for image/layer info)
func (v *MountsView) SetDetail(detail *models.ContainerDetail) {
	v.mu.Lock()
	v.detail = detail
	v.mu.Unlock()
}

// Refresh loads and displays mount data
func (v *MountsView) Refresh(ctx context.Context) error {
	v.mu.Lock()
	id := v.containerID
	v.mu.Unlock()

	if id == "" {
		return nil
	}

	mounts, err := v.rt.GetContainerMounts(ctx, id)
	if err != nil {
		return err
	}

	v.mu.Lock()
	v.mounts = mounts
	v.mu.Unlock()

	v.render()
	return nil
}

// render updates the table with mount data
func (v *MountsView) render() {
	v.mu.Lock()
	mounts := make([]*models.Mount, len(v.mounts))
	copy(mounts, v.mounts)
	detail := v.detail
	v.mu.Unlock()

	v.app.QueueUpdateDraw(func() {
		v.table.ClearData()

		// Show image/layer info as header rows if detail is available
		if detail != nil {
			if detail.ImageName != "" {
				v.table.AddRow("(image)", detail.ImageName, "image", "")
				v.table.SetRowColor(v.table.DataRowCount()-1, tcell.ColorAqua)
			}
			if detail.WritableLayerPath != "" {
				v.table.AddRow("(rw-layer)", detail.WritableLayerPath, "overlay", "read-write")
				v.table.SetRowColor(v.table.DataRowCount()-1, tcell.ColorYellow)
			}
			if detail.ReadOnlyLayerPath != "" {
				v.table.AddRow("(ro-layer)", detail.ReadOnlyLayerPath, "overlay", "read-only")
				v.table.SetRowColor(v.table.DataRowCount()-1, tcell.ColorGray)
			}
		}

		// Show mounts
		for _, m := range mounts {
			opts := strings.Join(m.Options, ",")
			if len(opts) > 50 {
				opts = opts[:47] + "..."
			}
			v.table.AddRow(m.Destination, m.Source, m.Type, opts)

			// Color based on mount type
			row := v.table.DataRowCount() - 1
			switch {
			case m.Type == "overlay" || m.Type == "overlayfs":
				v.table.SetRowColor(row, tcell.ColorDarkCyan)
			case strings.Contains(m.Type, "tmpfs"):
				v.table.SetRowColor(row, tcell.ColorGray)
			case m.Type == "bind" || m.Source != "":
				v.table.SetRowColor(row, tcell.ColorGreen)
			}
		}

		v.updateStatusBar()
	})
}

// HandleInput processes key events
func (v *MountsView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	return event
}

// GetFocusPrimitive returns the focusable primitive (table)
func (v *MountsView) GetFocusPrimitive() tview.Primitive {
	return v.table
}

// updateStatusBar renders the status bar
func (v *MountsView) updateStatusBar() {
	v.mu.Lock()
	count := len(v.mounts)
	v.mu.Unlock()

	v.statusBar.SetText(fmt.Sprintf(
		" [white]Mounts: [green]%d[white]  |  [aqua]overlay[-] [green]bind[-] [gray]tmpfs[-]",
		count,
	))
}
