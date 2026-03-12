package ui

import (
	"github.com/rivo/tview"
)

// PageName represents a page identifier
type PageName string

const (
	PageMain            PageName = "main"
	PageContainerList   PageName = "container_list"
	PageImageList       PageName = "image_list"
	PagePodList         PageName = "pod_list"
	PageContainerDetail PageName = "container_detail"
)

// Navigator manages page navigation
type Navigator struct {
	pages       *tview.Pages
	app         *tview.Application
	history     []PageName
	pageFocuses map[PageName]tview.Primitive
}

// NewNavigator creates a new navigator
func NewNavigator(app *tview.Application, pages *tview.Pages) *Navigator {
	return &Navigator{
		app:         app,
		pages:       pages,
		history:     make([]PageName, 0),
		pageFocuses: make(map[PageName]tview.Primitive),
	}
}

// RegisterFocus registers a focus primitive for a page
func (n *Navigator) RegisterFocus(page PageName, focus tview.Primitive) {
	n.pageFocuses[page] = focus
}

// NavigateTo switches to a specific page
func (n *Navigator) NavigateTo(page PageName) {
	n.history = append(n.history, page)
	n.pages.SwitchToPage(string(page))

	// Set focus to the registered focus primitive for this page
	if focus, ok := n.pageFocuses[page]; ok && focus != nil {
		n.app.SetFocus(focus)
	}
}

// NavigateToAndFocus switches to a page and sets focus to a specific primitive
func (n *Navigator) NavigateToAndFocus(page PageName, focus tview.Primitive) {
	n.history = append(n.history, page)
	n.pages.SwitchToPage(string(page))
	if focus != nil {
		n.app.SetFocus(focus)
	}
}

// Back navigates to the previous page
func (n *Navigator) Back() bool {
	if len(n.history) <= 1 {
		return false
	}

	// Remove current page
	n.history = n.history[:len(n.history)-1]

	// Get previous page
	prevPage := n.history[len(n.history)-1]
	n.pages.SwitchToPage(string(prevPage))

	return true
}

// CurrentPage returns the current page name
func (n *Navigator) CurrentPage() PageName {
	if len(n.history) == 0 {
		return ""
	}
	return n.history[len(n.history)-1]
}

// ClearHistory clears the navigation history
func (n *Navigator) ClearHistory() {
	n.history = make([]PageName, 0)
}
