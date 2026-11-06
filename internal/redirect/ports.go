// Package redirect is the "redirect" vertical slice: use case + Gin
// handler (T5). The use case imports only the stdlib (S5.1); the handler
// is the HTTP adapter of this slice. It deliberately does NOT import
// internal/shorten: slices are independent boundaries (S5.4), so the
// read-side port and its errors are owned here.
package redirect

import (
	"context"
	"errors"

	"urlshortener/internal/shorturl"
)

// Finder is the read port of the redirect slice. It is owned by the
// consumer (S5.2: every external interaction crosses a core-owned port)
// and implemented by the internal/platform/postgres adapter, which is a
// dumb translator (S5.3): it runs the lookup and translates storage
// outcomes — it decides nothing.
//
// FindByCode returns the ShortURL stored for code, rehydrated from the
// row, or:
//
//   - ErrNotFound when no row matches the code (mapped by the adapter
//     from pgx.ErrNoRows);
//   - any other infrastructure error as produced by the adapter (the
//     slice does not invent a taxonomy for storage outages).
//
// The port returns the ShortURL entity, not a raw string (S11): the
// adapter rehydrates the row through the domain's trusted constructor
// shorturl.NewShortURLFromRepository, so persistence never leaks past
// the domain — the use case and handler speak only in entities. The
// stored values were validated by the shorten slice's rich domain when
// they were persisted (S7); rehydration is trusted and does not
// revalidate. The row's persistence shape stays inside the adapter.
type Finder interface {
	FindByCode(ctx context.Context, code string) (shorturl.ShortURL, error)
}

// Cache is the fast read source of the redirect slice. Like Finder it is
// a consumer-owned port (S5.2: every external interaction crosses a
// core-owned port) implemented by adapters (it.3 ships an in-memory map;
// a later Redis iteration implements the SAME port, L20) — dumb
// translators (S5.3) that decide nothing about when to read or write.
//
// The core knows THAT a fast source exists, never HOW it caches: the use
// case orchestrates Get → on miss, Finder → Put (S26) without knowing the
// eviction policy, TTL semantics or storage technology behind these two
// methods.
//
//   - Get returns the entity stored under code, whether it was found,
//     and any infrastructure error.
//   - Put stores the entity under code. Entities are immutable in v1
//     (S2), so no invalidation exists or is needed (S28).
type Cache interface {
	Get(ctx context.Context, code string) (shorturl.ShortURL, bool, error)
	Put(ctx context.Context, code string, su shorturl.ShortURL) error
}

// ErrNotFound is returned by Finder.FindByCode (and surfaced by the use
// case) when the code matches no row. Matched with errors.Is, never by
// string comparison, so the handler's 404 mapping (S2/S8) stays
// deterministic. It lives next to the port it crosses (S5.2: errors as
// domain values).
var ErrNotFound = errors.New("redirect: code not found")
