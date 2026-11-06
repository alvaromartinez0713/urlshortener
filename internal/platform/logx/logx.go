// Package logx holds slog handler wrappers. Wrap makes a handler
// request-correlation aware (S30/T32): it adds the request id from the
// record's context as a request_id attribute, so every log line of one
// request — access lines, error lines, use-case warnings — shares the
// same id and greps together.
package logx

import (
	"context"
	"log/slog"

	"urlshortener/internal/platform/requestid"
)

// wrap is a slog.Handler decorator that injects the context's request id
// into every record it forwards.
type wrap struct{ inner slog.Handler }

// Wrap returns a handler that adds slog.String("request_id", id) to each
// record whose context carries a request id (requestid.FromContext),
// before delegating to handler. Records without one pass through
// untouched — process-level logs (startup, shutdown) stay uncorrelated.
func Wrap(handler slog.Handler) slog.Handler {
	return wrap{inner: handler}
}

// Enabled delegates to the wrapped handler.
func (w wrap) Enabled(ctx context.Context, level slog.Level) bool {
	return w.inner.Enabled(ctx, level)
}

// Handle injects the request id from ctx (when present) and forwards the
// record. The record is cloned first: records must never be mutated
// beyond the handler that owns them.
func (w wrap) Handle(ctx context.Context, r slog.Record) error {
	injected := false
	if id, ok := requestid.FromContext(ctx); ok {
		r = r.Clone()
		r.AddAttrs(slog.String("request_id", id))
		injected = true
	}
	if clientID, ok := requestid.ClientFromContext(ctx); ok {
		// S31: the client id travels under its OWN attribute — it can
		// never be mistaken for the authoritative internal id.
		if !injected {
			r = r.Clone()
		}
		r.AddAttrs(slog.String("client_request_id", clientID))
	}
	return w.inner.Handle(ctx, r)
}

// WithAttrs delegates to the wrapped handler.
func (w wrap) WithAttrs(attrs []slog.Attr) slog.Handler {
	return wrap{inner: w.inner.WithAttrs(attrs)}
}

// WithGroup delegates to the wrapped handler.
func (w wrap) WithGroup(name string) slog.Handler {
	return wrap{inner: w.inner.WithGroup(name)}
}
