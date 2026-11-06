package redirect

import (
	"context"
	"log/slog"

	"urlshortener/internal/shorturl"
)

// UseCase resolves short codes to their stored targets. It depends only
// on the core-owned Finder and Cache ports (S5.2) and imports no
// framework — no Gin, no pgx, no SQL (S5.1). It makes no HTTP decision;
// status codes are the handler's business (S5.3).
type UseCase struct {
	finder Finder
	cache  Cache
	// logger is optional (S29): nil stays silent (tests, tools). When
	// present, a cache Get/Put failure — which S27 degrades, never fails
	// — emits one WARN so a degraded cache is visible to the team instead
	// of silently swallowed (R4-002).
	logger *slog.Logger
}

// NewUseCase constructs the use case over its read ports. finder and cache
// are required: finder is the source of truth (the database), cache the
// fast read source in front of it (S26). logger is optional; nil stays
// silent (S29).
func NewUseCase(finder Finder, cache Cache, logger *slog.Logger) *UseCase {
	return &UseCase{finder: finder, cache: cache, logger: logger}
}

// Redirect resolves code to the long URL it redirects to (S2).
//
// Flow: a cheap possibility check, then the cache-first read path (S26):
//
//  1. Cache.Get — a HIT returns the cached entity WITHOUT calling the
//     Finder; the core knows THAT a fast source exists, never HOW it
//     caches.
//  2. On a MISS (or a Get error, S27), the Finder (the source of truth)
//     resolves the code, and the resolved entity is Put into the cache.
//     Only POSITIVE results are cached: a miss for an unknown code is
//     NOT stored — negative caching is out of scope (L20), so every
//     probe of a nonexistent code keeps reaching the Finder.
//  3. Cache resilience (S27): a cache failure must NEVER fail the
//     redirect — a Get error is treated as a miss (fall back to the
//     Finder) and a Put error is ignored. A degraded cache degrades
//     performance, never availability.
//
// The set of codes that can exist is exactly the set of
// shorturl.Base62Encode outputs (S3); the DOMAIN owns that rule as
// shorturl.IsPossibleCode (S16) — the use case consumes the domain
// predicate instead of duplicating the alphabet. A code outside the
// alphabet can never have a row, so it is answered as ErrNotFound
// WITHOUT touching either port — a correct fast path that keeps garbage
// probes off the database and the cache, and a use-case decision (S5.3),
// not an adapter one. The
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
//   - anything else — infrastructure failure from the Finder, propagated
//     as-is (500). Cache errors never reach the caller (S27).
//
// On success it returns the rehydrated ShortURL entity (S11): the read
// ports speak in domain entities, not raw strings — persistence never
// leaks past the domain. On any error the returned entity is the zero
// value.
func (uc *UseCase) Redirect(ctx context.Context, code string) (shorturl.ShortURL, error) {
	if !shorturl.IsPossibleCode(code) {
		return shorturl.ShortURL{}, ErrNotFound
	}

	su, found, err := uc.cache.Get(ctx, code)
	if err == nil && found {
		return su, nil
	}
	if err != nil {
		// Get error → degraded, treated as a miss (S27). S29: the team
		// must SEE the degradation — one WARN, then fall through to the
		// source of truth.
		uc.logDegradation("cache get failed; serving from source of truth", code, err)
	}

	su, err = uc.finder.FindByCode(ctx, code)
	if err != nil {
		return shorturl.ShortURL{}, err
	}

	// Best-effort populate, POSITIVE results only (L20): a Put error is
	// degraded, not fatal (S27), and an ErrNotFound miss was already
	// returned above.
	if putErr := uc.cache.Put(ctx, code, su); putErr != nil {
		uc.logDegradation("cache put failed; continuing without caching", code, putErr)
	}
	return su, nil
}

// logDegradation emits one WARN for a cache failure (S29/R4-002). A nil
// logger stays silent by design.
func (uc *UseCase) logDegradation(msg, code string, err error) {
	if uc.logger == nil {
		return
	}
	uc.logger.LogAttrs(context.Background(), slog.LevelWarn, msg,
		slog.String("code", code),
		slog.String("error", err.Error()),
	)
}
