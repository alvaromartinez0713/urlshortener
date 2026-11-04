// Package shorten is the "shorten" vertical slice: use case + Gin handler
// (T4). The use case imports only the domain (internal/shorturl) and the
// stdlib (S5.1); the handler is the HTTP adapter of this slice.
package shorten

import (
	"context"
	"errors"

	"urlshortener/internal/shorturl"
)

// Repository is the persistence port of the shorten slice. It is owned by
// the consumer (S5.2: every external interaction crosses a core-owned
// port) and implemented by the internal/platform/postgres adapter, which
// is a dumb translator (S5.3): it stores what it is given and translates
// storage failures into the domain-level errors below — it decides
// nothing.
//
// Save persists the ShortURL aggregate built by the domain. It returns:
//
//   - ErrCodeCollision when the code's unique index rejects the insert
//     (mapped by the adapter from Postgres unique_violation, 23505) — the
//     trigger for the use case's S3 retry;
//   - any other infrastructure error as produced by the adapter (the
//     slice does not invent a taxonomy for storage outages).
//
// The write port intentionally returns no record: the use case already
// holds the entity it built. Read access belongs to the redirect slice
// (T5), which will own its own port.
type Repository interface {
	Save(ctx context.Context, su shorturl.ShortURL) error
}

// Errors of the slice, next to the port they cross (S5.2: errors as
// domain values; matched with errors.Is, never by string).
var (
	// ErrCodeCollision is returned by Repository.Save when the code's
	// unique index rejects the insert. The use case retries with a fresh
	// sequence (S3).
	ErrCodeCollision = errors.New("shorten: code collision")

	// ErrCodeGenerationExhausted is returned by the use case when every
	// bounded retry attempt collided. Reaching it means the keyspace is
	// pathologically saturated; it maps to a 5xx, not a client error.
	ErrCodeGenerationExhausted = errors.New("shorten: could not generate a unique code after the allowed attempts")
)
