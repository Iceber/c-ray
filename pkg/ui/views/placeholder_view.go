package views

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// PlaceholderView renders a reserved page until the actual implementation lands.
type PlaceholderView struct {
	*tview.Flex

	body      *tview.TextView
	statusBar *tview.TextView
}

// NewPlaceholderView creates a new placeholder content view.
func NewPlaceholderView(title string, description string, hint string) *PlaceholderView {
	v := &PlaceholderView{
		Flex: tview.NewFlex().SetDirection(tview.FlexRow),
	}

	v.body = tview.NewTextView().
		SetDynamicColors(true).
		SetWordWrap(true)
	v.body.SetText(fmt.Sprintf("\n [white::b]%s[-:-:-]\n\n %s\n [gray]%s[-]\n", title, description, hint))

	v.statusBar = tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	v.statusBar.SetBackgroundColor(tcell.ColorBlack)
	v.statusBar.SetText(" [gray]Reserved page[-]")

	v.Flex.AddItem(v.body, 0, 1, true)
	v.Flex.AddItem(v.statusBar, 1, 0, false)
	return v
}

// HandleInput is a no-op for placeholder views.
func (v *PlaceholderView) HandleInput(event *tcell.EventKey) *tcell.EventKey {
	return event
}

// GetFocusPrimitive returns the body text view.
func (v *PlaceholderView) GetFocusPrimitive() tview.Primitive {
	return v.body
}
