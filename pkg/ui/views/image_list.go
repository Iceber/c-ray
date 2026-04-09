package views

import (
	"context"
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

// imageListStyle controls how the image list is rendered.
type imageListStyle int

const (
	// imageListStyleFlat: one row per Name, repeating digest/size/created for same digest.
	imageListStyleFlat imageListStyle = iota
	// imageListStyleTree: one digest row with collapsible Names beneath.
	imageListStyleTree
)

// ImageListView displays a list of images.
type ImageListView struct {
	*tview.Flex

	app             *tview.Application
	rt              runtime.Runtime
	table           *components.Table
	statusBar       *tview.TextView
	entries         []imageEntry
	onSelect        func(runtime.Image)
	rowMap          []int  // data-row (0-based) → entries index
	entryPrimaryRow []int  // entries index → primary table row (1-based)
	style           imageListStyle
	expanded        []bool // per-entry expanded state (tree style only)
}

type imageEntry struct {
	handle runtime.Image
	info   *runtime.ImageInfo
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
		Flex:  tview.NewFlex().SetDirection(tview.FlexRow),
		app:   app,
		rt:    rt,
		style: imageListStyleFlat,
	}
	v.table = components.NewTable(imageColumns)
	v.table.SetSelectedFunc(func(row int) {
		e := v.entryForDataRow(row)
		if e == nil {
			return
		}
		if v.onSelect != nil {
			v.onSelect(e.handle)
		}
	})
	v.table.SetSelectionChangedFunc(func(row, _ int) {
		if row <= 0 {
			return
		}
		v.updateStatusBar()
	})
	v.table.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Rune() {
		case 'v', 'V':
			v.toggleStyle()
			return nil
		case 'e':
			if v.style == imageListStyleTree {
				v.toggleExpand()
				return nil
			}
		case 'a':
			if v.style == imageListStyleTree {
				v.toggleExpandAll()
				return nil
			}
		}
		return event
	})
	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.table.AddRow(components.Muted("Loading images..."), "", "", "")

	v.Flex.AddItem(v.table, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	return v
}

// SetSelectedFunc sets the callback for image selection.
func (v *ImageListView) SetSelectedFunc(handler func(img runtime.Image)) {
	v.onSelect = handler
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
		v.entries = nil
		queueUpdateDraw(v.app, func() {
			v.table.ClearData()
			v.table.AddRow(fmt.Sprintf("[%s]Failed to load images: %v[-]", components.ColorName(components.ColorFgError), err), "", "", "")
		})
		return err
	}

	entries := make([]imageEntry, 0, len(images))
	for _, img := range images {
		info, err := img.Info(ctx)
		if err != nil {
			continue
		}
		entries = append(entries, imageEntry{handle: img, info: info})
	}

	v.entries = entries
	// Preserve expanded state per digest across refreshes.
	oldExpanded := v.expanded
	v.expanded = make([]bool, len(entries))
	if oldExpanded != nil {
		for i := range entries {
			if i < len(oldExpanded) {
				v.expanded[i] = oldExpanded[i]
			}
		}
	}
	queueUpdateDraw(v.app, func() {
		v.render()
		v.restoreSelection(savedSelection)
	})
	return nil
}

// ── render dispatches to the active style ──────────────────────────────────

func (v *ImageListView) render() {
	switch v.style {
	case imageListStyleFlat:
		v.renderFlat()
	case imageListStyleTree:
		v.renderTree()
	}
	v.updateStatusBar()
}

func (v *ImageListView) renderFlat() {
	v.table.ClearData()
	v.rowMap = v.rowMap[:0]
	v.entryPrimaryRow = make([]int, len(v.entries))
	tableRow := 1
	for idx, e := range v.entries {
		img := e.info
		digest := shortDigest(img.Digest, 19)
		size := formatSize(img.Size)
		created := img.CreatedAt.Format("2006-01-02 15:04:05")

		v.entryPrimaryRow[idx] = tableRow
		if len(img.Names) == 0 {
			v.rowMap = append(v.rowMap, idx)
			v.table.AddRow("-", digest, size, created)
			tableRow++
		} else {
			for i, name := range img.Names {
				v.rowMap = append(v.rowMap, idx)
				if i == 0 {
					v.table.AddRow(name, digest, size, created)
				} else {
					v.table.AddRow(name, digest, size, created)
				}
				tableRow++
			}
		}
	}
}

func (v *ImageListView) renderTree() {
	v.table.ClearData()
	v.rowMap = v.rowMap[:0]
	v.entryPrimaryRow = make([]int, len(v.entries))
	tableRow := 1
	for idx, e := range v.entries {
		img := e.info
		digest := shortDigest(img.Digest, 19)
		size := formatSize(img.Size)
		created := img.CreatedAt.Format("2006-01-02 15:04:05")
		nameCount := len(img.Names)

		// Digest row with expand/collapse indicator.
		var indicator string
		if nameCount > 1 {
			if v.expanded[idx] {
				indicator = "▼ "
			} else {
				indicator = "▶ "
			}
		} else {
			indicator = "  "
		}
		primaryName := "-"
		if nameCount > 0 {
			primaryName = img.Names[0]
		}
		label := indicator + primaryName
		if nameCount > 1 {
			label += components.Muted(fmt.Sprintf(" (+%d)", nameCount-1))
		}

		v.entryPrimaryRow[idx] = tableRow
		v.rowMap = append(v.rowMap, idx)
		v.table.AddRow(label, digest, size, created)
		tableRow++

		// Child name rows when expanded.
		if nameCount > 1 && v.expanded[idx] {
			for i := 1; i < nameCount; i++ {
				v.rowMap = append(v.rowMap, idx)
				v.table.AddRow(components.Muted("    "+img.Names[i]), "", "", "")
				tableRow++
			}
		}
	}
}

// ── toggle helpers ──────────────────────────────────────────────────────────

func (v *ImageListView) toggleStyle() {
	saved := v.getSelection()
	if v.style == imageListStyleFlat {
		v.style = imageListStyleTree
	} else {
		v.style = imageListStyleFlat
	}
	v.render()
	v.restoreSelection(saved)
}

func (v *ImageListView) toggleExpand() {
	row, _ := v.table.GetSelection()
	e := v.entryForTableRow(row)
	if e == nil {
		return
	}
	idx := v.rowMap[row-1]
	if len(e.info.Names) <= 1 {
		return
	}
	saved := v.getSelection()
	v.expanded[idx] = !v.expanded[idx]
	v.render()
	v.restoreSelection(saved)
}

func (v *ImageListView) toggleExpandAll() {
	// If any entry is collapsed, expand all; otherwise collapse all.
	anyCollapsed := false
	for idx, e := range v.entries {
		if len(e.info.Names) > 1 && !v.expanded[idx] {
			anyCollapsed = true
			break
		}
	}
	saved := v.getSelection()
	for idx, e := range v.entries {
		if len(e.info.Names) > 1 {
			v.expanded[idx] = anyCollapsed
		}
	}
	v.render()
	v.restoreSelection(saved)
}

// ── row mapping helpers ─────────────────────────────────────────────────────

func (v *ImageListView) entryForDataRow(dataRow int) *imageEntry {
	if dataRow < 0 || dataRow >= len(v.rowMap) {
		return nil
	}
	idx := v.rowMap[dataRow]
	if idx < 0 || idx >= len(v.entries) {
		return nil
	}
	return &v.entries[idx]
}

func (v *ImageListView) entryForTableRow(tableRow int) *imageEntry {
	return v.entryForDataRow(tableRow - 1)
}

// ── selection helpers ───────────────────────────────────────────────────────

func (v *ImageListView) getSelection() imageSelection {
	row, _ := v.table.GetSelection()
	e := v.entryForTableRow(row)
	if e == nil {
		return imageSelection{}
	}
	return imageSelection{digest: e.info.Digest, name: primaryImageName(e.info)}
}

func (v *ImageListView) restoreSelection(saved imageSelection) {
	if v.table.DataRowCount() == 0 {
		return
	}
	for idx, e := range v.entries {
		img := e.info
		if saved.digest != "" && img.Digest == saved.digest {
			v.table.Select(v.entryPrimaryRow[idx], 0)
			return
		}
		if saved.digest == "" && saved.name != "" && imageHasName(img, saved.name) {
			v.table.Select(v.entryPrimaryRow[idx], 0)
			return
		}
	}
	if len(v.entryPrimaryRow) > 0 {
		v.table.Select(v.entryPrimaryRow[0], 0)
	}
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

// ── status bar ──────────────────────────────────────────────────────────────

func (v *ImageListView) updateStatusBar() {
	var totalSize int64
	for _, e := range v.entries {
		totalSize += e.info.Size
	}
	parts := []string{
		components.KV("Images ", fmt.Sprintf("%d", len(v.entries))),
		components.KV("Size ", formatSize(totalSize)),
	}
	if img := v.selectedImageInfo(); img != nil && img.Digest != "" {
		d := img.Digest
		if len(d) > 30 {
			d = d[:30]
		}
		parts = append(parts, components.KV("Digest ", d))
	}
	hints := []components.FooterHint{
		{Key: "v", Action: "view"},
		{Key: "r", Action: "refresh"},
	}
	if v.style == imageListStyleTree {
		hints = append(hints, components.FooterHint{Key: "e", Action: "toggle"}, components.FooterHint{Key: "a", Action: "expand/collapse"})
	}
	for _, h := range hints {
		parts = append(parts, components.KeyHint(h.Key, h.Action))
	}
	v.statusBar.SetText(" " + strings.Join(parts, "  |  "))
}

func (v *ImageListView) selectedImageInfo() *runtime.ImageInfo {
	row, _ := v.table.GetSelection()
	e := v.entryForTableRow(row)
	if e == nil {
		return nil
	}
	return e.info
}

// ── utilities ───────────────────────────────────────────────────────────────

func shortDigest(digest string, maxLen int) string {
	if len(digest) > maxLen {
		return digest[:maxLen]
	}
	return digest
}
