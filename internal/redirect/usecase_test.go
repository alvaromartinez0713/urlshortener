package redirect

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"urlshortener/internal/shorturl"
)

// fakeFinder is a deterministic Finder fake: FindByCode returns the
// configured rehydrated entity and error. It holds no call counter —
// interactions are asserted through behavior (what the use case returns
// given what the fake would answer), never through spying on the port
// (ADR-002). Pure stdlib — proves the use case tests run without Docker
// or network (S5.5).
type fakeFinder struct {
	su  shorturl.ShortURL
	err error
}

func (f *fakeFinder) FindByCode(ctx context.Context, code string) (shorturl.ShortURL, error) {
	return f.su, f.err
}

// fakeCache is a deterministic Cache fake (S26): it behaves as a small
// map cache with optionally injected Get/Put failures. It holds no call
// counter — limits are modeled through inputs (getErr/putErr), and
// interactions are asserted through BEHAVIOR (what the use case returns
// and what the cache ends up containing), never by spying on the port
// (ADR-002). Pure stdlib — proves the use case tests run without Docker
// or network (S5.5).
type fakeCache struct {
	entries map[string]shorturl.ShortURL
	getErr  error // returned by Get, regardless of contents
	putErr  error // returned by Put; nothing is stored
}

func (c *fakeCache) Get(ctx context.Context, code string) (shorturl.ShortURL, bool, error) {
	if c.getErr != nil {
		return shorturl.ShortURL{}, false, c.getErr
	}
	su, ok := c.entries[code]
	return su, ok, nil
}

func (c *fakeCache) Put(ctx context.Context, code string, su shorturl.ShortURL) error {
	if c.putErr != nil {
		return c.putErr
	}
	if c.entries == nil {
		c.entries = make(map[string]shorturl.ShortURL)
	}
	c.entries[code] = su
	return nil
}

// mustRehydrate builds a stored entity through the domain's trusted
// rehydration constructor (S11), failing the test if it refuses.
func mustRehydrate(t *testing.T, code, target string) shorturl.ShortURL {
	t.Helper()
	su, err := shorturl.NewShortURLFromRepository(code, target)
	if err != nil {
		t.Fatalf("rehydrate %q -> %q: %v", code, target, err)
	}
	return su
}

func TestRedirectSuccessReturnsStoredEntity(t *testing.T) {
	// S11: the read port returns the ShortURL entity, not a raw string.
	// A code that matches a row resolves to the rehydrated entity whose
	// Target() is the stored long URL exactly as persisted (S2: GET
	// /:code redirects to the target). The cache starts empty (a miss),
	// so this is also the canonical MISS → Finder → Put path (S26).
	want := "https://example.com/a/b?q=1#frag"
	finder := &fakeFinder{su: mustRehydrate(t, "10", want)}
	cache := &fakeCache{}
	uc := NewUseCase(finder, cache, nil)

	su, err := uc.Redirect(context.Background(), "10")

	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != want {
		t.Errorf("Target() = %q, want the stored long URL unchanged", su.Target())
	}
	if su.Code() != "10" {
		t.Errorf("Code() = %q, want the looked-up code", su.Code())
	}
}

func TestRedirectUnknownCodeIsErrNotFound(t *testing.T) {
	// A code that matches no row is ErrNotFound (matched via errors.Is),
	// which the handler maps to 404 (S2).
	finder := &fakeFinder{err: ErrNotFound}
	uc := NewUseCase(finder, &fakeCache{}, nil)

	su, err := uc.Redirect(context.Background(), "zzz")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Redirect error = %v, want errors.Is ErrNotFound", err)
	}
	if su != (shorturl.ShortURL{}) {
		t.Errorf("entity = %+v, want the zero value on failure", su)
	}
}

func TestRedirectInfraErrorPropagates(t *testing.T) {
	// Infrastructure failures from the port propagate as-is (S5.3): the
	// use case must not mask a database outage as a client error.
	infraErr := errors.New("connection refused")
	finder := &fakeFinder{err: infraErr}
	uc := NewUseCase(finder, &fakeCache{}, nil)

	su, err := uc.Redirect(context.Background(), "10")

	if !errors.Is(err, infraErr) {
		t.Fatalf("Redirect error = %v, want errors.Is %v", err, infraErr)
	}
	if su != (shorturl.ShortURL{}) {
		t.Errorf("entity = %+v, want the zero value on failure", su)
	}
}

func TestRedirectImpossibleCodeRejectedWithoutPortCall(t *testing.T) {
	// The set of existing codes is exactly the set of Base62Encode
	// outputs (S3): non-empty strings over [0-9A-Za-z]. A code outside
	// that alphabet can never have a row, so the use case rejects it as
	// ErrNotFound WITHOUT a port call — a correct, cheap fast path that
	// keeps garbage probes off the database. The decision lives in the
	// use case (S5.3); the adapter stays a dumb translator.
	tests := []struct {
		name string
		code string
	}{
		{name: "empty", code: ""},
		{name: "non-base62 char", code: "ab-c"},
		{name: "dot", code: "a.b"},
		{name: "slash", code: "a/b"},
		{name: "unicode", code: "cód"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// "The port is not reached" is asserted by BEHAVIOR, not by a
			// call counter (ADR-002): the fake finder is configured to
			// SUCCEED if it were reached, so only the use case's fast path
			// can produce ErrNotFound for an impossible code.
			finder := &fakeFinder{su: mustRehydrate(t, "10", "PORT-MUST-NOT-BE-REACHED")}
			uc := NewUseCase(finder, &fakeCache{}, nil)

			su, err := uc.Redirect(context.Background(), tc.code)

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Redirect(%q) error = %v, want errors.Is ErrNotFound", tc.code, err)
			}
			if su != (shorturl.ShortURL{}) {
				t.Errorf("entity = %+v, want the zero value on failure", su)
			}
		})
	}
}

func TestRedirectCacheHitServesWithoutFinder(t *testing.T) {
	// S26: on a cache HIT the use case returns the entity WITHOUT calling
	// the Finder. Asserted behaviorally, not with a call counter (ADR-002):
	// the fake Finder is configured to FAIL if it were reached, so only
	// the cache can satisfy the redirect — any Finder touch would surface
	// as the Finder's error.
	want := "https://example.com/hit"
	finder := &fakeFinder{err: errors.New("FINDER MUST NOT BE REACHED ON HIT")}
	cache := &fakeCache{entries: map[string]shorturl.ShortURL{
		"10": mustRehydrate(t, "10", want),
	}}
	uc := NewUseCase(finder, cache, nil)

	su, err := uc.Redirect(context.Background(), "10")

	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != want {
		t.Errorf("Target() = %q, want the cached long URL %q", su.Target(), want)
	}
}

func TestRedirectCacheMissPopulatesCache(t *testing.T) {
	// S26: on a cache MISS the use case resolves through the Finder and
	// then Puts the resolved entity into the cache. Asserted behaviorally:
	// after the call, the cache contains the entity under the code.
	want := "https://example.com/miss-populates"
	finder := &fakeFinder{su: mustRehydrate(t, "10", want)}
	cache := &fakeCache{}
	uc := NewUseCase(finder, cache, nil)

	su, err := uc.Redirect(context.Background(), "10")

	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != want {
		t.Errorf("Target() = %q, want the found long URL %q", su.Target(), want)
	}
	cached, ok := cache.entries["10"]
	if !ok {
		t.Fatalf("cache after miss = %v, want an entry stored under the code", cache.entries)
	}
	if cached != mustRehydrate(t, "10", want) {
		t.Errorf("cache entry = %+v, want the resolved entity", cached)
	}
}

func TestRedirectCacheGetErrorDegradesToFinder(t *testing.T) {
	// S27: a cache Get error is treated as a MISS — the use case falls
	// back to the Finder and the redirect still succeeds. A degraded
	// cache degrades performance, never availability.
	want := "https://example.com/get-error"
	finder := &fakeFinder{su: mustRehydrate(t, "10", want)}
	cache := &fakeCache{getErr: errors.New("cache unavailable")}
	uc := NewUseCase(finder, cache, nil)

	su, err := uc.Redirect(context.Background(), "10")

	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != want {
		t.Errorf("Target() = %q, want the found long URL %q", su.Target(), want)
	}
}

func TestRedirectCachePutErrorDoesNotFailRedirect(t *testing.T) {
	// S27: a cache Put error is ignored — the redirect resolved through
	// the Finder still succeeds. The cache failure must never fail the
	// request; the source of truth remains the database.
	want := "https://example.com/put-error"
	finder := &fakeFinder{su: mustRehydrate(t, "10", want)}
	cache := &fakeCache{putErr: errors.New("cache write failed")}
	uc := NewUseCase(finder, cache, nil)

	su, err := uc.Redirect(context.Background(), "10")

	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != want {
		t.Errorf("Target() = %q, want the found long URL %q", su.Target(), want)
	}
}

func TestRedirectUnknownCodeDoesNotPopulateCache(t *testing.T) {
	// S26 + L20: only POSITIVE results are cached — negative caching is
	// explicitly out of scope. A miss for an unknown code returns
	// ErrNotFound (S2 semantics unchanged) and leaves the cache empty, so
	// every probe of a nonexistent code keeps reaching the Finder.
	finder := &fakeFinder{err: ErrNotFound}
	cache := &fakeCache{}
	uc := NewUseCase(finder, cache, nil)

	su, err := uc.Redirect(context.Background(), "zzz")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Redirect error = %v, want errors.Is ErrNotFound", err)
	}
	if su != (shorturl.ShortURL{}) {
		t.Errorf("entity = %+v, want the zero value on failure", su)
	}
	if len(cache.entries) != 0 {
		t.Errorf("cache after unknown code = %v, want it empty (no negative caching)", cache.entries)
	}
}

// captureHandler is a minimal in-memory slog.Handler so the degradation
// tests can assert the exact WARN records the use case emits (S29).
type captureHandler struct{ records []slog.Record }

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.records = append(h.records, r)
	return nil
}
func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func recordAtLevel(records []slog.Record, level slog.Level) int {
	n := 0
	for _, r := range records {
		if r.Level == level {
			n++
		}
	}
	return n
}

// TestRedirectCacheGetErrorLogsDegradationWarning (S29/R4-002): a Get error
// degrades to the Finder (S27) AND emits exactly one WARN — a degraded
// cache must be visible to the team, not silently swallowed.
func TestRedirectCacheGetErrorLogsDegradationWarning(t *testing.T) {
	h := &captureHandler{}
	finder := &fakeFinder{su: mustRehydrate(t, "10", "https://example.com")}
	cache := &fakeCache{getErr: errors.New("cache down")}
	uc := NewUseCase(finder, cache, slog.New(h))

	su, err := uc.Redirect(context.Background(), "10")
	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != "https://example.com" {
		t.Errorf("Target() = %q, want the Finder's entity (degraded path)", su.Target())
	}
	if got := recordAtLevel(h.records, slog.LevelWarn); got != 1 {
		t.Errorf("WARN records = %d, want exactly 1 for the Get degradation", got)
	}
}

// TestRedirectCachePutErrorLogsDegradationWarning (S29/R4-002): a Put error
// is best-effort ignored (S27) AND emits exactly one WARN.
func TestRedirectCachePutErrorLogsDegradationWarning(t *testing.T) {
	h := &captureHandler{}
	finder := &fakeFinder{su: mustRehydrate(t, "10", "https://example.com")}
	cache := &fakeCache{putErr: errors.New("cache write refused")}
	uc := NewUseCase(finder, cache, slog.New(h))

	if _, err := uc.Redirect(context.Background(), "10"); err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if got := recordAtLevel(h.records, slog.LevelWarn); got != 1 {
		t.Errorf("WARN records = %d, want exactly 1 for the Put degradation", got)
	}
}

// TestRedirectNilLoggerStaysSilent (S29): a nil logger is a legitimate
// wiring choice (tests, tools) — no panic, no logging, behavior unchanged.
func TestRedirectNilLoggerStaysSilent(t *testing.T) {
	finder := &fakeFinder{su: mustRehydrate(t, "10", "https://example.com")}
	cache := &fakeCache{getErr: errors.New("cache down")}
	uc := NewUseCase(finder, cache, nil)

	su, err := uc.Redirect(context.Background(), "10")
	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != "https://example.com" {
		t.Errorf("Target() = %q, want the Finder's entity", su.Target())
	}
}
