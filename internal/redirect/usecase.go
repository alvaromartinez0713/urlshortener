package redirect

import (
	"context"
	"strings"

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
// can exist is exactly the set of internal/shorturl.Base62Encode outputs
// (S3): non-empty strings over [0-9a-zA-Z]. A code outside that alphabet
// can never have a row, so it is answered as ErrNotFound WITHOUT calling
// the port — a correct fast path that keeps garbage probes off the
// database, and a use-case decision (S5.3), not an adapter one. The
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
	if !isPossibleCode(code) {
		return shorturl.ShortURL{}, ErrNotFound
	}
	return uc.finder.FindByCode(ctx, code)
}

// base62Alphabet mirrors the domain's code alphabet
// (internal/shorturl/base62.go, S3). It is stated here — rather than
// exported from the domain — because the redirect slice's rule is "could
// this string ever have been produced as a code?", which is this slice's
// knowledge, not the domain's. It is load-bearing: isPossibleCode iterates
// it directly. TestRedirectPossibleCodesExactlyMatchBase62Outputs
// cross-checks the two against each other so they can never drift.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// isPossibleCode reports whether code could have been produced by
// shorturl.Base62Encode: non-empty and entirely over base62Alphabet.
func isPossibleCode(code string) bool {
	if code == "" {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(base62Alphabet, rune(code[i])) {
			return false
		}
	}
	return true
}
