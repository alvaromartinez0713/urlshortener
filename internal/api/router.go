// Package api owns the HTTP adapter: Gin wiring, middleware, and the
// mounting point for vertical slices. It contains no business logic —
// handlers live in their own slice packages and are registered here.
package api

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/api/middleware"
	"urlshortener/internal/platform/httpx"
	"urlshortener/internal/redirect"
	"urlshortener/internal/shorten"
)

// NewRouter builds the Gin engine with the base middleware stack (S13),
// the health endpoint, and JSON problem-details responses for unknown
// routes. Slice routes are registered on the returned engine by the
// composition root.
//
// The logger is injected by the composition root (JSON slog handler over
// os.Stdout — structured, one record per line, machine-parseable without
// extra dependencies, ADR-003). A nil logger falls back to slog.Default().
//
// Middleware ORDER (S13), outermost first:
//
//  1. gin.Recovery — OUTERMOST panic barrier: a panic is contained and
//     answered with a 500 before any other middleware inspects the
//     request, and it logs the stack itself (Recovery never attaches a
//     c.Error, so the error middleware correctly sees nothing).
//  2. middleware.RequestLogger — access log: registered OUTSIDE the
//     error middleware so its line is written AFTER the error middleware
//     has rendered the response, and therefore records the FINAL status
//     of every request (an error response logged as 200 would make the
//     access log useless for the team).
//  3. middleware.ProblemRenderer — INNERMOST, the SINGLE error choke point:
//     renders every error response and logs every failure (method, path,
//     status, mapped detail, full cause chain) before the access line is
//     written. Being innermost also lets it see every error attached by
//     the handlers it wraps.
func NewRouter(logger *slog.Logger) *gin.Engine {
	router := gin.New()
	router.Use(gin.Recovery(), middleware.RequestLogger(logger), middleware.ProblemRenderer(logger))

	router.GET("/healthz", handleHealthz)

	// S8: unknown routes answer as JSON, never an HTML default page.
	// NoRoute writes its problem directly (there is no error to
	// classify), so the error middleware sees no c.Error and does not
	// double-render.
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
