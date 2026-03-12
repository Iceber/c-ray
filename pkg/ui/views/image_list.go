package views

import (
	"context"
	"fmt"

	"github.com/icebergu/c-ray/pkg/models"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// ImageListView displays a list of images
type ImageListView struct {
	*tview.Flex

	table     *components.Table
	statusBar *tview.TextView
	rt        runtime.Runtime
	images    []*models.Image
}

var imageColumns = []components.Column{
	{Title: "NAME", Width: 0},
	{Title: "DIGEST", Width: 20},
	{Title: "SIZE", Width: 12, Align: tview.AlignRight},
	{Title: "CREATED", Width: 20},
}

// NewImageListView creates a new image list view
func NewImageListView(rt runtime.Runtime) *ImageListView {
	v := &ImageListView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
		rt:   rt,
	}

	v.table = components.NewTable(imageColumns)
	v.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignLeft)

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)

	return v
}

// Refresh loads and displays image data
func (v *ImageListView) Refresh(ctx context.Context) error {
	images, err := v.rt.ListImages(ctx)
	if err != nil {
		return err
	}

	v.images = images
	v.render()
	return nil
}

// render updates the table with current image data
func (v *ImageListView) render() {
	v.table.ClearData()

	for _, img := range v.images {
		digest := img.Digest
		if len(digest) > 19 {
			digest = digest[:19]
		}

		size := formatSize(img.Size)
		created := img.CreatedAt.Format("2006-01-02 15:04:05")

		v.table.AddRow(img.Name, digest, size, created)
	}

	v.updateStatusBar()
}

// updateStatusBar updates the status bar text
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

// GetTable returns the underlying table component
func (v *ImageListView) GetTable() *components.Table {
	return v.table
}

// formatSize formats bytes into a human-readable size string
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
