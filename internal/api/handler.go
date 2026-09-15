// Package api wires the HTTP layer of the braille prepress encoder.
package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"braille-api/internal/braille"
)

type encodeRequest struct {
	Text string `json:"text"`
}

// layoutRequest keeps cells_per_line raw so a missing, non-integer, or
// out-of-range width is reported as 422 invalid_line_width instead of
// collapsing into the 400 JSON binding failure.
type layoutRequest struct {
	Text         string          `json:"text"`
	CellsPerLine json.RawMessage `json:"cells_per_line"`
}

// proofreadRequest keeps observed_cells raw so the 422 validation order
// (missing field, overlong array, first out-of-range index) is enforced by
// the domain instead of being decided by the JSON binder. Element type
// errors are still rejected at parse time as 400.
type proofreadRequest struct {
	Text          string          `json:"text"`
	ObservedCells json.RawMessage `json:"observed_cells"`
}

// NewRouter builds the Gin engine with all routes registered.
func NewRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/healthz", healthz)
	r.POST("/encode", encode)
	r.POST("/layout", layout)
	r.POST("/proofread", proofread)
	return r
}

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func encode(c *gin.Context) {
	var req encodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code":    "bad_request",
			"message": `request body must be a JSON object with a string field "text"`,
		}})
		return
	}
	items, encErr := braille.Encode(req.Text)
	if encErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": encErr})
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// layout runs the full encoding first — text problems keep their existing
// 422 errors with no partial layout — then validates the line width and
// packs the records into preview lines.
func layout(c *gin.Context) {
	var req layoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code":    "bad_request",
			"message": `request body must be a JSON object with a string field "text" and an integer field "cells_per_line"`,
		}})
		return
	}
	items, encErr := braille.Encode(req.Text)
	if encErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": encErr})
		return
	}
	width, ok := parseCellsPerLine(req.CellsPerLine)
	if !ok {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": braille.NewLineWidthError()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"lines": braille.Layout(items, width)})
}

// parseCellsPerLine validates the raw cells_per_line value: it must be
// present, a JSON integer, and within [MinCellsPerLine, MaxCellsPerLine].
func parseCellsPerLine(raw json.RawMessage) (int, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var w int
	if err := json.Unmarshal(raw, &w); err != nil {
		return 0, false
	}
	if w < braille.MinCellsPerLine || w > braille.MaxCellsPerLine {
		return 0, false
	}
	return w, true
}

// proofread aligns the encoder-derived expected cell stream (newline
// records skipped) against the device readback. Text is encoded and
// validated first; the readback array is then checked for a missing
// field, overlong input, or an out-of-range index.
func proofread(c *gin.Context) {
	var req proofreadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code":    "bad_request",
			"message": `request body must be a JSON object with a string field "text" and an integer array field "observed_cells"`,
		}})
		return
	}
	// Field-shape errors (observed_cells not an array of integers) make
	// the object itself illegal: 400, before any semantic 422 checks.
	parsed, ok := parseObservedCells(req.ObservedCells)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": gin.H{
			"code":    "bad_request",
			"message": `request body must be a JSON object with a string field "text" and an integer array field "observed_cells"`,
		}})
		return
	}
	result, domainErr := braille.Proofread(req.Text, parsed)
	if domainErr != nil {
		c.JSON(http.StatusUnprocessableEntity, gin.H{"error": domainErr})
		return
	}
	c.JSON(http.StatusOK, result)
}

// parseObservedCells type-checks the raw observed_cells value. An absent
// field or JSON null is reported as a present=false parsed value, which
// the domain rejects as 422 invalid_observed_cells. Any other value must
// be an array of JSON integer literals; a non-array, a non-integer
// element (string, boolean, null, fraction, exponent), or malformed JSON
// yields ok=false so the handler returns 400. An integer literal too
// large for int64 is still an integer (not a type error): it is flagged
// via Overflow and rejected as out-of-range by the domain.
func parseObservedCells(raw json.RawMessage) (braille.ParsedObserved, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return braille.ParsedObserved{}, true
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return braille.ParsedObserved{}, false
	}
	elements, ok := value.([]any)
	if !ok {
		return braille.ParsedObserved{}, false
	}
	parsed := braille.ParsedObserved{
		Present:  true,
		Integers: make([]int64, len(elements)),
		Overflow: make([]bool, len(elements)),
	}
	for i, el := range elements {
		num, isNumber := el.(json.Number)
		if !isNumber || !isIntegerLiteral(num.String()) {
			return braille.ParsedObserved{}, false
		}
		v, err := strconv.ParseInt(num.String(), 10, 64)
		if err != nil {
			parsed.Overflow[i] = true
			continue
		}
		parsed.Integers[i] = v
	}
	return parsed, true
}

// isIntegerLiteral reports whether s has the shape of a JSON integer
// token: an optional minus followed by one or more digits. json.Decoder
// already guarantees a valid number token, so anything failing this
// shape carries a fraction point or exponent and is not an integer.
func isIntegerLiteral(s string) bool {
	if len(s) > 0 && s[0] == '-' {
		s = s[1:]
	}
	if len(s) == 0 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
