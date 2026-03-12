package components

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Column defines a table column
type Column struct {
	Title  string
	Width  int // 0 means auto-expand
	Align  int // tview.AlignLeft, AlignCenter, AlignRight
	Hidden bool
}

// Table is a reusable table component with header styling and row selection
type Table struct {
	*tview.Table

	columns     []Column
	onSelect    func(row int)
	headerStyle tcell.Style
}

// NewTable creates a new reusable table component
func NewTable(columns []Column) *Table {
	t := &Table{
		Table:       tview.NewTable(),
		columns:     columns,
		headerStyle: tcell.StyleDefault.Bold(true).Foreground(tcell.ColorYellow),
	}

	t.SetSelectable(true, false)
	t.SetFixed(1, 0) // Fix header row
	t.SetBorder(false)
	t.SetBorders(false)
	t.Select(1, 0) // Start selection at first data row

	// Use a muted background color for selection to avoid white-on-white
	t.SetSelectedStyle(tcell.StyleDefault.
		Background(tcell.ColorDarkSlateGray).
		Foreground(tcell.ColorWhite))

	t.renderHeader()

	return t
}

// SetSelectedFunc sets the callback when a row is selected (Enter key)
func (t *Table) SetSelectedFunc(handler func(row int)) *Table {
	t.onSelect = handler
	t.Table.SetSelectedFunc(func(row, column int) {
		if row > 0 && t.onSelect != nil {
			t.onSelect(row - 1) // Convert to 0-based data index
		}
	})
	return t
}

// renderHeader renders the header row
func (t *Table) renderHeader() {
	for col, c := range t.columns {
		if c.Hidden {
			continue
		}
		cell := tview.NewTableCell(c.Title).
			SetSelectable(false).
			SetStyle(t.headerStyle).
			SetAlign(c.Align)
		if c.Width > 0 {
			cell.SetMaxWidth(c.Width)
		} else {
			cell.SetExpansion(1)
		}
		t.SetCell(0, col, cell)
	}
}

// Clear removes all data rows but keeps the header
func (t *Table) ClearData() {
	rowCount := t.GetRowCount()
	for row := rowCount - 1; row >= 1; row-- {
		t.RemoveRow(row)
	}
}

// AddRow adds a row of data to the table
func (t *Table) AddRow(values ...string) {
	row := t.GetRowCount()
	for col, val := range values {
		if col >= len(t.columns) {
			break
		}
		c := t.columns[col]
		if c.Hidden {
			continue
		}
		cell := tview.NewTableCell(val).
			SetAlign(c.Align)
		if c.Width > 0 {
			cell.SetMaxWidth(c.Width)
		} else {
			cell.SetExpansion(1)
		}
		t.SetCell(row, col, cell)
	}
}

// SetRowColor sets the text color for a specific data row
func (t *Table) SetRowColor(dataRow int, color tcell.Color) {
	row := dataRow + 1 // Account for header
	colCount := t.GetColumnCount()
	for col := 0; col < colCount; col++ {
		cell := t.GetCell(row, col)
		if cell != nil {
			cell.SetTextColor(color)
		}
	}
}

// GetColumns returns the current column definitions
func (t *Table) GetColumns() []Column {
	return t.columns
}

// SetColumns updates column definitions and re-renders the header
func (t *Table) SetColumns(columns []Column) {
	t.columns = columns
	t.renderHeader()
}

// DataRowCount returns the number of data rows (excluding header)
func (t *Table) DataRowCount() int {
	count := t.GetRowCount() - 1
	if count < 0 {
		return 0
	}
	return count
}
