package braille

import "fmt"

// Line width bounds accepted by the layout preview, in braille cells. The
// minimum of 2 matches the widest character record — indicator plus body —
// so any single character always fits on a line of its own.
const (
	MinCellsPerLine = 2
	MaxCellsPerLine = 80
)

// CodeInvalidLineWidth rejects a missing, non-integer, or out-of-range
// cells_per_line.
const CodeInvalidLineWidth = "invalid_line_width"

// Line is one row of the layout preview: the records packed onto it, in
// source order. A line may hold zero cells — consecutive or trailing
// newlines keep their visible empty lines.
type Line struct {
	LineIndex int    `json:"line_index"`
	Items     []Item `json:"items"`
}

// NewLineWidthError builds the rejection for a missing, non-integer, or
// out-of-range cells_per_line, stating the allowed range.
func NewLineWidthError() *Error {
	return &Error{
		Code:    CodeInvalidLineWidth,
		Message: fmt.Sprintf("cells_per_line must be an integer between %d and %d", MinCellsPerLine, MaxCellsPerLine),
		Min:     MinCellsPerLine,
		Max:     MaxCellsPerLine,
	}
}

// Layout packs encoded records into lines of at most cellsPerLine cells,
// in source order.
//
// A character's cells — indicator and body — are never split across lines:
// a record that would overflow the current line moves whole to the next.
// An explicit newline record ends the current line immediately and is
// stored as that line's last record; consecutive or trailing newlines
// therefore keep their visible empty lines. Records are reused exactly as
// produced by Encode: automatic wrapping never rewrites number segments,
// capital indicators, or source indexes, and concatenating the items of
// all lines reproduces the Encode output unchanged.
//
// cellsPerLine must lie within [MinCellsPerLine, MaxCellsPerLine]; callers
// validate it before invoking Layout.
func Layout(items []Item, cellsPerLine int) []Line {
	var lines []Line
	current := Line{LineIndex: 0, Items: []Item{}}
	used := 0
	flush := func() {
		lines = append(lines, current)
		current = Line{LineIndex: len(lines), Items: []Item{}}
		used = 0
	}
	for _, item := range items {
		if item.Kind == KindNewline {
			current.Items = append(current.Items, item)
			flush()
			continue
		}
		if n := len(item.Cells); used > 0 && used+n > cellsPerLine {
			flush()
		}
		current.Items = append(current.Items, item)
		used += len(item.Cells)
	}
	flush()
	return lines
}
