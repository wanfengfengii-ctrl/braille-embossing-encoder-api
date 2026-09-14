// Package braille converts source text into a reviewable stream of
// six-dot braille cells ahead of embossing.
//
// Dot positions 1-6 carry bit weights 1, 2, 4, 8, 16, 32, so a cell is
// encoded as the sum of its raised dots' weights.
package braille

import "fmt"

// Indicator and blank cell masks.
const (
	CapitalPrefix = 32 // dot 6: marks the following letter as lowercase-source capitalized form
	NumberPrefix  = 60 // dots 3,4,5,6: opens a run of digits
	SpaceCell     = 0  // blank cell for the half-width space
)

// MaxCodePoints bounds the accepted input length in Unicode code points.
const MaxCodePoints = 2000

// letterMasks holds the fixed six-dot masks for A-Z.
var letterMasks = [26]int{
	1, 3, 9, 25, 17, 11, 27, 19, 10, 26, // A B C D E F G H I J
	5, 7, 13, 29, 21, 15, 31, 23, 14, 30, // K L M N O P Q R S T
	37, 39, 58, 45, 61, 53, // U V W X Y Z
}

// Kind classifies an output item.
type Kind string

const (
	KindCharacter Kind = "character"
	KindNewline   Kind = "newline"
)

// PrefixType marks which indicator cell, if any, precedes the cells of the
// character itself.
type PrefixType string

const (
	PrefixNone    PrefixType = "none"
	PrefixCapital PrefixType = "capital"
	PrefixNumber  PrefixType = "number"
)

// Item is the output record for a single source character.
type Item struct {
	SourceIndex int        `json:"source_index"`
	Source      string     `json:"source"`
	Kind        Kind       `json:"kind"`
	PrefixType  PrefixType `json:"prefix_type"`
	Cells       []int      `json:"cells"`
}

// Error codes returned by Encode.
const (
	CodeEmptyText   = "empty_text"
	CodeTooLong     = "text_too_long"
	CodeInvalidChar = "invalid_character"
)

// Error describes why an input was rejected. Inputs are rejected as a
// whole: no partial encoding is ever produced alongside an Error.
type Error struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	SourceIndex *int   `json:"source_index,omitempty"`
	Source      string `json:"source,omitempty"`
	Length      int    `json:"length,omitempty"`
	Limit       int    `json:"limit,omitempty"`
}

func (e *Error) Error() string { return e.Message }

// Encode validates text and converts it into one Item per source code
// point. On any validation failure it returns nil items and an *Error
// describing the first problem found.
func Encode(text string) ([]Item, *Error) {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil, &Error{
			Code:    CodeEmptyText,
			Message: "text must contain at least one character",
		}
	}
	if len(runes) > MaxCodePoints {
		return nil, &Error{
			Code:    CodeTooLong,
			Message: fmt.Sprintf("text has %d code points, exceeding the limit of %d", len(runes), MaxCodePoints),
			Length:  len(runes),
			Limit:   MaxCodePoints,
		}
	}
	for i, r := range runes {
		if !isAllowed(r) {
			idx := i
			return nil, &Error{
				Code:        CodeInvalidChar,
				Message:     fmt.Sprintf("illegal character %q (U+%04X) at source_index %d", r, r, i),
				SourceIndex: &idx,
				Source:      string(r),
			}
		}
	}

	items := make([]Item, 0, len(runes))
	inNumber := false
	for i, r := range runes {
		item := Item{
			SourceIndex: i,
			Source:      string(r),
			Kind:        KindCharacter,
			PrefixType:  PrefixNone,
		}
		switch {
		case r == '\n':
			item.Kind = KindNewline
			item.Cells = []int{}
			inNumber = false
		case r == ' ':
			item.Cells = []int{SpaceCell}
			inNumber = false
		case r >= 'A' && r <= 'Z':
			item.Cells = []int{letterMasks[r-'A']}
			inNumber = false
		case r >= 'a' && r <= 'z':
			item.PrefixType = PrefixCapital
			item.Cells = []int{CapitalPrefix, letterMasks[r-'a']}
			inNumber = false
		default: // digit
			if !inNumber {
				item.PrefixType = PrefixNumber
				item.Cells = []int{NumberPrefix, digitMask(r)}
				inNumber = true
			} else {
				item.Cells = []int{digitMask(r)}
			}
		}
		items = append(items, item)
	}
	return items, nil
}

// digitMask maps digits onto letter masks: 1-9 reuse A-I, 0 reuses J.
func digitMask(d rune) int {
	if d == '0' {
		return letterMasks['J'-'A']
	}
	return letterMasks[d-'1']
}

func isAllowed(r rune) bool {
	switch {
	case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		return true
	case r == ' ' || r == '\n':
		return true
	}
	return false
}
