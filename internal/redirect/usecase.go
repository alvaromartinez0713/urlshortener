package redirect

import (
	"context"

	"urlshortener/internal/shorturl"
)

// UseCase resolves short codes to their stored targets. It depends only
// on the core-owned Finder port (S5.2) and imports no framework — no Gin,
// no pgx, no SQL (S5.1). It makes no HTTP decision; status codes are the
// handler's business (S5.3).
type UseCase struct {
	finder Finder
}

// NewUseCase constructs the use case over its read port.
func NewUseCase(finder Finder) *UseCase {
	return &UseCase{finder: finder}
}

// Redirect resolves code to the long URL it redirects to (S2).
//
// Flow: a cheap possibility check, then the port. The set of codes that
// can exist is exactly the set of shorturl.Base62Encode outputs (S3); the
// DOMAIN owns that rule as shorturl.IsPossibleCode (S16) — the use case
// consumes the domain predicate instead of duplicating the alphabet. A
// code outside the alphabet can never have a row, so it is answered as
// ErrNotFound WITHOUT calling the port — a correct fast path that keeps
// garbage probes off the database, and a use-case decision (S5.3), not an
// adapter one. The
// consistency of this rule with the domain's alphabet is pinned by
// TestRedirectPossibleCodesExactlyMatchBase62Outputs, which cross-checks
// against shorturl.Base62Encode.
//
// The empty code is covered by the same rule and is deliberately NOT a
// distinct error: it can never be stored (Base62Encode never returns
// empty), so it is semantically a nonexistent code. A separate
// ErrEmptyCode would only exist to be mapped to the same 404. (In
// practice the mounted route GET /:code never even delivers an empty
// code — Gin matches "/" to no route and the api package's NoRoute
// handler answers 404 — but the use case guards it anyway so the
// invariant holds independently of routing.)
//
// Returned errors, matched via errors.Is:
//
//   - ErrNotFound — no row matches the code (404);
//   - anything else — infrastructure failure from the port, propagated
//     as-is (500).
//
// On success it returns the rehydrated ShortURL entity (S11): the read
// port speaks in domain entities, not raw strings — persistence never
// leaks past the domain. On any error the returned entity is the zero
// value.
func (uc *UseCase) Redirect(ctx context.Context, code string) (shorturl.ShortURL, error) {
	if !shorturl.IsPossibleCode(code) {
		return shorturl.ShortURL{}, ErrNotFound
	}
	return uc.finder.FindByCode(ctx, code)
}
