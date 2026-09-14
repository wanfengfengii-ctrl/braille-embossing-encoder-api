package braille

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// lineCellSum counts the braille cells a line occupies; newline records
// contribute none.
func lineCellSum(l Line) int {
	n := 0
	for _, it := range l.Items {
		n += len(it.Cells)
	}
	return n
}

// TestLayoutDoubleCellCharacterBoundaryWrap pins the critical wrap: a
// two-cell character (indicator + body) that would straddle the line end
// moves whole to the next line, while one that fits exactly stays.
func TestLayoutDoubleCellCharacterBoundaryWrap(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
		want  []Line
	}{
		{
			name:  "capital letter wraps unsplit at width 2",
			text:  "Ab",
			width: 2,
			want: []Line{
				{0, []Item{{0, "A", KindCharacter, PrefixNone, []int{1}}}},
				{1, []Item{{1, "b", KindCharacter, PrefixCapital, []int{32, 3}}}},
			},
		},
		{
			name:  "widest character fits exactly at minimum width",
			text:  "a",
			width: MinCellsPerLine,
			want: []Line{
				{0, []Item{{0, "a", KindCharacter, PrefixCapital, []int{32, 1}}}},
			},
		},
		{
			name:  "two double-cell characters fill one line exactly",
			text:  "ab",
			width: 4,
			want: []Line{
				{0, []Item{
					{0, "a", KindCharacter, PrefixCapital, []int{32, 1}},
					{1, "b", KindCharacter, PrefixCapital, []int{32, 3}},
				}},
			},
		},
		{
			name:  "one cell short forces the wrap",
			text:  "ab",
			width: 3,
			want: []Line{
				{0, []Item{{0, "a", KindCharacter, PrefixCapital, []int{32, 1}}}},
				{1, []Item{{1, "b", KindCharacter, PrefixCapital, []int{32, 3}}}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Layout(mustEncode(t, tc.text), tc.width)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestLayoutNumberSegmentAcrossAutoLines ensures an automatic wrap never
// rewrites a number segment: a digit continuing on the next line keeps its
// original record (no fresh number indicator, same source_index).
func TestLayoutNumberSegmentAcrossAutoLines(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		width int
		want  []Line
	}{
		{
			name:  "indicator fills the line, body digit continues on the next",
			text:  "12",
			width: 2,
			want: []Line{
				{0, []Item{{0, "1", KindCharacter, PrefixNumber, []int{60, 1}}}},
				{1, []Item{{1, "2", KindCharacter, PrefixNone, []int{3}}}},
			},
		},
		{
			name:  "segment splits mid-run without re-indication",
			text:  "123",
			width: 3,
			want: []Line{
				{0, []Item{
					{0, "1", KindCharacter, PrefixNumber, []int{60, 1}},
					{1, "2", KindCharacter, PrefixNone, []int{3}},
				}},
				{1, []Item{{2, "3", KindCharacter, PrefixNone, []int{9}}}},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Layout(mustEncode(t, tc.text), tc.width)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestLayoutExplicitNewlineEndsLine: a newline record is stored as the
// last record of the line it ends, and the next character starts fresh.
func TestLayoutExplicitNewlineEndsLine(t *testing.T) {
	got := Layout(mustEncode(t, "AB\nCD"), 10)
	want := []Line{
		{0, []Item{
			{0, "A", KindCharacter, PrefixNone, []int{1}},
			{1, "B", KindCharacter, PrefixNone, []int{3}},
			{2, "\n", KindNewline, PrefixNone, []int{}},
		}},
		{1, []Item{
			{3, "C", KindCharacter, PrefixNone, []int{9}},
			{4, "D", KindCharacter, PrefixNone, []int{25}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestLayoutConsecutiveNewlinesKeepEmptyLines: each newline ends a line,
// so a run of newlines leaves visible empty lines between content.
func TestLayoutConsecutiveNewlinesKeepEmptyLines(t *testing.T) {
	got := Layout(mustEncode(t, "a\n\nb"), 10)
	want := []Line{
		{0, []Item{
			{0, "a", KindCharacter, PrefixCapital, []int{32, 1}},
			{1, "\n", KindNewline, PrefixNone, []int{}},
		}},
		{1, []Item{
			{2, "\n", KindNewline, PrefixNone, []int{}},
		}},
		{2, []Item{
			{3, "b", KindCharacter, PrefixCapital, []int{32, 3}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if lineCellSum(got[1]) != 0 {
		t.Errorf("middle line must be visibly empty, got %d cells", lineCellSum(got[1]))
	}
}

// TestLayoutTrailingNewlineKeepsEmptyLine: a final newline still opens a
// new (empty) line, and that line serializes its items as [].
func TestLayoutTrailingNewlineKeepsEmptyLine(t *testing.T) {
	got := Layout(mustEncode(t, "a\n"), 10)
	if len(got) != 2 {
		t.Fatalf("got %d lines, want 2: %+v", len(got), got)
	}
	if got[1].LineIndex != 1 || got[1].Items == nil || len(got[1].Items) != 0 {
		t.Errorf("trailing line must be empty but present, got %+v", got[1])
	}
	raw, err := json.Marshal(got[1])
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(raw) != `{"line_index":1,"items":[]}` {
		t.Errorf("empty line must serialize items as [], got %s", raw)
	}
}

// TestLayoutNoTrailingNewlineNoPhantomLine: without a trailing newline the
// last content line is the last line — no phantom empty line is appended.
func TestLayoutNoTrailingNewlineNoPhantomLine(t *testing.T) {
	got := Layout(mustEncode(t, "AB"), 2)
	want := []Line{
		{0, []Item{
			{0, "A", KindCharacter, PrefixNone, []int{1}},
			{1, "B", KindCharacter, PrefixNone, []int{3}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// TestLayoutSpaceOccupiesCell: the blank cell of a space counts toward the
// line width like any other cell.
func TestLayoutSpaceOccupiesCell(t *testing.T) {
	got := Layout(mustEncode(t, "A B"), 2)
	want := []Line{
		{0, []Item{
			{0, "A", KindCharacter, PrefixNone, []int{1}},
			{1, " ", KindCharacter, PrefixNone, []int{0}},
		}},
		{1, []Item{
			{2, "B", KindCharacter, PrefixNone, []int{3}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}

	got = Layout(mustEncode(t, "A B"), 3)
	if len(got) != 1 || len(got[0].Items) != 3 {
		t.Errorf("width 3 must hold the whole text on one line, got %+v", got)
	}
}

// TestLayoutPreservesEncodeRecords is the layout invariant: across every
// line, items concatenate back to the exact Encode output — wrapping never
// rewrites records, drops records, or reorders them — and no line exceeds
// the width.
func TestLayoutPreservesEncodeRecords(t *testing.T) {
	texts := []string{
		"A1b 23\nZz0",
		"12 3\n4a5Z6",
		"a\n\nb\n",
		"ABCDEFGH",
		strings.Repeat("a", 40),
		"1234567890",
	}
	for _, width := range []int{MinCellsPerLine, 3, 5, 80} {
		for _, text := range texts {
			items := mustEncode(t, text)
			lines := Layout(items, width)
			var flat []Item
			for i, l := range lines {
				if l.LineIndex != i {
					t.Fatalf("text %q width %d: line %d has index %d", text, width, i, l.LineIndex)
				}
				if n := lineCellSum(l); n > width {
					t.Fatalf("text %q width %d: line %d holds %d cells", text, width, i, n)
				}
				flat = append(flat, l.Items...)
			}
			if !reflect.DeepEqual(flat, items) {
				t.Fatalf("text %q width %d: flattened lines differ from Encode output", text, width)
			}
		}
	}
}

// TestLayoutNewlineOnlyAsLastItem: a newline record may only ever appear
// as the final record of its line.
func TestLayoutNewlineOnlyAsLastItem(t *testing.T) {
	lines := Layout(mustEncode(t, "a\nb\n\nc"), 10)
	for _, l := range lines {
		for i, it := range l.Items {
			if it.Kind == KindNewline && i != len(l.Items)-1 {
				t.Errorf("line %d: newline record not in last position: %+v", l.LineIndex, l.Items)
			}
		}
	}
}

func TestNewLineWidthError(t *testing.T) {
	err := NewLineWidthError()
	if CodeInvalidLineWidth != "invalid_line_width" {
		t.Errorf("got code constant %q", CodeInvalidLineWidth)
	}
	if err.Code != CodeInvalidLineWidth {
		t.Errorf("got code %q", err.Code)
	}
	if err.Min != MinCellsPerLine || err.Max != MaxCellsPerLine {
		t.Errorf("got range %d-%d, want %d-%d", err.Min, err.Max, MinCellsPerLine, MaxCellsPerLine)
	}
	if !strings.Contains(err.Message, "2") || !strings.Contains(err.Message, "80") {
		t.Errorf("message must state the allowed range, got %q", err.Message)
	}
	raw, mErr := json.Marshal(err)
	if mErr != nil {
		t.Fatalf("marshal: %v", mErr)
	}
	if !strings.Contains(string(raw), `"min":2`) || !strings.Contains(string(raw), `"max":80`) {
		t.Errorf("serialized error must carry the range, got %s", raw)
	}
}
