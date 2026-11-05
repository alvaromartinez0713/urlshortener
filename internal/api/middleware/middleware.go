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

// ProblemRenderer is the centralized error middleware (S13, ADR-003;
// S18 renames it for what it does: render the attached problems). After
// the handler chain returns it inspects gin's attached errors (c.Error):
//
//   - httpx.ProblemError: the slice already mapped the failure to a
//     user-safe problem. Render that problem (if no response was written
//     yet) and log: WARN for client-class failures (status < 500), ERROR
//     for infrastructure failures (status >= 500), always with method,
//     path, status, the mapped detail and the FULL cause chain.
//   - any other error: provenance unknown — log ERROR with the full
//     chain + request context and render the canonical generic 500
//     problem (S17). The internal text never reaches the user.
//   - EVERY attached error is logged (S18): a request with multiple
//     c.Error attachments produces one failure record each, in
//     attachment order; the rendered response is the LAST attached
//     error's (last-wins, matching gin's c.Errors.Last).
//   - no error attached: do nothing (the request logger owns access logs).
//
// A nil logger falls back to slog.Default().
func ProblemRenderer(logger *slog.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = slog.Default()
	}
	return func(c *gin.Context) {
		c.Next()

		if len(c.Errors) == 0 {
			return
		}
		// S18: every attached error is logged, in attachment order.
		for _, attached := range c.Errors {
			logFailure(c, logger, attached.Err)
		}

		// Render the LAST attached error (last-wins).
		err := c.Errors.Last().Err
		var pe *httpx.ProblemError
		if !errors.As(err, &pe) {
			// Plain error of unknown provenance: the canonical generic
			// 500 for the user (S17), full chain already logged.
			if !c.Writer.Written() {
				httpx.WriteProblem(c, httpx.GenericInternalProblem())
			}
			return
		}
		// Never append a body to an already-written response (e.g. an
		// error attached after c.Redirect).
		if !c.Writer.Written() {
			httpx.WriteProblem(c, pe.Problem)
		}
	}
}

// logFailure emits one structured record for a failed request. The level
// encodes the S13 taxonomy: WARN for client-class (4xx) failures, ERROR
// for infrastructure (5xx) ones and for errors of unknown provenance.
// Attrs: method, path, status, the mapped (user-safe) detail and the full
// cause chain — everything the team needs, nothing the user gets.
func logFailure(c *gin.Context, logger *slog.Logger, err error) {
	status := http.StatusInternalServerError
	var pe *httpx.ProblemError
	if errors.As(err, &pe) {
		status = pe.Problem.Status
	}
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

// causeChain flattens err's full cause tree into one string (S18):
// single-unwrap chains layer by layer, AND errors.Join trees — any error
// implementing Unwrap() []error has every branch walked (depth-first), so
// a joined cause is never truncated. fmt's %v would print only the
// outermost message, losing the rest.
func causeChain(err error) string {
	var msgs []string
	var walk func(error)
	type multiUnwrapper interface{ Unwrap() []error }
	walk = func(e error) {
		if e == nil {
			return
		}
		msgs = append(msgs, e.Error())
		if u, ok := e.(multiUnwrapper); ok {
			for _, branch := range u.Unwrap() {
				walk(branch)
			}
			return
		}
		walk(errors.Unwrap(e))
	}
	walk(err)
	return strings.Join(msgs, ": ")
}

// RequestLogger emits one INFO access line per request (method, path,
// status, latency, client IP) via slog, replacing gin.Logger (S13). It is
// registered INNERMOST (after ProblemRenderer) so its latency measures the
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
