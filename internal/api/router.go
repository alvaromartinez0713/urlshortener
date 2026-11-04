// Package api owns the HTTP adapter: Gin wiring, middleware, and the
// mounting point for vertical slices. It contains no business logic —
// handlers live in their own slice packages and are registered here.
package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/httpx"
	"urlshortener/internal/redirect"
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

// Mounting point for vertical slices (T4/T5): the composition root
// registers each slice's handler on the engine returned by NewRouter.
// The api package owns all route registration; the composition root only
// constructs the slice's handler and hands it over. No business logic
// here — each handler owns that boundary.
func MountShorten(router *gin.Engine, h *shorten.Handler) {
	router.POST("/shorten", h.Shorten)
}

// MountRedirect registers the redirect slice's route on the engine (T5):
// GET /:code. Static siblings in the GET tree ("/healthz") and the POST
// "/shorten" route coexist with the :code param segment in modern Gin's
// routing tree.
func MountRedirect(router *gin.Engine, h *redirect.Handler) {
	router.GET("/:code", h.Redirect)
}

func handleHealthz(c *gin.Context) {
	// Deliberately touches no dependency (not even the database):
	// liveness of the process, not readiness of its dependencies.
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// handleProblemNotFound answers unknown routes with an RFC 9457
// problem details document rendered by the shared httpx builder (S8/S9).
func handleProblemNotFound(c *gin.Context) {
	httpx.WriteProblem(c, httpx.Problem{
		Status: http.StatusNotFound,
		Type:   httpx.TypeURI("route-not-found"),
		Detail: "the requested route does not exist",
	})
}
