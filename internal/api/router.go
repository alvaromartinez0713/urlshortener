// Package api owns the HTTP adapter: Gin wiring, middleware, and the
// mounting point for vertical slices. It contains no business logic —
// handlers live in their own slice packages and are registered here.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/shorten"
)

// NewRouter builds the Gin engine with base middleware, the health
// endpoint, and JSON problem-details responses for unknown routes.
// Slice routes are registered on the returned engine by the
// composition root.
func NewRouter() *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), gin.Logger())

	router.GET("/healthz", handleHealthz)

	// S8: unknown routes answer as JSON, never an HTML default page.
	router.NoRoute(handleProblemNotFound)

	return router
}

// Mounting point for vertical slices (T4/T5): once the shorten and
// redirect slice packages exist, the composition root registers their
// handlers on the engine returned by NewRouter, e.g.:
//
//	router.POST("/shorten", shortenHandler.Shorten)
//	router.GET("/:code", redirectHandler.Redirect)
//
// No stub packages are created for them in this scaffold.

// MountShorten registers the shorten slice's routes on the engine (T4).
// It is the mounting point promised above: the api package owns all route
// registration; the composition root only constructs the slice's handler
// and hands it over. No business logic here — the handler owns that
// boundary.
func MountShorten(router *gin.Engine, h *shorten.Handler) {
	router.POST("/shorten", h.Shorten)
}

func handleHealthz(c *gin.Context) {
	// Deliberately touches no dependency (not even the database):
	// liveness of the process, not readiness of its dependencies.
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleProblemNotFound answers unknown routes with an RFC 9457
// problem details document.
func handleProblemNotFound(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotFound, gin.H{
		"type":   "about:blank",
		"title":  "Not Found",
		"status": http.StatusNotFound,
		"detail": "the requested route does not exist",
	})
}
