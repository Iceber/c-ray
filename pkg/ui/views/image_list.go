package views

import (
	"context"
	"fmt"

	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// ImageListView displays a list of images.
type ImageListView struct {
	*tview.Flex

	app       *tview.Application
	rt        runtime.Runtime
	table     *components.Table
	statusBar *tview.TextView
	images    []*runtime.ImageInfo
}

type imageSelection struct {
	digest string
	name   string
}

var imageColumns = []components.Column{
	{Title: "NAME", Width: 0},
	{Title: "DIGEST", Width: 20},
	{Title: "SIZE", Width: 12, Align: tview.AlignRight},
	{Title: "CREATED", Width: 20},
}

// NewImageListView creates a new image list view.
func NewImageListView(app *tview.Application, rt runtime.Runtime) *ImageListView {
	v := &ImageListView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		app:  app,
		rt:   rt,
	}
	v.table = components.NewTable(imageColumns)
	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.table.AddRow("[gray]Loading images...[-]", "", "", "")

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	return v
}

// GetFocusPrimitive returns the inner table that should receive keyboard focus.
func (v *ImageListView) GetFocusPrimitive() tview.Primitive {
	return v.table
}

// Refresh loads and displays image data.
func (v *ImageListView) Refresh(ctx context.Context) error {
	savedSelection := v.getSelection()

	images, err := v.rt.ListImages(ctx)
	if err != nil {
		return err
	}

	infos := make([]*runtime.ImageInfo, 0, len(images))
	for _, img := range images {
		info, err := img.Info(ctx)
		if err != nil {
			continue
		}
		infos = append(infos, info)
	}

	v.images = infos
	queueUpdateDraw(v.app, func() {
		v.render()
		v.restoreSelection(savedSelection)
	})
	return nil
}

func (v *ImageListView) render() {
	v.table.ClearData()
	for _, img := range v.images {
		digest := img.Digest
		if len(digest) > 19 {
			digest = digest[:19]
		}
		v.table.AddRow(img.Name, digest, formatSize(img.Size), img.CreatedAt.Format("2006-01-02 15:04:05"))
	}
	v.updateStatusBar()
}

func (v *ImageListView) getSelection() imageSelection {
	row, _ := v.table.GetSelection()
	dataRow := row - 1
	if dataRow < 0 || dataRow >= len(v.images) {
		return imageSelection{}
	}
	img := v.images[dataRow]
	return imageSelection{digest: img.Digest, name: img.Name}
}

func (v *ImageListView) restoreSelection(saved imageSelection) {
	if v.table.DataRowCount() == 0 {
		return
	}
	for idx, img := range v.images {
		if saved.digest != "" && img.Digest == saved.digest {
			v.table.Select(idx+1, 0)
			return
		}
		if saved.digest == "" && saved.name != "" && img.Name == saved.name {
			v.table.Select(idx+1, 0)
			return
		}
	}
	v.table.Select(1, 0)
}

func (v *ImageListView) updateStatusBar() {
	var totalSize int64
	for _, img := range v.images {
		totalSize += img.Size
	}
	v.statusBar.SetText(fmt.Sprintf(
		" [white]Images: [green]%d[white] total, %s total size  |  [yellow]r[white]:refresh",
		len(v.images), formatSize(totalSize),
	))
}
