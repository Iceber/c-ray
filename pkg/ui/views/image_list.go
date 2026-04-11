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

	app             *tview.Application
	rt              runtime.Runtime
	table           *components.Table
	statusBar       *tview.TextView
	entries         []imageEntry
	onSelect        func(runtime.Image)
	rowMap          []int // data-row (0-based) → entries index
	entryPrimaryRow []int // entries index → primary table row (1-based)
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
	{Title: "IMAGE", Width: 0},
	{Title: "TAG", Width: 20},
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

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.table.AddRow(components.Muted("Loading images..."), "", "", "", "", "")

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
			v.table.AddRow(fmt.Sprintf("[%s]Failed to load images: %v[-]", components.ColorName(components.ColorFgError), err), "", "", "", "", "")
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
	queueUpdateDraw(v.app, func() {
		v.renderFlat()
		v.updateStatusBar()
		v.restoreSelection(savedSelection)
	})
	return nil
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
		visibleNames := imageVisibleNames(img)
		aliasCount := fmt.Sprintf("%d", len(visibleNames))
		if len(visibleNames) == 0 {
			v.rowMap = append(v.rowMap, idx)
			v.table.AddRow("-", "", "", digest, size, created)
			tableRow++
		} else {
			for _, name := range visibleNames {
				image, tag := splitImageName(name)
				v.rowMap = append(v.rowMap, idx)
				v.table.AddRow(image, tag, aliasCount, digest, size, created)
				tableRow++
			}
		}
	}
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
	if img == nil {
		return ""
	}
	if visible := imageVisibleNames(img); len(visible) > 0 {
		return visible[0]
	}
	if len(img.Names) > 0 {
		return img.Names[0]
	}
	return ""
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
	parts = append(parts, components.KeyHint("r", "refresh"))
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

// imageVisibleNames returns names that should be shown in the list:
// - excludes names starting with "sha256:"
// - excludes names of the form "<repo>@sha256:<hash>" where hash matches the image digest
func imageVisibleNames(img *runtime.ImageInfo) []string {
	// Extract the raw hash from the digest (e.g. "sha256:abc" → "abc").
	digestHash := img.Digest
	if after, ok := strings.CutPrefix(digestHash, "sha256:"); ok {
		digestHash = after
	}

	visible := make([]string, 0, len(img.Names))
	for _, name := range img.Names {
		if strings.HasPrefix(name, "sha256:") {
			continue
		}
		// Hide "<repo>@sha256:<hash>" when the hash matches this image's digest.
		if idx := strings.Index(name, "@sha256:"); idx >= 0 {
			if name[idx+len("@sha256:"):] == digestHash {
				continue
			}
		}
		visible = append(visible, name)
	}
	return visible
}

// splitImageName splits "repo:tag" into ("repo", "tag").
// If no tag is present, tag is returned as "<none>".
func splitImageName(name string) (string, string) {
	// Handle names with port like localhost:5000/repo:tag
	lastSlash := strings.LastIndex(name, "/")
	tagPart := name
	if lastSlash >= 0 {
		tagPart = name[lastSlash+1:]
	}
	if idx := strings.LastIndex(tagPart, ":"); idx >= 0 {
		splitAt := lastSlash + 1 + idx
		if lastSlash < 0 {
			splitAt = idx
		}
		return name[:splitAt], name[splitAt+1:]
	}
	return name, "<none>"
}
