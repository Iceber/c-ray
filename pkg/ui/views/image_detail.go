package views

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/icebergu/c-ray/pkg/runtime"
	"github.com/icebergu/c-ray/pkg/ui/components"
	"github.com/rivo/tview"
)

const imageMissingPathMarker = "[red::b]!! MISSING PATH !![-:-:-]"

const imageFoldedValueWidth = 48

// ImageDetailTab is the active image-detail tab.
type ImageDetailTab int

const (
	ImageDetailTabInfo ImageDetailTab = iota
	ImageDetailTabPlatforms
	ImageDetailTabLayers
	ImageDetailTabUsedBy
)

type imageInfoDetailRef struct {
	Title string
	Full  string
}

type platformsFileRef struct {
	Title string
	Path  string
}

type imageLayerRef struct {
	layer *runtime.ImageLayer
}

type imageMergedNode struct {
	name           string
	relPath        string
	isDir          bool
	fromLayer      int // layer index that last added/modified this entry
	deletedByLayer int // -1 = visible; >=0 = deleted by this layer's whiteout
	loaded         bool
}

type imageUsedByEntry struct {
	ContainerName string
	ContainerID   string
	Pod           string
	Status        runtime.ContainerStatus
	CreatedAt     time.Time
	ImageRef      string
}

// ImageDetailView renders detail information for one image.
type ImageDetailView struct {
	*tview.Flex

	app *tview.Application
	rt  runtime.Runtime
	ctx context.Context

	image      runtime.Image
	info       *runtime.ImageInfo
	config     *runtime.ImageConfigInfo
	layers     []*runtime.ImageLayer
	usedBy     []imageUsedByEntry
	refreshed  time.Time
	headerBar  *tview.TextView
	contextBar *tview.TextView
	tabBar     *components.TabBar
	content    *tview.Pages
	footer     *components.Footer

	infoView              *tview.Flex
	infoSummary           *tview.TextView
	infoManifest          *tview.TreeView
	infoConfig            *tview.TreeView
	infoDetail            *tview.TextView
	platformsPage         *tview.Flex
	platformsBody         *tview.Flex
	platformsView         *tview.TreeView
	platformsPreview      *tview.TextView
	platformsPreviewOpen  bool
	platformsPreviewFocus int // 0=tree 1=preview
	platformsDetail       *tview.TextView

	layersPage           *tview.Flex
	layersBody           *tview.Flex
	layersDetail         *tview.TextView
	layersBrowserOpen    bool
	layersBrowserFocus   int // 0=tree 1=browser
	layersBrowserTree    *tview.TreeView
	layersBrowserInfo    *tview.TextView
	layersBrowserPreview *tview.TextView
	layersBrowser        *tview.Flex

	layersMergedOpen        bool
	layersMergedFocus       int // 0=tree 1=merged
	layersMergedShowDeleted bool
	layersMergedTree        *tview.TreeView
	layersMergedPreview     *tview.TextView
	layersMergedInfo        *tview.TextView
	layersMergedBrowser     *tview.Flex

	layersView  *tview.TreeView
	usedByTable *components.Table

	activeTab  ImageDetailTab
	onBack     func()
	refreshGen uint64
}

var imageDetailTabs = []components.TabDef{
	{Label: "Info", Key: "1"},
	{Label: "Platforms", Key: "2"},
	{Label: "Layers", Key: "3"},
	{Label: "Used By", Key: "4"},
}

var imageUsedByColumns = []components.Column{
	{Title: "CONTAINER", Width: 0},
	{Title: "POD", Width: 24},
	{Title: "STATUS", Width: 10},
	{Title: "CREATED", Width: 20},
}

// NewImageDetailView creates an image-detail view.
func NewImageDetailView(app *tview.Application, rt runtime.Runtime, ctx context.Context) *ImageDetailView {
	v := &ImageDetailView{
		Flex:      tview.NewFlex().SetDirection(tview.FlexRow),
		app:       app,
		rt:        rt,
		ctx:       ctx,
		activeTab: ImageDetailTabInfo,
	}
	v.setupLayout()
	return v
}

func (v *ImageDetailView) setupLayout() {
	v.headerBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.headerBar.SetBackgroundColor(components.ColorBgHeader)
	v.contextBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.contextBar.SetBackgroundColor(components.ColorBgHeader)
	v.tabBar = components.NewTabBar()
	v.footer = components.NewFooter()

	v.infoSummary = tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	v.infoSummary.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.infoSummary.SetTitle(fmt.Sprintf(" %s ", components.Accent("Summary"))).SetTitleAlign(tview.AlignLeft)

	v.infoManifest = tview.NewTreeView()
	components.InitTreeView(v.infoManifest)
	v.infoManifest.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.infoManifest.SetTitle(fmt.Sprintf(" %s ", components.Accent("Current Manifest"))).SetTitleAlign(tview.AlignLeft)
	v.infoManifest.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})
	v.infoManifest.SetChangedFunc(func(node *tview.TreeNode) {
		v.renderInfoDetail(node)
	})

	v.infoConfig = tview.NewTreeView()
	components.InitTreeView(v.infoConfig)
	v.infoConfig.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.infoConfig.SetTitle(fmt.Sprintf(" %s ", components.Accent("Current Config"))).SetTitleAlign(tview.AlignLeft)
	v.infoConfig.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})
	v.infoConfig.SetChangedFunc(func(node *tview.TreeNode) {
		v.renderInfoDetail(node)
	})

	v.infoDetail = tview.NewTextView().SetDynamicColors(true).SetWordWrap(true)
	v.infoDetail.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.infoDetail.SetTitle(fmt.Sprintf(" %s ", components.Accent("Detail (Full Value)"))).SetTitleAlign(tview.AlignLeft)
	v.infoDetail.SetText(" " + components.Muted("Select folded digest/path fields to see full values."))

	infoBody := tview.NewFlex().SetDirection(tview.FlexColumn)
	infoBody.AddItem(v.infoManifest, 0, 1, true)
	infoBody.AddItem(v.infoConfig, 0, 1, false)

	v.infoView = tview.NewFlex().SetDirection(tview.FlexRow)
	v.infoView.AddItem(v.infoSummary, 0, 2, false)
	v.infoView.AddItem(infoBody, 0, 7, true)
	v.infoView.AddItem(v.infoDetail, 0, 1, false)

	v.platformsView = tview.NewTreeView()
	components.InitTreeView(v.platformsView)
	v.platformsView.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.platformsView.SetTitle(fmt.Sprintf(" %s ", components.Accent("Platforms"))).SetTitleAlign(tview.AlignLeft)
	v.platformsView.SetChangedFunc(func(node *tview.TreeNode) {
		if v.platformsPreviewOpen {
			v.updatePlatformsPreview(node)
		}
		v.updatePlatformsDetail(node)
	})

	v.platformsPreview = tview.NewTextView().SetDynamicColors(true).SetWordWrap(false)
	v.platformsPreview.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.platformsPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("File Preview"))).SetTitleAlign(tview.AlignLeft)
	v.platformsPreview.SetText(" " + components.Muted("Select a Manifest or Config node to preview."))

	v.platformsDetail = tview.NewTextView().SetDynamicColors(true)
	v.platformsDetail.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.platformsDetail.SetTitle(fmt.Sprintf(" %s ", components.Accent("Detail"))).SetTitleAlign(tview.AlignLeft)
	v.platformsDetail.SetText(" " + components.Muted("Select a field to see full values."))

	v.platformsBody = tview.NewFlex().SetDirection(tview.FlexColumn)
	v.platformsBody.AddItem(v.platformsView, 0, 1, true)

	v.platformsPage = tview.NewFlex().SetDirection(tview.FlexRow)
	v.platformsPage.AddItem(v.platformsBody, 0, 9, true)
	v.platformsPage.AddItem(v.platformsDetail, 0, 1, false)

	v.layersView = tview.NewTreeView()
	components.InitTreeView(v.layersView)
	v.layersView.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersView.SetTitle(fmt.Sprintf(" %s ", components.Accent("Layers"))).SetTitleAlign(tview.AlignLeft)

	v.layersDetail = tview.NewTextView().SetDynamicColors(true)
	v.layersDetail.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersDetail.SetTitle(fmt.Sprintf(" %s ", components.Accent("Detail"))).SetTitleAlign(tview.AlignLeft)
	v.layersDetail.SetText(" " + components.Muted("Select a layer or field to see full values."))

	v.layersBrowserInfo = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.layersBrowserInfo.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersBrowserInfo.SetTitle(fmt.Sprintf(" %s ", components.Accent("Layer Browser"))).SetTitleAlign(tview.AlignLeft)
	v.layersBrowserInfo.SetText(" " + components.Muted("Select a layer node and press i to browse."))

	v.layersBrowserTree = tview.NewTreeView()
	components.InitTreeView(v.layersBrowserTree)
	v.layersBrowserTree.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersBrowserTree.SetTitle(fmt.Sprintf(" %s ", components.Accent("Files"))).SetTitleAlign(tview.AlignLeft)
	v.layersBrowserTree.SetSelectedFunc(func(node *tview.TreeNode) { v.toggleLayersBrowserNode(node) })
	v.layersBrowserTree.SetChangedFunc(func(node *tview.TreeNode) { v.renderLayersBrowserPreview(node) })

	v.layersBrowserPreview = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.layersBrowserPreview.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersBrowserPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview"))).SetTitleAlign(tview.AlignLeft)
	v.layersBrowserPreview.SetText(" " + components.Muted("No file selected."))

	v.layersBrowser = tview.NewFlex().SetDirection(tview.FlexRow)
	v.layersBrowser.AddItem(v.layersBrowserInfo, 3, 0, false)
	v.layersBrowser.AddItem(v.layersBrowserTree, 0, 1, true)
	v.layersBrowser.AddItem(v.layersBrowserPreview, 0, 1, false)

	// --- Merged overlay browser ---
	v.layersMergedInfo = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.layersMergedInfo.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersMergedInfo.SetTitle(fmt.Sprintf(" %s ", components.Accent("Merged View"))).SetTitleAlign(tview.AlignLeft)
	v.layersMergedInfo.SetText(" " + components.Muted("Press i to open merged filesystem view."))

	v.layersMergedTree = tview.NewTreeView()
	components.InitTreeView(v.layersMergedTree)
	v.layersMergedTree.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersMergedTree.SetTitle(fmt.Sprintf(" %s ", components.Accent("Merged Filesystem"))).SetTitleAlign(tview.AlignLeft)
	v.layersMergedTree.SetSelectedFunc(func(node *tview.TreeNode) { v.toggleMergedNode(node) })
	v.layersMergedTree.SetChangedFunc(func(node *tview.TreeNode) { v.renderMergedPreview(node) })

	v.layersMergedPreview = tview.NewTextView().SetDynamicColors(true).SetWrap(false)
	v.layersMergedPreview.SetBorder(true).SetBorderColor(components.ColorFgBorder)
	v.layersMergedPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview"))).SetTitleAlign(tview.AlignLeft)
	v.layersMergedPreview.SetText(" " + components.Muted("No file selected."))

	v.layersMergedBrowser = tview.NewFlex().SetDirection(tview.FlexRow)
	v.layersMergedBrowser.AddItem(v.layersMergedInfo, 3, 0, false)
	v.layersMergedBrowser.AddItem(v.layersMergedTree, 0, 1, true)
	v.layersMergedBrowser.AddItem(v.layersMergedPreview, 0, 1, false)

	v.layersBody = tview.NewFlex().SetDirection(tview.FlexColumn)
	v.layersBody.AddItem(v.layersView, 0, 1, true)

	v.layersPage = tview.NewFlex().SetDirection(tview.FlexRow)
	v.layersPage.AddItem(v.layersBody, 0, 1, true)
	v.layersPage.AddItem(v.layersDetail, 4, 0, false)

	v.usedByTable = components.NewTable(imageUsedByColumns)

	v.platformsView.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})
	v.platformsPreview.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if event.Key() == tcell.KeyTab || event.Key() == tcell.KeyBacktab {
			v.platformsPreviewFocus = 0
			v.app.SetFocus(v.platformsView)
			v.updatePlatformsFocusStyles()
			return nil
		}
		return event
	})
	v.layersView.SetSelectedFunc(func(node *tview.TreeNode) {
		if node != nil {
			node.SetExpanded(!node.IsExpanded())
		}
	})
	v.layersView.SetChangedFunc(func(node *tview.TreeNode) {
		v.updateLayersDetail(node)
		if v.layersMergedOpen {
			v.updateMergedHighlights()
		}
	})

	v.content = tview.NewPages()
	v.content.AddPage("info", v.infoView, true, true)
	v.content.AddPage("platforms", v.platformsPage, true, false)
	v.content.AddPage("layers", v.layersPage, true, false)
	v.content.AddPage("used-by", v.usedByTable, true, false)

	v.Flex.AddItem(v.headerBar, 1, 0, false)
	v.Flex.AddItem(v.contextBar, 1, 0, false)
	v.Flex.AddItem(v.tabBar, 1, 0, false)
	v.Flex.AddItem(v.content, 0, 1, true)
	v.Flex.AddItem(v.footer, 1, 0, false)

	v.renderHeader()
	v.updateTabBar()
	v.updateFooter()
}

// SetImage updates the selected image and starts loading details.
func (v *ImageDetailView) SetImage(img runtime.Image) {
	atomic.AddUint64(&v.refreshGen, 1)
	v.image = img
	v.info = nil
	v.config = nil
	v.layers = nil
	v.usedBy = nil
	v.refreshed = time.Time{}

	queueUpdateDraw(v.app, func() {
		v.renderHeader()
		v.renderInfo()
		v.renderPlatforms()
		v.renderLayers()
		v.renderUsedBy()
	})

	go v.Refresh()
}

// SetBackFunc sets the callback used when leaving detail view.
func (v *ImageDetailView) SetBackFunc(handler func()) {
	v.onBack = handler
}

// GetFocusPrimitive returns the focus primitive for active tab.
func (v *ImageDetailView) GetFocusPrimitive() tview.Primitive {
	switch v.activeTab {
	case ImageDetailTabInfo:
		return v.infoManifest
	case ImageDetailTabPlatforms:
		if v.platformsPreviewOpen && v.platformsPreviewFocus == 1 {
			return v.platformsPreview
		}
		return v.platformsView
	case ImageDetailTabLayers:
		if v.layersMergedOpen && v.layersMergedFocus == 1 {
			return v.layersMergedTree
		}
		if v.layersBrowserOpen && v.layersBrowserFocus == 1 {
			return v.layersBrowserTree
		}
		return v.layersView
	case ImageDetailTabUsedBy:
		return v.usedByTable
	default:
		return v.infoManifest
	}
}

// Refresh loads image fields, layers, parsed path data, and used-by containers.
func (v *ImageDetailView) Refresh() {
	if v.image == nil {
		return
	}
	gen := atomic.LoadUint64(&v.refreshGen)

	info, err := v.image.Info(v.ctx)
	if err != nil {
		if atomic.LoadUint64(&v.refreshGen) != gen {
			return
		}
		queueUpdateDraw(v.app, func() {
			v.headerBar.SetText(fmt.Sprintf(" [%s]Failed to load image: %v[-]", components.ColorName(components.ColorFgError), err))
			v.contextBar.SetText(" ")
		})
		return
	}
	config, _ := v.image.Config(v.ctx)
	layers, _ := v.image.Layers(v.ctx, runtime.LayerQuery{})
	usedBy, _ := v.resolveUsedBy(v.ctx, info)

	if atomic.LoadUint64(&v.refreshGen) != gen {
		return
	}

	v.info = info
	v.config = config
	v.layers = layers
	v.usedBy = usedBy
	v.refreshed = time.Now()

	queueUpdateDraw(v.app, func() {
		v.renderHeader()
		v.refreshActiveTab()
	})
}

func (v *ImageDetailView) refreshActiveTab() {
	switch v.activeTab {
	case ImageDetailTabInfo:
		v.renderInfo()
	case ImageDetailTabPlatforms:
		v.renderPlatforms()
	case ImageDetailTabLayers:
		v.renderLayers()
	case ImageDetailTabUsedBy:
		v.renderUsedBy()
	}
}

func (v *ImageDetailView) renderHeader() {
	if v.info == nil {
		v.headerBar.SetText(fmt.Sprintf(" %s Loading image detail...", components.Muted("⏳")))
		v.contextBar.SetText(" ")
		return
	}

	idOrDigest := strings.TrimSpace(v.info.Digest)
	if idOrDigest == "" && v.image != nil {
		idOrDigest = strings.TrimSpace(v.image.Ref())
	}
	if idOrDigest == "" {
		idOrDigest = "-"
	}

	kind := imageKindSchemaSummary(v.config)
	if kind == "-" {
		kind = "unknown"
	}

	v.headerBar.SetText(fmt.Sprintf(
		" %s  |  %s  |  %s",
		components.KV("Digest", idOrDigest),
		components.KV("Size", formatBytes(v.info.Size)),
		components.KV("Created", detailTimeLabel(v.info.CreatedAt)),
	))

	ctx := []string{components.KV("Names", imageTopNamesLine(v.info))}
	if !v.refreshed.IsZero() {
		ctx = append(ctx, components.Dim("refreshed "+v.refreshed.Format("15:04:05")))
	}
	v.contextBar.SetText(" " + joinSpaced(ctx))
}

// HandleInput handles tab navigation and subview interaction.
func (v *ImageDetailView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	if event.Key() == tcell.KeyCtrlC || event.Rune() == 'Q' {
		return event
	}
	switch event.Key() {
	case tcell.KeyEscape:
		if v.onBack != nil {
			v.onBack()
		}
		return nil
	case tcell.KeyTab, tcell.KeyBacktab:
		// When the Platforms preview panel is open, Tab switches pane focus
		// instead of cycling tabs.
		if v.activeTab == ImageDetailTabPlatforms && v.platformsPreviewOpen {
			if v.platformsPreviewFocus == 0 {
				v.platformsPreviewFocus = 1
				v.app.SetFocus(v.platformsPreview)
			} else {
				v.platformsPreviewFocus = 0
				v.app.SetFocus(v.platformsView)
			}
			v.updatePlatformsFocusStyles()
			return nil
		}
		// When the Layers browser is open, Tab switches pane focus.
		if v.activeTab == ImageDetailTabLayers && (v.layersBrowserOpen || v.layersMergedOpen) {
			if v.layersBrowserOpen {
				if v.layersBrowserFocus == 0 {
					v.layersBrowserFocus = 1
					v.app.SetFocus(v.layersBrowserTree)
				} else {
					v.layersBrowserFocus = 0
					v.app.SetFocus(v.layersView)
				}
			} else {
				if v.layersMergedFocus == 0 {
					v.layersMergedFocus = 1
					v.app.SetFocus(v.layersMergedTree)
				} else {
					v.layersMergedFocus = 0
					v.app.SetFocus(v.layersView)
				}
			}
			v.updateLayersFocusStyles()
			return nil
		}
		if event.Key() == tcell.KeyTab {
			v.switchTab((v.activeTab + 1) % 4)
		} else {
			v.switchTab((v.activeTab + 3) % 4)
		}
		return nil
	}

	switch event.Rune() {
	case 'q':
		if v.onBack != nil {
			v.onBack()
		}
		return nil
	case 'r', 'R':
		go v.Refresh()
		return nil
	case '1':
		v.switchTab(ImageDetailTabInfo)
		return nil
	case '2':
		v.switchTab(ImageDetailTabPlatforms)
		return nil
	case '3':
		v.switchTab(ImageDetailTabLayers)
		return nil
	case '4':
		v.switchTab(ImageDetailTabUsedBy)
		return nil
	}

	switch v.activeTab {
	case ImageDetailTabInfo:
		if v.app != nil && v.app.GetFocus() == v.infoConfig {
			return components.HandleTreeInput(event, v.infoConfig, func() { components.ExpandAllNodes(v.infoConfig.GetRoot()) }, nil)
		}
		return components.HandleTreeInput(event, v.infoManifest, func() { components.ExpandAllNodes(v.infoManifest.GetRoot()) }, nil)
	case ImageDetailTabPlatforms:
		if event.Rune() == 'p' || event.Rune() == 'P' {
			v.togglePlatformsPreview()
			return nil
		}
		if v.platformsPreviewOpen && v.platformsPreviewFocus == 1 {
			return event
		}
		return components.HandleTreeInput(event, v.platformsView, func() { components.ExpandAllNodes(v.platformsView.GetRoot()) }, nil)
	case ImageDetailTabLayers:
		switch event.Rune() {
		case 'i':
			// Toggle merged overlay browser.
			if v.layersBrowserOpen {
				v.closeLayersBrowser()
			}
			if v.layersMergedOpen {
				v.closeMergedBrowser()
			} else {
				v.openMergedBrowser()
			}
			return nil
		case 'I':
			// Toggle single-layer diff browser.
			if v.layersMergedOpen {
				v.closeMergedBrowser()
			}
			if v.layersBrowserOpen {
				v.closeLayersBrowser()
			} else {
				v.openLayersBrowser()
			}
			return nil
		case 'd', 'D':
			if v.layersMergedOpen {
				v.layersMergedShowDeleted = !v.layersMergedShowDeleted
				v.rebuildMergedTree()
			}
			return nil
		}
		if v.layersMergedOpen && v.layersMergedFocus == 1 {
			if event.Rune() == 'e' || event.Rune() == 'E' {
				v.toggleMergedNode(v.layersMergedTree.GetCurrentNode())
				return nil
			}
			if event.Rune() == 'a' || event.Rune() == 'A' {
				components.ExpandAllNodes(v.layersMergedTree.GetRoot())
				return nil
			}
			return event
		}
		if v.layersBrowserOpen && v.layersBrowserFocus == 1 {
			if event.Rune() == 'e' || event.Rune() == 'E' {
				v.toggleLayersBrowserNode(v.layersBrowserTree.GetCurrentNode())
				return nil
			}
			if event.Rune() == 'a' || event.Rune() == 'A' {
				components.ExpandAllNodes(v.layersBrowserTree.GetRoot())
				return nil
			}
			return event
		}
		return components.HandleTreeInput(event, v.layersView, func() { components.ExpandAllNodes(v.layersView.GetRoot()) }, nil)
	default:
		return event
	}
}

func (v *ImageDetailView) switchTab(tab ImageDetailTab) {
	v.activeTab = tab
	switch tab {
	case ImageDetailTabInfo:
		v.content.SwitchToPage("info")
		v.renderInfo()
		v.app.SetFocus(v.infoManifest)
	case ImageDetailTabPlatforms:
		v.platformsPreviewOpen = false
		v.platformsPreviewFocus = 0
		v.refreshPlatformsPageLayout()
		v.content.SwitchToPage("platforms")
		v.renderPlatforms()
		v.app.SetFocus(v.platformsView)
	case ImageDetailTabLayers:
		v.layersBrowserOpen = false
		v.layersBrowserFocus = 0
		v.layersMergedOpen = false
		v.layersMergedFocus = 0
		v.refreshLayersPageLayout()
		v.content.SwitchToPage("layers")
		v.renderLayers()
		v.app.SetFocus(v.layersView)
	case ImageDetailTabUsedBy:
		v.content.SwitchToPage("used-by")
		v.renderUsedBy()
		v.app.SetFocus(v.usedByTable)
	}
	v.updateTabBar()
	v.updateFooter()
}

func (v *ImageDetailView) updateTabBar() {
	v.tabBar.Update(imageDetailTabs, int(v.activeTab))
}

func (v *ImageDetailView) updateFooter() {
	hints := []components.FooterHint{
		{Key: "Esc", Action: "back"},
		{Key: "1-4", Action: "pages"},
		{Key: "Tab", Action: "next"},
		{Key: "r", Action: "refresh"},
	}
	if v.activeTab == ImageDetailTabInfo || v.activeTab == ImageDetailTabPlatforms || v.activeTab == ImageDetailTabLayers {
		hints = append(hints, components.FooterHint{Key: "e", Action: "toggle"}, components.FooterHint{Key: "a", Action: "expand/collapse"})
	}
	if v.activeTab == ImageDetailTabInfo {
		hints = append(hints, components.FooterHint{Key: "Tab", Action: "switch pane"})
	}
	if v.activeTab == ImageDetailTabPlatforms {
		hints = append(hints, components.FooterHint{Key: "p", Action: "preview"})
		if v.platformsPreviewOpen {
			hints = append(hints, components.FooterHint{Key: "Tab", Action: "switch pane"})
		}
	}
	if v.activeTab == ImageDetailTabLayers {
		hints = append(hints, components.FooterHint{Key: "i", Action: "merged view"})
		hints = append(hints, components.FooterHint{Key: "I", Action: "diff view"})
		if v.layersBrowserOpen || v.layersMergedOpen {
			hints = append(hints, components.FooterHint{Key: "Tab", Action: "switch pane"})
		}
		if v.layersMergedOpen {
			if v.layersMergedShowDeleted {
				hints = append(hints, components.FooterHint{Key: "d", Action: "hide deleted"})
			} else {
				hints = append(hints, components.FooterHint{Key: "d", Action: "show deleted"})
			}
		}
	}
	v.footer.Update(hints)
}

func (v *ImageDetailView) renderInfo() {
	if v.info == nil {
		v.infoSummary.SetText(" " + components.Muted("Refresh to load image detail"))
		v.infoManifest.SetRoot(components.NewTreeNode(components.Muted("No data")).SetSelectable(false))
		v.infoConfig.SetRoot(components.NewTreeNode(components.Muted("No data")).SetSelectable(false))
		v.infoDetail.SetText(" " + components.Muted("Select folded digest/path fields to see full values."))
		return
	}

	v.renderInfoSummaryPanel()
	v.renderInfoManifestPanel()
	v.renderInfoConfigPanel()
	components.ApplyTreeFocusStyle(v.infoManifest, v.app == nil || v.app.GetFocus() != v.infoConfig)
	components.ApplyTreeFocusStyle(v.infoConfig, v.app != nil && v.app.GetFocus() == v.infoConfig)
	if node := v.infoManifest.GetCurrentNode(); node != nil {
		v.renderInfoDetail(node)
	}
}

func (v *ImageDetailView) renderInfoSummaryPanel() {
	lines := []string{
		gridKV("Digest", fallbackValue(v.info.Digest, "-")),
		gridKV("Size", formatBytes(v.info.Size)),
		gridKV("Kind/Schema", imageKindSchemaSummary(v.config)),
	}
	if v.config != nil {
		if strings.EqualFold(v.config.TargetKind, "Index") {
			lines = append(lines, gridKV("Index Path", parsePathLabel(v.config.IndexPath)))
		}

		platforms := imageInfoPlatforms(v.config)
		if len(platforms) == 0 {
			lines = append(lines, gridKV("Platforms", "-"))
		} else {
			lines = append(lines, gridKV("Platforms", strings.Join(platforms, ", ")))
		}
	}
	v.infoSummary.SetText(" " + strings.Join(lines, "\n "))
}

func (v *ImageDetailView) renderInfoManifestPanel() {
	root := components.NewTreeNode(components.Accent("Current Manifest")).SetSelectable(false).SetExpanded(true)
	manifest := currentManifest(v.config)
	if manifest == nil {
		root.AddChild(components.NewTreeNode(components.Muted("Current manifest unavailable")).SetSelectable(false))
		v.infoManifest.SetRoot(root)
		v.infoManifest.SetCurrentNode(root)
		return
	}

	layersCount := imageManifestLayerCount(manifest.Path)
	root.AddChild(newInfoFieldNode("Digest", manifest.Digest))
	root.AddChild(newInfoFieldNode("Manifest Path", manifest.Path))
	root.AddChild(newInfoFieldNode("Config Path", manifest.ConfigPath))
	root.AddChild(newInfoFieldNode("Platform", fallbackValue(manifest.Platform, "-")))
	root.AddChild(newInfoFieldNode("Rootfs Layers", layersCount))

	v.infoManifest.SetRoot(root)
	v.infoManifest.SetCurrentNode(root)
}

func (v *ImageDetailView) renderInfoConfigPanel() {
	root := components.NewTreeNode(components.Accent("Current Config")).SetSelectable(false).SetExpanded(true)
	manifest := currentManifest(v.config)
	if manifest == nil {
		root.AddChild(components.NewTreeNode(components.Muted("Current config unavailable")).SetSelectable(false))
		v.infoConfig.SetRoot(root)
		v.infoConfig.SetCurrentNode(root)
		return
	}

	user, labelsCount, envCount, workdir, runCmd := imageCurrentConfigFields(manifest.ConfigPath)
	root.AddChild(newInfoFieldNode("User", user))
	root.AddChild(newInfoFieldNode("Labels", labelsCount))
	root.AddChild(newInfoFieldNode("Env", envCount))
	root.AddChild(newInfoFieldNode("Workdir", workdir))
	root.AddChild(newInfoFieldNode("Run", runCmd))

	v.infoConfig.SetRoot(root)
	v.infoConfig.SetCurrentNode(root)
}

func (v *ImageDetailView) renderInfoDetail(node *tview.TreeNode) {
	if node == nil {
		v.infoDetail.SetText(" " + components.Muted("Select folded digest/path fields to see full values."))
		return
	}
	ref, ok := node.GetReference().(*imageInfoDetailRef)
	if !ok || ref == nil {
		v.infoDetail.SetText(" " + components.Muted("Select folded digest/path fields to see full values."))
		return
	}
	v.infoDetail.SetText(fmt.Sprintf(" %s\n %s", components.KV(ref.Title, ""), fallbackValue(ref.Full, "-")))
}

func (v *ImageDetailView) renderPlatforms() {
	root := components.NewTreeNode(components.Accent("Platforms")).SetSelectable(false).SetExpanded(true)
	if v.config == nil {
		root.AddChild(components.NewTreeNode(components.Muted("Image config unavailable")).SetSelectable(false))
	} else {
		// Build a digest→annotations lookup from the index JSON.
		// Per OCI spec, per-platform annotations live in the index under manifests[i].annotations,
		// NOT inside the individual manifest blob (m.Path).
		indexAnnotations := extractIndexAnnotations(v.config.IndexPath)

		// Index section: shown when the image has an index blob.
		if strings.TrimSpace(v.config.IndexPath) != "" {
			indexNode := components.NewTreeNode(fmt.Sprintf("[%s::b]Index[-:-:-]", components.ColorName(components.ColorFgAccentAlt))).SetSelectable(true).SetExpanded(true)
			if pathExists(v.config.IndexPath) {
				indexFileNode := components.NewTreeNode("  " + gridKV("File", foldValue(v.config.IndexPath))).SetSelectable(true)
				indexFileNode.SetReference(&platformsFileRef{Title: "Index", Path: v.config.IndexPath})
				indexNode.AddChild(indexFileNode)
				for _, line := range parsedManifestHighlights(v.config.IndexPath, false) {
					indexNode.AddChild(components.NewTreeNode("  " + line).SetSelectable(true))
				}
			} else {
				indexNode.AddChild(components.NewTreeNode("  " + gridKV("File", parsePathLabel(v.config.IndexPath))).SetSelectable(true))
			}
			root.AddChild(indexNode)
		}

		currentPlatform := strings.TrimSpace(imageCurrentPlatform(v.config))
		manifests := v.config.Manifests
		if len(manifests) == 0 && v.config.Manifest != nil {
			manifests = []*runtime.ImageManifest{v.config.Manifest}
		}

		for _, m := range manifests {
			label := fallbackValue(m.Platform, "(unknown)")
			isCurrent := currentPlatform != "" && strings.TrimSpace(m.Platform) == currentPlatform
			if isCurrent {
				label += " " + components.Muted("(current)")
			}
			normalized := strings.ToLower(strings.TrimSpace(m.Platform))
			isUnknown := normalized == "" || normalized == "unknown" || normalized == "unknown/unknown"
			var nodeLabel string
			if isUnknown && !isCurrent {
				nodeLabel = components.Muted(label)
			} else {
				nodeLabel = fmt.Sprintf("[%s::b]%s[-:-:-]", components.ColorName(components.ColorFgAccentAlt), label)
			}
			node := components.NewTreeNode(nodeLabel).SetSelectable(true).SetExpanded(false)
			node.AddChild(components.NewTreeNode("  " + gridKV("Digest", fallbackValue(m.Digest, "-"))).SetSelectable(true))
			if pathExists(m.Path) {
				manifestNode := components.NewTreeNode("  " + gridKV("Manifest", foldValue(m.Path))).SetSelectable(true)
				manifestNode.SetReference(&platformsFileRef{Title: "Manifest — " + fallbackValue(m.Platform, "unknown"), Path: m.Path})
				node.AddChild(manifestNode)
				if pathExists(m.ConfigPath) {
					configNode := components.NewTreeNode("  " + gridKV("Config", foldValue(m.ConfigPath))).SetSelectable(true)
					configNode.SetReference(&platformsFileRef{Title: "Config — " + fallbackValue(m.Platform, "unknown"), Path: m.ConfigPath})
					node.AddChild(configNode)
				} else {
					node.AddChild(components.NewTreeNode("  " + gridKV("Config", parsePathLabel(m.ConfigPath))).SetSelectable(true))
				}
			} else {
				node.AddChild(components.NewTreeNode("  " + gridKV("Fetched to local", "False")).SetSelectable(true))
			}
			if pathExists(m.Path) {
				for _, line := range parsedManifestHighlights(m.Path, isCurrent) {
					node.AddChild(components.NewTreeNode("  " + line).SetSelectable(true))
				}
			}
			// Merge per-platform annotations from the index with manifest-level annotations.
			annotations := make(map[string]string)
			for k, vv := range indexAnnotations[m.Digest] {
				annotations[k] = vv
			}
			if pathExists(m.Path) {
				for k, vv := range extractAnnotations(m.Path) {
					annotations[k] = vv
				}
			}
			if len(annotations) > 0 {
				annotationsNode := components.NewTreeNode("  " + components.Accent("Annotations")).SetSelectable(true).SetExpanded(false)
				keys := make([]string, 0, len(annotations))
				for k := range annotations {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					annotationsNode.AddChild(components.NewTreeNode("    " + components.KV(k+":", foldValue(annotations[k]))).SetSelectable(true))
				}
				node.AddChild(annotationsNode)
			}
			root.AddChild(node)
		}
		if len(manifests) == 0 {
			root.AddChild(components.NewTreeNode(components.Muted("No manifest entries")).SetSelectable(false))
		}
	}
	v.platformsView.SetRoot(root)
	v.platformsView.SetCurrentNode(root)
	v.updatePlatformsDetail(root)
	v.updatePlatformsFocusStyles()
}

func (v *ImageDetailView) togglePlatformsPreview() {
	v.platformsPreviewOpen = !v.platformsPreviewOpen
	if !v.platformsPreviewOpen {
		v.platformsPreviewFocus = 0
		v.app.SetFocus(v.platformsView)
	} else {
		v.updatePlatformsPreview(v.platformsView.GetCurrentNode())
	}
	v.refreshPlatformsPageLayout()
	v.updatePlatformsFocusStyles()
	v.updateFooter()
}

func (v *ImageDetailView) refreshPlatformsPageLayout() {
	v.platformsBody.Clear()
	v.platformsBody.AddItem(v.platformsView, 0, 1, !v.platformsPreviewOpen || v.platformsPreviewFocus == 0)
	if v.platformsPreviewOpen {
		v.platformsBody.AddItem(v.platformsPreview, 0, 1, v.platformsPreviewFocus == 1)
	}
}

func (v *ImageDetailView) updatePlatformsFocusStyles() {
	if v.platformsPreviewOpen {
		components.ApplyTreeFocusStyle(v.platformsView, v.platformsPreviewFocus == 0)
		if v.platformsPreviewFocus == 1 {
			v.platformsPreview.SetBorderColor(components.ColorFgAccent)
		} else {
			v.platformsPreview.SetBorderColor(components.ColorFgBorder)
		}
	} else {
		components.ApplyTreeFocusStyle(v.platformsView, true)
	}
	v.platformsDetail.SetBorderColor(components.ColorFgBorder)
}

func (v *ImageDetailView) updatePlatformsPreview(node *tview.TreeNode) {
	if node == nil {
		v.platformsPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("File Preview")))
		v.platformsPreview.SetText(" " + components.Muted("Select a Manifest or Config node to preview."))
		return
	}
	ref, ok := node.GetReference().(*platformsFileRef)
	if !ok || ref == nil {
		v.platformsPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("File Preview")))
		v.platformsPreview.SetText(" " + components.Muted("Select a Manifest or Config node to preview."))
		return
	}
	v.platformsPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent(ref.Title)))
	data, err := os.ReadFile(ref.Path)
	if err != nil {
		v.platformsPreview.SetText(fmt.Sprintf(" [%s]read error: %v[-]", components.ColorName(components.ColorFgError), err))
		return
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err == nil {
		v.platformsPreview.SetText(buf.String())
	} else {
		v.platformsPreview.SetText(string(data))
	}
	v.platformsPreview.ScrollToBeginning()
}

func (v *ImageDetailView) updatePlatformsDetail(node *tview.TreeNode) {
	placeholder := " " + components.Muted("Select a field to see full values.")
	if node == nil {
		v.platformsDetail.SetText(placeholder)
		return
	}
	// If the node has a file reference, show the file path.
	if ref, ok := node.GetReference().(*platformsFileRef); ok && ref != nil {
		v.platformsDetail.SetText(fmt.Sprintf(" %s\n %s", components.KV(ref.Title, ""), ref.Path))
		return
	}
	// Otherwise, show the node text (which may be folded/truncated in the tree).
	text := strings.TrimPrefix(node.GetText(), "  ")
	if strings.TrimSpace(text) != "" {
		v.platformsDetail.SetText(" " + text)
	} else {
		v.platformsDetail.SetText(placeholder)
	}
}

func (v *ImageDetailView) renderLayers() {
	root := components.NewTreeNode(components.Accent("Layers")).SetSelectable(false).SetExpanded(true)
	if len(v.layers) == 0 {
		root.AddChild(components.NewTreeNode(components.Muted("No layer data")).SetSelectable(false))
		v.layersView.SetRoot(root)
		v.layersView.SetCurrentNode(root)
		v.updateLayersDetail(nil)
		v.updateLayersFocusStyles()
		return
	}

	var history []string
	if v.config != nil && v.config.Manifest != nil {
		history = loadConfigHistory(v.config.Manifest.ConfigPath)
	}

	// addField creates an indented child node with the value folded and a detail ref attached.
	addField := func(parent *tview.TreeNode, label, full string) {
		child := components.NewTreeNode("  " + gridKV(label, foldValue(full))).SetSelectable(true)
		child.SetReference(&imageInfoDetailRef{Title: label, Full: fallbackValue(full, "-")})
		parent.AddChild(child)
	}

	for _, layer := range v.layers {
		id := fallbackValue(layer.UncompressedDigest, fallbackValue(layer.CompressedDigest, "-"))
		node := components.NewTreeNode(fmt.Sprintf("[%s]#%d[-] %s  %s",
			components.ColorName(components.ColorFgMuted),
			layer.Index,
			components.Bright(shortID(id)),
			components.Muted(formatBytes(layer.Size)),
		)).SetSelectable(true).SetExpanded(false)
		node.SetReference(&imageLayerRef{layer: layer})

		addField(node, "Compressed", layer.CompressedDigest)
		addField(node, "Uncompressed", layer.UncompressedDigest)
		node.AddChild(components.NewTreeNode("  " + gridKV("Compression", fallbackValue(layer.CompressionType, "-"))).SetSelectable(true))
		if layer.Path != "" {
			addField(node, "Path", layer.Path)
		} else {
			node.AddChild(components.NewTreeNode("  " + gridKV("Path", imageMissingPathMarker)).SetSelectable(true))
		}
		node.AddChild(components.NewTreeNode("  " + gridKV("Usage", fmt.Sprintf("%s / %d inodes", formatBytes(layer.UsageSize), layer.UsageInodes))).SetSelectable(true))
		if layer.Index < len(history) {
			addField(node, "History", history[layer.Index])
		}
		if layer.Containerd != nil {
			if layer.Containerd.ContentPath != "" {
				addField(node, "Content Path", layer.Containerd.ContentPath)
			} else {
				node.AddChild(components.NewTreeNode("  " + gridKV("Content Path", imageMissingPathMarker)).SetSelectable(true))
			}
			node.AddChild(components.NewTreeNode("  " + gridKV("Snapshot Key", fallbackValue(layer.Containerd.SnapshotKey, "-"))).SetSelectable(true))
		}
		if layer.Docker != nil {
			node.AddChild(components.NewTreeNode("  " + gridKV("Cache ID", fallbackValue(layer.Docker.CacheID, "-"))).SetSelectable(true))
			node.AddChild(components.NewTreeNode("  " + gridKV("Graph Driver", fallbackValue(layer.Docker.GraphDriver, "-"))).SetSelectable(true))
			if layer.Docker.ShortLinkPath != "" {
				addField(node, "Short Link", layer.Docker.ShortLinkPath)
			} else {
				node.AddChild(components.NewTreeNode("  " + gridKV("Short Link", imageMissingPathMarker)).SetSelectable(true))
			}
		}
		if layer.Crio != nil {
			node.AddChild(components.NewTreeNode("  " + gridKV("Layer ID", fallbackValue(layer.Crio.ID, "-"))).SetSelectable(true))
			node.AddChild(components.NewTreeNode("  " + gridKV("Overlay Link", fallbackValue(layer.Crio.OverlayLinkID, "-"))).SetSelectable(true))
		}
		root.AddChild(node)
	}
	v.layersView.SetRoot(root)
	v.layersView.SetCurrentNode(root)
	v.updateLayersDetail(root)
	v.updateLayersFocusStyles()
}

func (v *ImageDetailView) updateLayersDetail(node *tview.TreeNode) {
	placeholder := " " + components.Muted("Select a layer node to browse, or a field to see its full value.")
	if node == nil {
		v.layersDetail.SetText(placeholder)
		return
	}
	if ref, ok := node.GetReference().(*imageInfoDetailRef); ok && ref != nil {
		v.layersDetail.SetText(fmt.Sprintf(" %s\n %s", components.KV(ref.Title, ""), ref.Full))
		return
	}
	if ref, ok := node.GetReference().(*imageLayerRef); ok && ref != nil {
		if ref.layer.Path != "" {
			v.layersDetail.SetText(fmt.Sprintf(" %s\n %s",
				components.KV(fmt.Sprintf("Layer #%d Path:", ref.layer.Index), ""), ref.layer.Path))
		} else {
			v.layersDetail.SetText(placeholder)
		}
		return
	}
	v.layersDetail.SetText(placeholder)
}

func (v *ImageDetailView) refreshLayersPageLayout() {
	v.layersBody.Clear()
	layersFocused := !v.layersBrowserOpen && !v.layersMergedOpen
	v.layersBody.AddItem(v.layersView, 0, 1, layersFocused)
	if v.layersBrowserOpen {
		v.layersBody.AddItem(v.layersBrowser, 0, 1, v.layersBrowserFocus == 1)
	}
	if v.layersMergedOpen {
		v.layersBody.AddItem(v.layersMergedBrowser, 0, 1, v.layersMergedFocus == 1)
	}
}

func (v *ImageDetailView) updateLayersFocusStyles() {
	anyBrowserOpen := v.layersBrowserOpen || v.layersMergedOpen
	var rightFocus int
	if v.layersBrowserOpen {
		rightFocus = v.layersBrowserFocus
	} else if v.layersMergedOpen {
		rightFocus = v.layersMergedFocus
	}
	components.ApplyTreeFocusStyle(v.layersView, !anyBrowserOpen || rightFocus == 0)
	components.ApplyTreeFocusStyle(v.layersBrowserTree, v.layersBrowserOpen && v.layersBrowserFocus == 1)
	components.ApplyTreeFocusStyle(v.layersMergedTree, v.layersMergedOpen && v.layersMergedFocus == 1)
	if v.layersBrowserOpen && v.layersBrowserFocus == 1 {
		v.layersBrowserInfo.SetBorderColor(components.ColorFgAccent)
	} else {
		v.layersBrowserInfo.SetBorderColor(components.ColorFgBorder)
	}
	if v.layersMergedOpen && v.layersMergedFocus == 1 {
		v.layersMergedInfo.SetBorderColor(components.ColorFgAccent)
	} else {
		v.layersMergedInfo.SetBorderColor(components.ColorFgBorder)
	}
}

func (v *ImageDetailView) openLayersBrowser() {
	node := v.layersView.GetCurrentNode()
	if node == nil {
		return
	}
	ref, ok := node.GetReference().(*imageLayerRef)
	if !ok || ref == nil || ref.layer == nil {
		return
	}
	layer := ref.layer
	if !dirExists(layer.Path) {
		v.layersDetail.SetText(fmt.Sprintf(" %s",
			components.Muted(fmt.Sprintf("Layer #%d: diff path not available for browsing.", layer.Index))))
		return
	}
	v.layersBrowserOpen = true
	v.layersBrowserFocus = 1
	v.initLayersBrowserTree(layer)
	v.refreshLayersPageLayout()
	v.app.SetFocus(v.layersBrowserTree)
	v.updateLayersFocusStyles()
	v.updateFooter()
}

func (v *ImageDetailView) closeLayersBrowser() {
	v.layersBrowserOpen = false
	v.layersBrowserFocus = 0
	v.layersBrowserTree.SetRoot(components.NewTreeNode(components.Muted("No layer selected")).SetSelectable(false))
	v.layersBrowserInfo.SetText(" " + components.Muted("Select a layer node and press i to browse its diff directory."))
	v.layersBrowserPreview.SetText(" " + components.Muted("No file selected."))
	v.layersBrowserPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
	v.refreshLayersPageLayout()
	v.app.SetFocus(v.layersView)
	v.updateLayersFocusStyles()
	v.updateFooter()
}

func (v *ImageDetailView) initLayersBrowserTree(layer *runtime.ImageLayer) {
	v.layersBrowserInfo.SetText(fmt.Sprintf(
		" %s %s  %s\n %s",
		components.Muted("Layer"), components.Bright(fmt.Sprintf("#%d", layer.Index)),
		components.Muted("(diff directory)"),
		layer.Path,
	))
	entry := &layerBrowserEntry{path: layer.Path, relPath: "", isDir: true}
	root := components.NewTreeNode(v.layersDiffNodeText("/", true)).
		SetSelectable(true).SetExpanded(true).SetReference(entry)
	v.loadLayersBrowserChildren(root, entry)
	v.layersBrowserTree.SetRoot(root)
	v.layersBrowserTree.SetCurrentNode(root)
	v.renderLayersBrowserPreview(root)
}

func (v *ImageDetailView) loadLayersBrowserChildren(parent *tview.TreeNode, entry *layerBrowserEntry) {
	if parent == nil || entry == nil || !entry.isDir || entry.loaded {
		return
	}
	parent.ClearChildren()
	entries, err := os.ReadDir(entry.path)
	if err != nil {
		parent.AddChild(components.NewTreeNode(
			fmt.Sprintf("[%s]%s[-]", components.ColorName(components.ColorFgError), err.Error()),
		).SetSelectable(false))
		entry.loaded = true
		return
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].IsDir() != entries[j].IsDir() {
			return entries[i].IsDir()
		}
		return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name())
	})
	for _, de := range entries {
		childPath := filepath.Join(entry.path, de.Name())
		childRelPath := de.Name()
		if entry.relPath != "" {
			childRelPath = filepath.Join(entry.relPath, de.Name())
		}
		childEntry := &layerBrowserEntry{path: childPath, relPath: childRelPath, isDir: de.IsDir()}
		name := de.Name()
		if de.IsDir() {
			name += "/"
		}
		child := components.NewTreeNode(v.layersDiffNodeText(name, de.IsDir())).
			SetSelectable(true).SetExpanded(false).SetReference(childEntry)
		parent.AddChild(child)
	}
	entry.loaded = true
}

// layersDiffNodeText returns styled display text for an overlay diff directory entry.
//
// OCI/overlay whiteout conventions:
//
//	.wh.<name>    — deletion marker: hides <name> from lower layers
//	.wh..wh..opq  — opaque whiteout: hides all lower-layer content in this directory
func (v *ImageDetailView) layersDiffNodeText(name string, isDir bool) string {
	base := strings.TrimSuffix(name, "/")
	if base == ".wh..wh..opq" {
		return fmt.Sprintf("[%s::b]%s[-:-:-] %s",
			components.ColorName(components.ColorFgError), name, components.Muted("opaque whiteout"))
	}
	if strings.HasPrefix(base, ".wh.") {
		deleted := strings.TrimPrefix(base, ".wh.")
		return fmt.Sprintf("[%s]%s[-] %s",
			components.ColorName(components.ColorFgError), name, components.Muted("(deletes "+deleted+")"))
	}
	if isDir {
		return fmt.Sprintf("[%s]%s[-]", components.ColorName(components.ColorFgAccentAlt), name)
	}
	return name
}

func (v *ImageDetailView) toggleLayersBrowserNode(node *tview.TreeNode) {
	if node == nil {
		return
	}
	entry, _ := node.GetReference().(*layerBrowserEntry)
	if entry == nil || !entry.isDir {
		v.renderLayersBrowserPreview(node)
		return
	}
	v.loadLayersBrowserChildren(node, entry)
	node.SetExpanded(!node.IsExpanded())
	v.renderLayersBrowserPreview(node)
}

func (v *ImageDetailView) renderLayersBrowserPreview(node *tview.TreeNode) {
	if node == nil {
		v.layersBrowserPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.layersBrowserPreview.SetText(" " + components.Muted("No file selected."))
		return
	}
	entry, _ := node.GetReference().(*layerBrowserEntry)
	if entry == nil || entry.isDir {
		v.layersBrowserPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.layersBrowserPreview.SetText(" " + components.Muted("Select a file to preview."))
		return
	}
	fi, err := os.Stat(entry.path)
	if err != nil {
		v.layersBrowserPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.layersBrowserPreview.SetText(fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err))
		return
	}
	name := filepath.Base(entry.path)
	_, isText := detectFileType(entry.path)
	v.layersBrowserPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview — "+name)))
	var lines []string
	lines = append(lines, fmt.Sprintf(" %s  %s",
		components.KV("File:", name), components.KV("Size:", formatBytes(fi.Size()))))
	if !isText || fi.Size() > 512*1024 {
		fileType, _ := detectFileType(entry.path)
		lines = append(lines, " "+components.Muted("("+fileType+")"))
	} else {
		data, err := os.ReadFile(entry.path)
		if err == nil {
			lines = append(lines, "")
			lines = append(lines, string(data))
		} else {
			lines = append(lines, " "+components.Muted(fmt.Sprintf("read error: %v", err)))
		}
	}
	v.layersBrowserPreview.SetText(strings.Join(lines, "\n"))
	v.layersBrowserPreview.ScrollToBeginning()
}

// ---------------------------------------------------------------------------
// Merged overlay filesystem browser
// ---------------------------------------------------------------------------

func (v *ImageDetailView) selectedLayerIndex() int {
	node := v.layersView.GetCurrentNode()
	if node == nil {
		return -1
	}
	ref, ok := node.GetReference().(*imageLayerRef)
	if !ok || ref == nil {
		return -1
	}
	return ref.layer.Index
}

// mergeLayerDir merges entries from all layers' diff directories for a given
// relative path, applying OCI overlay semantics (whiteouts, opaques).
func (v *ImageDetailView) mergeLayerDir(relDir string) (visible []imageMergedNode, deleted []imageMergedNode) {
	visMap := make(map[string]*imageMergedNode)
	delMap := make(map[string]int) // name → deletedByLayer

	for _, layer := range v.layers {
		if layer.Path == "" {
			continue
		}
		dirPath := layer.Path
		if relDir != "" {
			dirPath = filepath.Join(layer.Path, relDir)
		}
		entries, err := os.ReadDir(dirPath)
		if err != nil {
			continue
		}
		for _, de := range entries {
			name := de.Name()
			if name == ".wh..wh..opq" {
				// Opaque whiteout: clear everything from lower layers.
				for k, v := range visMap {
					if v.fromLayer < layer.Index {
						delete(visMap, k)
					}
				}
				for k, dl := range delMap {
					if dl < layer.Index {
						delete(delMap, k)
					}
				}
				continue
			}
			if strings.HasPrefix(name, ".wh.") {
				target := strings.TrimPrefix(name, ".wh.")
				delete(visMap, target)
				delMap[target] = layer.Index
				continue
			}
			relPath := name
			if relDir != "" {
				relPath = filepath.Join(relDir, name)
			}
			visMap[name] = &imageMergedNode{
				name:           name,
				relPath:        relPath,
				isDir:          de.IsDir(),
				fromLayer:      layer.Index,
				deletedByLayer: -1,
			}
			delete(delMap, name) // re-addition cancels previous deletion
		}
	}

	visible = make([]imageMergedNode, 0, len(visMap))
	for _, n := range visMap {
		visible = append(visible, *n)
	}
	sort.SliceStable(visible, func(i, j int) bool {
		if visible[i].isDir != visible[j].isDir {
			return visible[i].isDir
		}
		return strings.ToLower(visible[i].name) < strings.ToLower(visible[j].name)
	})

	deleted = make([]imageMergedNode, 0, len(delMap))
	for name, dl := range delMap {
		relPath := name
		if relDir != "" {
			relPath = filepath.Join(relDir, name)
		}
		deleted = append(deleted, imageMergedNode{
			name:           name,
			relPath:        relPath,
			isDir:          false,
			fromLayer:      -1,
			deletedByLayer: dl,
		})
	}
	sort.SliceStable(deleted, func(i, j int) bool {
		return strings.ToLower(deleted[i].name) < strings.ToLower(deleted[j].name)
	})

	return visible, deleted
}

func (v *ImageDetailView) openMergedBrowser() {
	// Check that at least one layer has a browsable diff path.
	hasAny := false
	for _, layer := range v.layers {
		if dirExists(layer.Path) {
			hasAny = true
			break
		}
	}
	if !hasAny {
		v.layersDetail.SetText(" " + components.Muted("Merged view unavailable: no layer diff paths are accessible."))
		return
	}
	v.layersMergedOpen = true
	v.layersMergedFocus = 1
	v.layersMergedShowDeleted = false
	v.initMergedTree()
	v.refreshLayersPageLayout()
	v.app.SetFocus(v.layersMergedTree)
	v.updateLayersFocusStyles()
	v.updateFooter()
}

func (v *ImageDetailView) closeMergedBrowser() {
	v.layersMergedOpen = false
	v.layersMergedFocus = 0
	v.layersMergedTree.SetRoot(components.NewTreeNode(components.Muted("No data")).SetSelectable(false))
	v.layersMergedInfo.SetText(" " + components.Muted("Press i to open merged filesystem view."))
	v.layersMergedPreview.SetText(" " + components.Muted("No file selected."))
	v.layersMergedPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
	v.refreshLayersPageLayout()
	v.app.SetFocus(v.layersView)
	v.updateLayersFocusStyles()
	v.updateFooter()
}

func (v *ImageDetailView) initMergedTree() {
	count := 0
	for _, l := range v.layers {
		if dirExists(l.Path) {
			count++
		}
	}
	v.layersMergedInfo.SetText(fmt.Sprintf(
		" %s %s  %s %s",
		components.Muted("Layers:"), components.Bright(fmt.Sprintf("%d/%d browsable", count, len(v.layers))),
		components.Muted("Deleted:"), components.Bright(fmt.Sprintf("%v", v.layersMergedShowDeleted)),
	))
	selLayer := v.selectedLayerIndex()
	rootNode := &imageMergedNode{name: "/", relPath: "", isDir: true, fromLayer: -1, deletedByLayer: -1}
	root := components.NewTreeNode(v.mergedNodeText(*rootNode, selLayer)).
		SetSelectable(true).SetExpanded(true).SetReference(rootNode)
	v.loadMergedChildren(root, "")
	v.layersMergedTree.SetRoot(root)
	v.layersMergedTree.SetCurrentNode(root)
	v.renderMergedPreview(root)
}

func (v *ImageDetailView) rebuildMergedTree() {
	v.initMergedTree()
	v.updateFooter()
}

func (v *ImageDetailView) loadMergedChildren(parent *tview.TreeNode, relDir string) {
	ref, _ := parent.GetReference().(*imageMergedNode)
	if ref != nil && ref.loaded {
		return
	}
	parent.ClearChildren()
	visible, deleted := v.mergeLayerDir(relDir)
	selLayer := v.selectedLayerIndex()
	for i := range visible {
		n := &visible[i]
		name := n.name
		if n.isDir {
			name += "/"
		}
		child := components.NewTreeNode(v.mergedNodeText(*n, selLayer)).
			SetSelectable(true).SetExpanded(false).SetReference(n)
		parent.AddChild(child)
	}
	if v.layersMergedShowDeleted {
		for i := range deleted {
			n := &deleted[i]
			child := components.NewTreeNode(v.mergedNodeText(*n, selLayer)).
				SetSelectable(true).SetExpanded(false).SetReference(n)
			parent.AddChild(child)
		}
	}
	if ref != nil {
		ref.loaded = true
	}
}

func (v *ImageDetailView) mergedNodeText(n imageMergedNode, selLayer int) string {
	name := n.name
	if n.isDir {
		name += "/"
	}
	// Deleted entry.
	if n.deletedByLayer >= 0 {
		tag := components.Muted(fmt.Sprintf(" [×L%d]", n.deletedByLayer))
		if n.deletedByLayer == selLayer {
			return fmt.Sprintf("[%s::b]~%s~[-:-:-]%s",
				components.ColorName(components.ColorFgError), name, tag)
		}
		return fmt.Sprintf("[%s]~%s~[-]%s",
			components.ColorName(components.ColorFgMuted), name, tag)
	}
	// Root node.
	if n.relPath == "" {
		return fmt.Sprintf("[%s]%s[-]", components.ColorName(components.ColorFgAccentAlt), name)
	}
	// Active layer match: strong highlight.
	tag := components.Muted(fmt.Sprintf(" [#%d]", n.fromLayer))
	if n.fromLayer == selLayer {
		if n.isDir {
			return fmt.Sprintf("[%s::b]%s[-:-:-]%s", components.ColorName(components.ColorFgAccentAlt), name, tag)
		}
		return fmt.Sprintf("[%s::b]%s[-:-:-]%s", components.ColorName(components.ColorFgAccentAlt), name, tag)
	}
	// Normal entry.
	if n.isDir {
		return fmt.Sprintf("[%s]%s[-]%s", components.ColorName(components.ColorFgAccentAlt), name, tag)
	}
	return name + tag
}

func (v *ImageDetailView) toggleMergedNode(node *tview.TreeNode) {
	if node == nil {
		return
	}
	ref, _ := node.GetReference().(*imageMergedNode)
	if ref == nil || !ref.isDir {
		v.renderMergedPreview(node)
		return
	}
	v.loadMergedChildren(node, ref.relPath)
	node.SetExpanded(!node.IsExpanded())
	v.renderMergedPreview(node)
}

func (v *ImageDetailView) updateMergedHighlights() {
	root := v.layersMergedTree.GetRoot()
	if root == nil {
		return
	}
	selLayer := v.selectedLayerIndex()
	// Update info bar with deletion toggle state.
	count := 0
	for _, l := range v.layers {
		if dirExists(l.Path) {
			count++
		}
	}
	v.layersMergedInfo.SetText(fmt.Sprintf(
		" %s %s  %s %s  %s %s",
		components.Muted("Layers:"), components.Bright(fmt.Sprintf("%d/%d browsable", count, len(v.layers))),
		components.Muted("Deleted:"), components.Bright(fmt.Sprintf("%v", v.layersMergedShowDeleted)),
		components.Muted("Selected:"), components.Bright(fmt.Sprintf("#%d", selLayer)),
	))
	var walk func(node *tview.TreeNode)
	walk = func(node *tview.TreeNode) {
		ref, _ := node.GetReference().(*imageMergedNode)
		if ref != nil {
			node.SetText(v.mergedNodeText(*ref, selLayer))
		}
		for _, child := range node.GetChildren() {
			walk(child)
		}
	}
	walk(root)
}

func (v *ImageDetailView) renderMergedPreview(node *tview.TreeNode) {
	if node == nil {
		v.layersMergedPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.layersMergedPreview.SetText(" " + components.Muted("No file selected."))
		return
	}
	ref, _ := node.GetReference().(*imageMergedNode)
	if ref == nil || ref.isDir {
		v.layersMergedPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.layersMergedPreview.SetText(" " + components.Muted("Select a file to preview."))
		return
	}
	if ref.deletedByLayer >= 0 {
		v.layersMergedPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.layersMergedPreview.SetText(fmt.Sprintf(" %s %s deleted by layer #%d",
			components.Muted("File"), ref.name, ref.deletedByLayer))
		return
	}
	// Find the actual file on disk from the fromLayer.
	var realPath string
	for _, layer := range v.layers {
		if layer.Index == ref.fromLayer && layer.Path != "" {
			realPath = filepath.Join(layer.Path, ref.relPath)
			break
		}
	}
	if realPath == "" {
		v.layersMergedPreview.SetText(" " + components.Muted("File path unavailable."))
		return
	}
	fi, err := os.Stat(realPath)
	if err != nil {
		v.layersMergedPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview")))
		v.layersMergedPreview.SetText(fmt.Sprintf(" [%s]%v[-]", components.ColorName(components.ColorFgError), err))
		return
	}
	_, isText := detectFileType(realPath)
	v.layersMergedPreview.SetTitle(fmt.Sprintf(" %s ", components.Accent("Preview — "+ref.name)))
	var lines []string
	lines = append(lines, fmt.Sprintf(" %s  %s  %s",
		components.KV("File:", ref.name),
		components.KV("Size:", formatBytes(fi.Size())),
		components.KV("Layer:", fmt.Sprintf("#%d", ref.fromLayer))))
	if !isText || fi.Size() > 512*1024 {
		fileType, _ := detectFileType(realPath)
		lines = append(lines, " "+components.Muted("("+fileType+")"))
	} else {
		data, err := os.ReadFile(realPath)
		if err == nil {
			lines = append(lines, "")
			lines = append(lines, string(data))
		} else {
			lines = append(lines, " "+components.Muted(fmt.Sprintf("read error: %v", err)))
		}
	}
	v.layersMergedPreview.SetText(strings.Join(lines, "\n"))
	v.layersMergedPreview.ScrollToBeginning()
}

func (v *ImageDetailView) renderUsedBy() {
	v.usedByTable.ClearData()
	for _, entry := range v.usedBy {
		status := string(entry.Status)
		if status == "" {
			status = "unknown"
		}
		pod := fallbackValue(entry.Pod, "-")
		name := fallbackValue(entry.ContainerName, shortID(entry.ContainerID))
		v.usedByTable.AddRow(name, pod, status, formatSummaryTime(entry.CreatedAt))
	}
	if len(v.usedBy) == 0 {
		v.usedByTable.AddRow(components.Muted("No containers currently reference this image"), "", "", "")
	}
}

func (v *ImageDetailView) resolveUsedBy(ctx context.Context, info *runtime.ImageInfo) ([]imageUsedByEntry, error) {
	containers, err := v.rt.ListContainers(ctx)
	if err != nil {
		return nil, err
	}
	aliases := make(map[string]bool)
	if info != nil {
		for _, name := range info.Names {
			name = strings.TrimSpace(name)
			if name != "" {
				aliases[name] = true
			}
		}
		if info.Digest != "" {
			aliases[info.Digest] = true
		}
	}
	result := make([]imageUsedByEntry, 0)
	for _, c := range containers {
		cInfo, err := c.Info(ctx)
		if err != nil || cInfo == nil {
			continue
		}
		cCfg, _ := c.Config(ctx)
		if !imageRefMatches(aliases, cInfo.Image, cCfg) {
			continue
		}
		pod := ""
		if cInfo.PodNamespace != "" || cInfo.PodName != "" {
			pod = fallbackValue(cInfo.PodNamespace, "?") + "/" + fallbackValue(cInfo.PodName, "?")
		}
		result = append(result, imageUsedByEntry{
			ContainerName: cInfo.Name,
			ContainerID:   cInfo.ID,
			Pod:           pod,
			Status:        cInfo.Status,
			CreatedAt:     cInfo.CreatedAt,
			ImageRef:      cInfo.Image,
		})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Status != result[j].Status {
			return result[i].Status == runtime.ContainerStatusRunning
		}
		return result[i].ContainerName < result[j].ContainerName
	})
	return result, nil
}

func imageRefMatches(aliases map[string]bool, infoImage string, cfg *runtime.ContainerConfig) bool {
	candidates := []string{strings.TrimSpace(infoImage)}
	if cfg != nil {
		candidates = append(candidates, strings.TrimSpace(cfg.ImageName))
	}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if aliases[c] {
			return true
		}
		if _, digest, ok := strings.Cut(c, "@"); ok && aliases[digest] {
			return true
		}
	}
	return false
}

func parsePathLabel(path string) string {
	if strings.TrimSpace(path) == "" {
		return imageMissingPathMarker
	}
	return path
}

func pathExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func dirExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

func imageTopNamesLine(info *runtime.ImageInfo) string {
	if info == nil || len(info.Names) == 0 {
		return "-"
	}
	names := make([]string, 0, len(info.Names))
	for _, n := range info.Names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return "-"
	}
	if len(names) <= 5 {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:4], ", ") + fmt.Sprintf("  %s", components.Muted(fmt.Sprintf("(+%d more)", len(names)-4)))
}

func currentManifest(config *runtime.ImageConfigInfo) *runtime.ImageManifest {
	if config == nil {
		return nil
	}
	if config.Manifest != nil {
		return config.Manifest
	}
	if len(config.Manifests) > 0 {
		return config.Manifests[0]
	}
	return nil
}

func foldValue(value string) string {
	v := fallbackValue(strings.TrimSpace(value), "-")
	r := []rune(v)
	if len(r) <= imageFoldedValueWidth {
		return v
	}
	return string(r[:imageFoldedValueWidth-3]) + "..."
}

func newInfoFieldNode(label, value string) *tview.TreeNode {
	full := fallbackValue(strings.TrimSpace(value), "-")
	node := components.NewTreeNode(gridKV(label, foldValue(full))).SetSelectable(true)
	node.SetReference(&imageInfoDetailRef{Title: label, Full: full})
	return node
}

func imageInfoPlatforms(config *runtime.ImageConfigInfo) []string {
	if config == nil {
		return nil
	}
	current := strings.TrimSpace(imageCurrentPlatform(config))
	manifests := config.Manifests
	if len(manifests) == 0 && config.Manifest != nil {
		manifests = []*runtime.ImageManifest{config.Manifest}
	}
	seen := make(map[string]bool)
	parts := make([]string, 0, len(manifests))
	for _, manifest := range manifests {
		if manifest == nil {
			continue
		}
		p := strings.TrimSpace(manifest.Platform)
		if p == "" {
			p = "(unknown)"
		}
		if seen[p] {
			continue
		}
		seen[p] = true
		if p == current {
			parts = append(parts, fmt.Sprintf("[%s::b]%s[-:-:-]%s", components.ColorName(components.ColorFgAccentAlt), p, components.Muted("(current)")))
		} else {
			parts = append(parts, p)
		}
	}
	return parts
}

func imageManifestLayerCount(manifestPath string) string {
	if strings.TrimSpace(manifestPath) == "" {
		return imageMissingPathMarker
	}
	obj, err := parseJSONFile(manifestPath)
	if err != nil {
		return fmt.Sprintf("parse error: %v", err)
	}
	layers, ok := obj["layers"].([]interface{})
	if !ok {
		return "0"
	}
	return fmt.Sprintf("%d", len(layers))
}

func imageCurrentConfigFields(configPath string) (user, labelsCount, envCount, workdir, runCmd string) {
	user = "-"
	labelsCount = "-"
	envCount = "-"
	workdir = "-"
	runCmd = "-"
	if strings.TrimSpace(configPath) == "" {
		user = imageMissingPathMarker
		labelsCount = imageMissingPathMarker
		envCount = imageMissingPathMarker
		workdir = imageMissingPathMarker
		runCmd = imageMissingPathMarker
		return
	}
	obj, err := parseJSONFile(configPath)
	if err != nil {
		msg := fmt.Sprintf("parse error: %v", err)
		return msg, msg, msg, msg, msg
	}
	cfg, _ := obj["config"].(map[string]interface{})
	if cfg == nil {
		return
	}
	if u := jsonFieldString(cfg, "User"); u != "" {
		user = u
	}
	labelsCount = fmt.Sprintf("%d", jsonMapCount(cfg, "Labels"))
	envCount = fmt.Sprintf("%d", jsonArrayCount(cfg, "Env"))
	if wd := jsonFieldString(cfg, "WorkingDir"); wd != "" {
		workdir = wd
	}
	entry := jsonArrayString(cfg, "Entrypoint")
	if entry != "" {
		runCmd = entry
		return
	}
	cmd := jsonArrayString(cfg, "Cmd")
	if cmd != "" {
		runCmd = cmd
	}
	return
}

func extractAnnotations(path string) map[string]string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	obj, err := parseJSONFile(path)
	if err != nil {
		return nil
	}
	annotations, ok := obj["annotations"].(map[string]interface{})
	if !ok || len(annotations) == 0 {
		return nil
	}
	result := make(map[string]string)
	for k, v := range annotations {
		if s, ok := v.(string); ok {
			result[k] = s
		}
	}
	return result
}

// extractIndexAnnotations parses an OCI image index JSON and returns a map of
// digest → annotations for each entry in manifests[]. Per the OCI spec,
// per-platform annotations are stored in the index, not in the manifest blob.
func extractIndexAnnotations(indexPath string) map[string]map[string]string {
	if strings.TrimSpace(indexPath) == "" {
		return nil
	}
	obj, err := parseJSONFile(indexPath)
	if err != nil {
		return nil
	}
	entries, ok := obj["manifests"].([]interface{})
	if !ok || len(entries) == 0 {
		return nil
	}
	result := make(map[string]map[string]string)
	for _, entry := range entries {
		em, _ := entry.(map[string]interface{})
		if em == nil {
			continue
		}
		digest, _ := em["digest"].(string)
		if digest == "" {
			continue
		}
		annotations, _ := em["annotations"].(map[string]interface{})
		if len(annotations) == 0 {
			continue
		}
		m := make(map[string]string)
		for k, v := range annotations {
			if s, ok := v.(string); ok {
				m[k] = s
			}
		}
		result[digest] = m
	}
	return result
}

func parseJSONFile(path string) (map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var obj map[string]interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	return obj, nil
}

// loadConfigHistory reads the OCI image config blob at configPath and returns
// the created_by strings for layers in order, skipping empty_layer entries.
//
// OCI config history rules:
//   - history[] may be longer than rootfs.diff_ids[]
//   - entries where "empty_layer":true do not correspond to any diff_id
//   - the i-th non-empty history entry (0-based) matches ImageLayer.Index==i
func loadConfigHistory(configPath string) []string {
	if strings.TrimSpace(configPath) == "" {
		return nil
	}
	obj, err := parseJSONFile(configPath)
	if err != nil {
		return nil
	}
	historyRaw, ok := obj["history"].([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(historyRaw))
	for _, entry := range historyRaw {
		em, _ := entry.(map[string]interface{})
		if em == nil {
			continue
		}
		if emptyLayer, _ := em["empty_layer"].(bool); emptyLayer {
			continue
		}
		createdBy, _ := em["created_by"].(string)
		out = append(out, createdBy)
	}
	return out
}

func parsedSummaryRows(obj map[string]interface{}) []string {
	rows := make([]string, 0)
	schemaVersion := jsonFieldString(obj, "schemaVersion")
	if schemaVersion != "" {
		rows = append(rows, gridKV("schemaVersion", schemaVersion))
	}
	mediaType := jsonFieldString(obj, "mediaType")
	if mediaType != "" {
		rows = append(rows, gridKV("mediaType", mediaType))
	}
	if manifests, ok := obj["manifests"].([]interface{}); ok {
		rows = append(rows, gridKV("manifests", fmt.Sprintf("%d", len(manifests))))
	}
	if layers, ok := obj["layers"].([]interface{}); ok {
		rows = append(rows, gridKV("layers", fmt.Sprintf("%d", len(layers))))
		var total float64
		for _, layer := range layers {
			lm, _ := layer.(map[string]interface{})
			if lm == nil {
				continue
			}
			size, _ := lm["size"].(float64)
			total += size
		}
		if total > 0 {
			rows = append(rows, gridKV("layer size", formatBytes(int64(total))))
		}
	}
	if arch := jsonFieldString(obj, "architecture"); arch != "" {
		rows = append(rows, gridKV("architecture", arch))
	}
	if osName := jsonFieldString(obj, "os"); osName != "" {
		rows = append(rows, gridKV("os", osName))
	}
	if cfg, ok := obj["config"].(map[string]interface{}); ok {
		if user := jsonFieldString(cfg, "User"); user != "" {
			rows = append(rows, gridKV("User", user))
		}
		if wd := jsonFieldString(cfg, "WorkingDir"); wd != "" {
			rows = append(rows, gridKV("WorkingDir", wd))
		}
		if entry := jsonArrayString(cfg, "Entrypoint"); entry != "" {
			rows = append(rows, gridKV("Entrypoint", entry))
		}
		if cmd := jsonArrayString(cfg, "Cmd"); cmd != "" {
			rows = append(rows, gridKV("Cmd", cmd))
		}
		if envCount := jsonArrayCount(cfg, "Env"); envCount > 0 {
			rows = append(rows, gridKV("Env", fmt.Sprintf("%d vars", envCount)))
		}
		if labels := jsonMapCount(cfg, "Labels"); labels > 0 {
			rows = append(rows, gridKV("Labels", fmt.Sprintf("%d", labels)))
		}
	}
	if len(rows) == 0 {
		rows = append(rows, components.Muted("No commonly recognized fields"))
	}
	return rows
}

func jsonFieldString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	switch value := v.(type) {
	case string:
		return value
	case float64:
		return fmt.Sprintf("%.0f", value)
	default:
		return ""
	}
}

func jsonArrayString(m map[string]interface{}, key string) string {
	if m == nil {
		return ""
	}
	arr, ok := m[key].([]interface{})
	if !ok || len(arr) == 0 {
		return ""
	}
	parts := make([]string, 0, len(arr))
	for _, item := range arr {
		if s, ok := item.(string); ok && s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, " ")
}

func jsonArrayCount(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	arr, ok := m[key].([]interface{})
	if !ok {
		return 0
	}
	return len(arr)
}

func jsonMapCount(m map[string]interface{}, key string) int {
	if m == nil {
		return 0
	}
	obj, ok := m[key].(map[string]interface{})
	if !ok {
		return 0
	}
	return len(obj)
}

func parsedManifestHighlights(path string, isCurrent bool) []string {
	if strings.TrimSpace(path) == "" {
		return []string{imageMissingPathMarker}
	}
	obj, err := parseJSONFile(path)
	if err != nil {
		return []string{fmt.Sprintf("[%s::b]parse error[-:-:-] %s", components.ColorName(components.ColorFgError), err.Error())}
	}
	rows := parsedSummaryRows(obj)
	if isCurrent {
		rows = append(rows, components.Muted("(current platform manifest)"))
	}
	return rows
}
