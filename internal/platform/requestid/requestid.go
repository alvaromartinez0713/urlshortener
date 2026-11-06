// Package requestid owns the request-correlation identity (S30/T32): a
// short random id per request, optionally honored from an incoming
// X-Request-ID header, carried in the request context and injected into
// every slog record by logx.Wrap.
//
// It is a pure platform package: no framework, no HTTP — the middleware
// and handlers translate between headers and this package, so anything
// (a CLI, a gRPC server) can correlate through the same context contract.
package requestid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
)

// maxIncomingLength bounds how much of an attacker-controlled
// X-Request-ID header may be honored: anything longer is rejected and a
// fresh id is generated, so hostile input can never bloat log lines.
const maxIncomingLength = 64

// ctxKey is unexported so only this package can store or read the id —
// other packages cannot collide with (or forge) the context key. The
// client key (S31) is a distinct type for the same reason: the two
// identities must never be mistakable for each other.
type ctxKey struct{}

type clientCtxKey struct{}

// Put returns a copy of ctx carrying the request id.
func Put(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the request id stored by Put, and whether one was
// present. An absent id is the normal case for non-request logging.
func FromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(ctxKey{}).(string)
	return id, ok
}

// PutClient returns a copy of ctx carrying the CLIENT-supplied correlation
// id (S31): a sanitized incoming X-Request-ID, kept under its own key so
// it can never be mistaken for — or collide with — the server-generated
// internal id. Purely informational: for the client's cross-system
// correlation.
func PutClient(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, clientCtxKey{}, id)
}

// ClientFromContext returns the client-supplied correlation id stored by
// PutClient, and whether one was present.
func ClientFromContext(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(clientCtxKey{}).(string)
	return id, ok
}

// NewID returns a short random request id: 8 crypto/rand bytes as 16
// lowercase hex characters — small enough to quote in an incident report,
// with collision odds that make per-request uniqueness a non-issue.
// If crypto/rand is unavailable (effectively never) it returns "unknown",
// mirroring the httpx occurrence-id fallback: correlation degrades to
// path-only instead of failing requests.
func NewID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

// Sanitize reports whether an incoming X-Request-ID value may be honored,
// returning it unchanged on acceptance. A value is honored only if it is
// 1-64 characters over [A-Za-z0-9._-]; ANYTHING else is rejected and the
// caller generates a fresh id — arbitrary attacker bytes are never echoed
// into logs or response headers.
func Sanitize(incoming string) (string, bool) {
	if incoming == "" || len(incoming) > maxIncomingLength {
		return "", false
	}
	for _, r := range incoming {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			return "", false
		}
	}
	return incoming, true
}
