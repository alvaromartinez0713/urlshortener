// Package middleware holds the api layer's cross-cutting HTTP middleware
// (S13): the error handler — the SINGLE place that renders error responses
// and logs them — and the slog request logger that replaced gin.Logger.
//
// It lives in its own subpackage of internal/api, not in the api package
// itself, so slice-internal tests (package shorten / package redirect)
// can wire the REAL middleware into their handler-test routers without an
// import cycle (api imports the slices; the slices' tests import this
// package, which imports nothing slice-owned).
package middleware

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/httpx"
)

// The generic 500 problem for plain (non-Problem) errors is httpx's
// canonical shape (S17): their provenance is unknown, so nothing but the
// generic document may reach the user (S13).

// ErrorHandler is the centralized error middleware (S13, ADR-003). After
// the handler chain returns it inspects gin's attached errors (c.Error):
//
//   - httpx.ProblemError: the slice already mapped the failure to a
//     user-safe problem. Render that problem (if no response was written
//     yet) and log: WARN for client-class failures (status < 500), ERROR
//     for infrastructure failures (status >= 500), always with method,
//     path, status, the mapped detail and the FULL cause chain.
//   - any other error: provenance unknown — log ERROR with the full
//     chain + request context and render the generic 500 problem. The
//     internal text never reaches the user.
//   - no error attached: do nothing (the request logger owns access logs).
//
// A nil logger falls back to slog.Default().
func ErrorHandler(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		c.Next()

		attached := c.Errors.Last()
		if attached == nil {
			return
		}
		err := attached.Err

		var pe *httpx.ProblemError
		if errors.As(err, &pe) {
			logFailure(c, logger, pe.Problem.Status, err)
			// Never append a body to an already-written response
			// (e.g. an error attached after c.Redirect).
			if !c.Writer.Written() {
				httpx.WriteProblem(c, pe.Problem)
			}
			return
		}

		// Plain error of unknown provenance: the canonical generic 500
		// for the user (S17), full chain for the team (S13).
		logFailure(c, logger, http.StatusInternalServerError, err)
		if !c.Writer.Written() {
			httpx.WriteProblem(c, httpx.GenericInternalProblem())
		}
	}
}

// logFailure emits one structured record for a failed request. The level
// encodes the S13 taxonomy: WARN for client-class (4xx) failures, ERROR
// for infrastructure (5xx) ones and for errors of unknown provenance.
// Attrs: method, path, status, the mapped (user-safe) detail and the full
// cause chain — everything the team needs, nothing the user gets.
func logFailure(c *gin.Context, logger *slog.Logger, status int, err error) {
	level := slog.LevelError
	if status < 500 {
		level = slog.LevelWarn
	}
	logger.LogAttrs(c.Request.Context(), level, "request error",
		slog.String("method", c.Request.Method),
		slog.String("path", c.Request.URL.Path),
		slog.Int("status", status),
		slog.String("error", err.Error()),
		slog.String("chain", causeChain(err)),
	)
}

// causeChain unwraps err layer by layer and joins every message, so the
// log shows the whole chain (ProblemError detail -> cause -> ...). fmt's
// %v would print only the outermost message, losing the middle layers.
func causeChain(err error) string {
	var b strings.Builder
	for err != nil {
		if b.Len() > 0 {
			b.WriteString(": ")
		}
		b.WriteString(err.Error())
		err = errors.Unwrap(err)
	}
	return b.String()
}

// RequestLogger emits one INFO access line per request (method, path,
// status, latency, client IP) via slog, replacing gin.Logger (S13). It is
// registered INNERMOST (after ErrorHandler) so its latency measures the
// application chain, and its access line is written before the error
// line of the same request — chronological correlation in the output.
// A nil logger falls back to slog.Default().
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		logger.LogAttrs(c.Request.Context(), slog.LevelInfo, "request",
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("client_ip", c.ClientIP()),
		)
	}
}
