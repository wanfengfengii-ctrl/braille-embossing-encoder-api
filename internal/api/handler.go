// Package api wires the HTTP layer of the braille prepress encoder.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"braille-api/internal/braille"
)

type encodeRequest struct {
	Text string `json:"text"`
}

// NewRouter builds the Gin engine with all routes registered.
func NewRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())
	r.GET("/healthz", healthz)
	r.POST("/encode", encode)
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
