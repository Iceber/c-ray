package views

import (
	"context"
	"fmt"
	"strings"

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
	{Title: "ALIASES", Width: 8, Align: tview.AlignRight},
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
	v.table.SetSelectionChangedFunc(func(row, _ int) {
		if row <= 0 {
			return
		}
		v.updateStatusBar()
	})
	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.table.AddRow(components.Muted("Loading images..."), "", "", "", "")

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
		v.images = nil
		queueUpdateDraw(v.app, func() {
			v.table.ClearData()
			v.table.AddRow(fmt.Sprintf("[%s]Failed to load images: %v[-]", components.ColorName(components.ColorFgError), err), "", "", "", "")
		})
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
		primaryName := "-"
		aliasText := ""
		if len(img.Names) > 0 {
			primaryName = img.Names[0]
			if len(img.Names) > 1 {
				aliasText = fmt.Sprintf("+%d", len(img.Names)-1)
			}
		}
		digest := img.Digest
		if len(digest) > 19 {
			digest = digest[:19]
		}
		v.table.AddRow(primaryName, aliasText, digest, formatSize(img.Size), img.CreatedAt.Format("2006-01-02 15:04:05"))
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
	return imageSelection{digest: img.Digest, name: primaryImageName(img)}
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
		if saved.digest == "" && saved.name != "" && imageHasName(img, saved.name) {
			v.table.Select(idx+1, 0)
			return
		}
	}
	v.table.Select(1, 0)
}

func primaryImageName(img *runtime.ImageInfo) string {
	if img == nil || len(img.Names) == 0 {
		return ""
	}
	return img.Names[0]
}

func imageHasName(img *runtime.ImageInfo, name string) bool {
	if img == nil || name == "" {
		return false
	}
	for _, candidate := range img.Names {
		if candidate == name {
			return true
		}
	}
	return false
}

func imageAliasSummary(img *runtime.ImageInfo) string {
	if img == nil || len(img.Names) == 0 {
		return ""
	}
	return strings.Join(img.Names, ", ")
}

func (v *ImageListView) updateStatusBar() {
	var totalSize int64
	for _, img := range v.images {
		totalSize += img.Size
	}
	parts := []string{
		components.KV("Images ", fmt.Sprintf("%d", len(v.images))),
		components.KV("Size ", formatSize(totalSize)),
	}
	if summary := imageAliasSummary(v.selectedImageInfo()); summary != "" {
		parts = append(parts, components.KV("Names ", summary))
	}
	parts = append(parts, components.KeyHint("r", "refresh"))
	v.statusBar.SetText(" " + strings.Join(parts, "  |  "))
}

func (v *ImageListView) selectedImageInfo() *runtime.ImageInfo {
	row, _ := v.table.GetSelection()
	dataRow := row - 1
	if dataRow < 0 || dataRow >= len(v.images) {
		return nil
	}
	return v.images[dataRow]
}
