// Package api wires the HTTP layer of the braille prepress encoder.
package api

import (
	"encoding/json"
	"net/http"

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

// NewRouter builds the Gin engine with all routes registered.
func NewRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/healthz", healthz)
	r.POST("/encode", encode)
	r.POST("/layout", layout)
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
