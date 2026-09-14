package braille

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func mustEncode(t *testing.T, text string) []Item {
	t.Helper()
	items, err := Encode(text)
	if err != nil {
		t.Fatalf("Encode(%q) returned error: %+v", text, err)
	}
	return items
}

func TestLetterMasksMatchSpec(t *testing.T) {
	want := []int{1, 3, 9, 25, 17, 11, 27, 19, 10, 26, 5, 7, 13, 29, 21, 15, 31, 23, 14, 30, 37, 39, 58, 45, 61, 53}
	items := mustEncode(t, "ABCDEFGHIJKLMNOPQRSTUVWXYZ")
	if len(items) != 26 {
		t.Fatalf("got %d items, want 26", len(items))
	}
	for i, it := range items {
		if it.SourceIndex != i || it.Source != string(rune('A'+i)) {
			t.Errorf("item %d: got index=%d source=%q", i, it.SourceIndex, it.Source)
		}
		if it.Kind != KindCharacter || it.PrefixType != PrefixNone {
			t.Errorf("item %d: got kind=%s prefix=%s", i, it.Kind, it.PrefixType)
		}
		if !reflect.DeepEqual(it.Cells, []int{want[i]}) {
			t.Errorf("item %d (%s): got cells %v, want [%d]", i, it.Source, it.Cells, want[i])
		}
	}
}

func TestLowercaseGetsCapitalPrefix(t *testing.T) {
	items := mustEncode(t, "aBc")
	want := []Item{
		{SourceIndex: 0, Source: "a", Kind: KindCharacter, PrefixType: PrefixCapital, Cells: []int{32, 1}},
		{SourceIndex: 1, Source: "B", Kind: KindCharacter, PrefixType: PrefixNone, Cells: []int{3}},
		{SourceIndex: 2, Source: "c", Kind: KindCharacter, PrefixType: PrefixCapital, Cells: []int{32, 9}},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got %+v, want %+v", items, want)
	}
}

func TestDigitsReuseLetterMasks(t *testing.T) {
	items := mustEncode(t, "1234567890")
	wantCells := [][]int{{60, 1}, {3}, {9}, {25}, {17}, {11}, {27}, {19}, {10}, {26}}
	for i, it := range items {
		if !reflect.DeepEqual(it.Cells, wantCells[i]) {
			t.Errorf("digit %d: got cells %v, want %v", i, it.Cells, wantCells[i])
		}
		wantPrefix := PrefixNone
		if i == 0 {
			wantPrefix = PrefixNumber
		}
		if it.PrefixType != wantPrefix {
			t.Errorf("digit %d: got prefix %s, want %s", i, it.PrefixType, wantPrefix)
		}
	}
}

// TestNumberSegmentStateSwitching covers the number indicator being
// emitted only at the head of each consecutive digit run and re-armed
// after a space, a newline, or a letter.
func TestNumberSegmentStateSwitching(t *testing.T) {
	items := mustEncode(t, "12 3\n4a5Z6")
	want := []Item{
		{0, "1", KindCharacter, PrefixNumber, []int{60, 1}},
		{1, "2", KindCharacter, PrefixNone, []int{3}},
		{2, " ", KindCharacter, PrefixNone, []int{0}},
		{3, "3", KindCharacter, PrefixNumber, []int{60, 9}},
		{4, "\n", KindNewline, PrefixNone, []int{}},
		{5, "4", KindCharacter, PrefixNumber, []int{60, 25}},
		{6, "a", KindCharacter, PrefixCapital, []int{32, 1}},
		{7, "5", KindCharacter, PrefixNumber, []int{60, 17}},
		{8, "Z", KindCharacter, PrefixNone, []int{53}},
		{9, "6", KindCharacter, PrefixNumber, []int{60, 11}},
	}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got %+v, want %+v", items, want)
	}
}

func TestSpaceIsBlankCell(t *testing.T) {
	items := mustEncode(t, " ")
	want := []Item{{0, " ", KindCharacter, PrefixNone, []int{0}}}
	if !reflect.DeepEqual(items, want) {
		t.Errorf("got %+v, want %+v", items, want)
	}
}

// TestNewlineSerialization pins the unique newline record: kind=newline,
// prefix_type=none, cells serialized as [] (never null, never a mask).
func TestNewlineSerialization(t *testing.T) {
	items := mustEncode(t, "\n")
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	it := items[0]
	if it.Kind != KindNewline || it.PrefixType != PrefixNone {
		t.Errorf("got kind=%s prefix=%s", it.Kind, it.PrefixType)
	}
	if it.Cells == nil || len(it.Cells) != 0 {
		t.Errorf("cells must be an empty non-nil slice, got %v", it.Cells)
	}
	raw, _ := json.Marshal(it)
	if !strings.Contains(string(raw), `"cells":[]`) {
		t.Errorf("newline must serialize cells as [], got %s", raw)
	}
}

func TestEmptyTextRejected(t *testing.T) {
	items, err := Encode("")
	if items != nil {
		t.Errorf("expected nil items on error, got %v", items)
	}
	if err == nil || err.Code != CodeEmptyText {
		t.Errorf("got %+v, want code %s", err, CodeEmptyText)
	}
}

func TestLengthBoundaries(t *testing.T) {
	items, err := Encode(strings.Repeat("a", MaxCodePoints))
	if err != nil || len(items) != MaxCodePoints {
		t.Errorf("2000 code points: got items=%d err=%+v", len(items), err)
	}

	items, err = Encode(strings.Repeat("a", MaxCodePoints+1))
	if items != nil {
		t.Errorf("expected nil items on error, got %d items", len(items))
	}
	if err == nil || err.Code != CodeTooLong {
		t.Fatalf("got %+v, want code %s", err, CodeTooLong)
	}
	if err.Length != MaxCodePoints+1 || err.Limit != MaxCodePoints {
		t.Errorf("got length=%d limit=%d", err.Length, err.Limit)
	}
}

// TestInvalidCharactersRejected ensures the whole input is rejected and
// the first illegal code point index is reported.
func TestInvalidCharactersRejected(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		wantIndex int
		wantRune  string
	}{
		{"hanzi", "ab汉cd", 2, "汉"},
		{"tab", "a\tb", 1, "\t"},
		{"carriage return", "a\rb", 1, "\r"},
		{"punctuation", "ab,cd", 2, ","},
		{"fullwidth digit", "1２3", 1, "２"},
		{"emoji", "ok🙂", 2, "🙂"},
		// The first illegal rune is reported; a multibyte rune after
		// ASCII keeps its code point position.
		{"hanzi after ascii", "abc汉", 3, "汉"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			items, err := Encode(tc.text)
			if items != nil {
				t.Errorf("partial encoding leaked: %v", items)
			}
			if err == nil || err.Code != CodeInvalidChar {
				t.Fatalf("got %+v, want code %s", err, CodeInvalidChar)
			}
			if err.SourceIndex == nil || *err.SourceIndex != tc.wantIndex {
				t.Errorf("got index %v, want %d", err.SourceIndex, tc.wantIndex)
			}
			if err.Source != tc.wantRune {
				t.Errorf("got source %q, want %q", err.Source, tc.wantRune)
			}
		})
	}
}

func TestErrorImplementsError(t *testing.T) {
	var _ error = (*Error)(nil)
}
