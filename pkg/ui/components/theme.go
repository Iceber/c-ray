package components

import (
	"fmt"

	"github.com/gdamore/tcell/v2"
)

// --- Modern dark theme color tokens ---

// Background colors
const (
	ColorBg        = tcell.ColorDefault // Global transparent background
	ColorBgPanel   = tcell.ColorDefault // Panel background
	ColorBgHeader  = tcell.Color235     // #262626 — subtle dark header band
	ColorBgTabBar  = tcell.Color236     // #303030 — tab bar strip
	ColorBgFooter  = tcell.Color235     // #262626 — footer strip
	ColorBgSelect  = tcell.Color238     // #444444 — selected row / focus
	ColorBgTabOn   = tcell.Color30      // teal — active tab pill
	ColorBgTabOff  = tcell.Color237     // #3a3a3a — inactive tab pill
	ColorBgTagRun  = tcell.Color28      // dark green — running tag
	ColorBgTagStop = tcell.Color52      // dark red — exited/error tag
	ColorBgTagWarn = tcell.Color130     // dark orange — warning tag
	ColorBgTagInfo = tcell.Color24      // dark teal — info tag
)

// Foreground colors
const (
	ColorFg          = tcell.Color252   // #d0d0d0 — primary text
	ColorFgMuted     = tcell.Color245   // #8a8a8a — secondary / label
	ColorFgDim       = tcell.Color240   // #585858 — dim hint
	ColorFgBright    = tcell.ColorWhite // #ffffff — emphasis
	ColorFgAccent    = tcell.Color51    // bright cyan — active/focus
	ColorFgAccentAlt = tcell.Color156   // green-yellow — alternate accent
	ColorFgOk        = tcell.Color114   // soft green — running / success
	ColorFgWarn      = tcell.Color214   // orange — warnings
	ColorFgError     = tcell.Color203   // soft red — errors
	ColorFgPaused    = tcell.Color228   // yellow — paused
	ColorFgCreated   = tcell.Color74    // steel blue — created state
	ColorFgBorder    = tcell.Color60    // #5f5f87 slate — borders
	ColorFgSeparator = tcell.Color238   // #444444 — light separator
	ColorFgKey       = tcell.Color223   // warm sand — keyboard shortcut highlight
	ColorFgTag       = tcell.ColorWhite // tag foreground (on colored bg)
)

// Namespace status colors for the Info grid view.
const (
	ColorNsPrivate = tcell.Color114 // #87af87 green — container private namespace
	ColorNsPod     = tcell.Color51  // #00ffff cyan — pod-shared namespace
	ColorNsHost    = tcell.Color214 // #ffaf00 orange — host-shared namespace
)

// Styles
var (
	StyleDefault    = tcell.StyleDefault.Foreground(ColorFg).Background(ColorBg)
	StyleHeader     = tcell.StyleDefault.Foreground(ColorFg).Background(ColorBgHeader)
	StyleTableHead  = tcell.StyleDefault.Foreground(ColorFgAccent).Background(ColorBg).Bold(true)
	StyleSelectRow  = tcell.StyleDefault.Foreground(ColorFgBright).Background(ColorBgSelect)
	StyleTreeGraphs = tcell.StyleDefault.Foreground(ColorFgBorder)
)

// --- tview color tag helpers ---

// Tag returns a tview dynamic color tag string for the given color name, e.g. "[green]".
func Tag(name string) string {
	return "[" + name + "]"
}

// StatusTag renders a fixed-width tag like "[ RUNNING ]" with background color.
func StatusTag(label string, fg, bg tcell.Color) string {
	return fmt.Sprintf("[%s:%s:b] %s [-:-:-]", ColorName(fg), ColorName(bg), label)
}

// KV renders a key-value pair with muted key and bright value.
func KV(key, value string) string {
	return fmt.Sprintf("[%s]%s[-] [%s]%s[-]", ColorName(ColorFgMuted), key, ColorName(ColorFgBright), value)
}

// Muted wraps text in the muted color tag.
func Muted(text string) string {
	return fmt.Sprintf("[%s]%s[-]", ColorName(ColorFgMuted), text)
}

// Bright wraps text in bright white.
func Bright(text string) string {
	return fmt.Sprintf("[%s::b]%s[-:-:-]", ColorName(ColorFgBright), text)
}

// Accent wraps text in accent color.
func Accent(text string) string {
	return fmt.Sprintf("[%s]%s[-]", ColorName(ColorFgAccent), text)
}

// Dim wraps text in dim color.
func Dim(text string) string {
	return fmt.Sprintf("[%s]%s[-]", ColorName(ColorFgDim), text)
}

// KeyHint renders "key:action" with highlighted key.
func KeyHint(key, action string) string {
	return fmt.Sprintf("[%s]%s[-][%s]:%s[-]", ColorName(ColorFgKey), key, ColorName(ColorFg), action)
}

// ColorName returns the tview color tag name for a tcell.Color.
func ColorName(c tcell.Color) string {
	switch c {
	case tcell.ColorWhite:
		return "white"
	case tcell.ColorGreen:
		return "green"
	case tcell.ColorRed:
		return "red"
	case tcell.ColorYellow:
		return "yellow"
	case tcell.ColorAqua:
		return "aqua"
	case tcell.ColorGray:
		return "gray"
	case tcell.ColorDefault:
		return "-"
	default:
		// For 256-color palette, use the numeric form.
		return fmt.Sprintf("#%06x", c.TrueColor().Hex())
	}
}
