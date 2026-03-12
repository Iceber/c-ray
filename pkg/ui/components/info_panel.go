package components

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// InfoItem represents a key-value pair displayed in the info panel
type InfoItem struct {
	Label string
	Value string
	Color tcell.Color // Color for the value text; 0 means default
}

// InfoSection represents a group of info items under a section title
type InfoSection struct {
	Title string
	Items []InfoItem
}

// InfoPanel is a reusable component for displaying key-value information
type InfoPanel struct {
	*tview.TextView
}

// NewInfoPanel creates a new info panel
func NewInfoPanel() *InfoPanel {
	p := &InfoPanel{
		TextView: tview.NewTextView().
			SetDynamicColors(true).
			SetWordWrap(true),
	}
	p.SetBorder(false)
	return p
}

// SetSections renders multiple sections of info items
func (p *InfoPanel) SetSections(sections []InfoSection) {
	p.Clear()
	text := ""

	for i, section := range sections {
		if i > 0 {
			text += "\n"
		}
		if section.Title != "" {
			text += fmt.Sprintf("[yellow::b]%s[-:-:-]\n", section.Title)
		}
		for _, item := range section.Items {
			valueColor := "white"
			if item.Color != 0 {
				valueColor = colorToTag(item.Color)
			}
			text += fmt.Sprintf("  [gray]%-20s[%s]%s[-]\n", item.Label, valueColor, item.Value)
		}
	}

	p.SetText(text)
}

// SetItems renders a flat list of info items (no sections)
func (p *InfoPanel) SetItems(items []InfoItem) {
	p.SetSections([]InfoSection{{Items: items}})
}

// colorToTag converts a tcell.Color to a tview color tag string
func colorToTag(c tcell.Color) string {
	switch c {
	case tcell.ColorGreen:
		return "green"
	case tcell.ColorRed:
		return "red"
	case tcell.ColorYellow:
		return "yellow"
	case tcell.ColorBlue:
		return "blue"
	case tcell.ColorAqua:
		return "aqua"
	case tcell.ColorDarkCyan:
		return "darkcyan"
	case tcell.ColorGray:
		return "gray"
	default:
		return "white"
	}
}
