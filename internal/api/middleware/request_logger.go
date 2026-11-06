package middleware

import (
	"log/slog"
	"time"

	"github.com/gin-gonic/gin"
)

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
