// Package redirect is the "redirect" vertical slice: use case + Gin
// handler (T5). The use case imports only the stdlib (S5.1); the handler
// is the HTTP adapter of this slice. It deliberately does NOT import
// internal/shorten: slices are independent boundaries (S5.4), so the
// read-side port and its errors are owned here.
package redirect

import (
	"context"
	"errors"
)

// Finder is the read port of the redirect slice. It is owned by the
// consumer (S5.2: every external interaction crosses a core-owned port)
// and implemented by the internal/platform/postgres adapter, which is a
// dumb translator (S5.3): it runs the lookup and translates storage
// outcomes — it decides nothing.
//
// FindByCode returns the long URL stored for code, or:
//
//   - ErrNotFound when no row matches the code (mapped by the adapter
//     from pgx.ErrNoRows);
//   - any other infrastructure error as produced by the adapter (the
//     slice does not invent a taxonomy for storage outages).
//
// The port returns the raw stored string, not a domain entity: reading is
// a lookup, and the stored target was already validated and normalized by
// the shorten slice's rich domain when it was persisted (S7). Re-wrapping
// it here would add nothing the data model (S6: long_url text) does not
// hold.
type Finder interface {
	FindByCode(ctx context.Context, code string) (string, error)
}

// ErrNotFound is returned by Finder.FindByCode (and surfaced by the use
// case) when the code matches no row. Matched with errors.Is, never by
// string comparison, so the handler's 404 mapping (S2/S8) stays
// deterministic. It lives next to the port it crosses (S5.2: errors as
// domain values).
var ErrNotFound = errors.New("redirect: code not found")
