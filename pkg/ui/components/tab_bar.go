package components

import (
	"fmt"
	"strings"

	"github.com/rivo/tview"
)

// TabDef defines a single tab entry.
type TabDef struct {
	Label string
	Key   string
	Badge string // optional badge text (e.g. count)
}

// TabBar is a reusable segmented tab bar component.
type TabBar struct {
	*tview.TextView
}

// NewTabBar creates a styled tab bar.
func NewTabBar() *TabBar {
	tv := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	tv.SetBackgroundColor(ColorBgTabBar)
	return &TabBar{TextView: tv}
}

// Update renders the tab strip with the given tabs, highlighting activeIdx.
func (tb *TabBar) Update(tabs []TabDef, activeIdx int) {
	var b strings.Builder
	b.WriteString(" ")
	for i, t := range tabs {
		if i == activeIdx {
			// Active pill: bright text on accent bg
			badge := ""
			if t.Badge != "" {
				badge = " " + t.Badge
			}
			b.WriteString(fmt.Sprintf("[#ffffff:%s:b] %s", ColorName(ColorBgTabOn), t.Label))
			if t.Key != "" {
				b.WriteString(fmt.Sprintf("(%s)", t.Key))
			}
			b.WriteString(fmt.Sprintf("%s [-:-:-] ", badge))
		} else {
			// Inactive pill: muted text on dim bg
			badge := ""
			if t.Badge != "" {
				badge = " " + t.Badge
			}
			b.WriteString(fmt.Sprintf("[%s:%s] %s", ColorName(ColorFg), ColorName(ColorBgTabOff), t.Label))
			if t.Key != "" {
				b.WriteString(fmt.Sprintf("(%s)", t.Key))
			}
			b.WriteString(fmt.Sprintf("%s [-:-] ", badge))
		}
	}
	tb.SetText(b.String())
}

// Footer is a reusable context-sensitive footer for key hints.
type Footer struct {
	*tview.TextView
}

// NewFooter creates a styled footer bar.
func NewFooter() *Footer {
	tv := tview.NewTextView().SetDynamicColors(true).SetTextAlign(tview.AlignLeft)
	tv.SetBackgroundColor(ColorBgFooter)
	return &Footer{TextView: tv}
}

// Update renders the footer with left-aligned context keys and right info.
func (f *Footer) Update(hints []FooterHint) {
	var parts []string
	for _, h := range hints {
		parts = append(parts, KeyHint(h.Key, h.Action))
	}
	f.SetText(" " + strings.Join(parts, "  "))
}

// FooterHint represents a single key→action pair displayed in the footer.
type FooterHint struct {
	Key    string
	Action string
}
