package redirect_test

// End-to-end test for the redirect slice (T5): real Gin router, real use
// case, real pgx adapter, REAL Postgres via testcontainers. It verifies
// the full HTTP path of S2's redirect contract (301 + Location, 404 for
// unknown codes, case-sensitive codes) against the same mounted router
// shape as the composition root. Skips gracefully when Docker is
// unavailable.
//
// All e2e tests of this package share ONE Postgres container (S12 fast
// feedback, T11), started lazily and reused; per-test data isolation comes
// from `TRUNCATE urls RESTART IDENTITY` before each test (see
// startE2EPostgres), with tests kept sequential (no t.Parallel). Same
// convention as internal/shorten/e2e_test.go, including its ryuk/wait-
// strategy compensations.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"urlshortener/internal/api"
	"urlshortener/internal/platform/cache"
	"urlshortener/internal/platform/postgres"
	"urlshortener/internal/redirect"
	"urlshortener/internal/shorten"
	"urlshortener/internal/testsupport"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"
)

func TestMain(m *testing.M) {
	code := m.Run()
	testsupport.CloseE2EPostgres()
	os.Exit(code)
}

// startE2EPostgres returns the shared testsupport pool with a clean urls
// table (S24: one scaffolding implementation for all container packages).
func startE2EPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return testsupport.E2EPostgres(t)
}

// newE2ERouter wires the composition root's real dependency chain: ONE
// postgres adapter implementing every core-owned port (shorten.Repository
// + shorturl.CodeGenerator + redirect.Finder) -> use cases -> handlers ->
// api.MountShorten + api.MountRedirect.
//
// The cache is the REAL in-memory adapter (T27), built exactly like the
// composition root does in internal/app.buildRouter — ONE cache.Memory per
// router, injected into redirect.NewUseCase. The TTL is shortened to 5s,
// far longer than any single test needs (S12: no test waits on expiry), so
// the e2e path exercises the same Get→miss→Finder→Put orchestration as
// production with no miss-cache fake in between.
func newE2ERouter(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := startE2EPostgres(t)
	adapter := postgres.New(pool)

	shortenUseCase := shorten.NewUseCase(adapter, adapter)
	redirectUseCase := redirect.NewUseCase(adapter, cache.NewMemory(time.Minute, 10000), slog.Default())

	router := api.NewRouter(slog.Default())
	api.MountShorten(router, shorten.NewHandler(shortenUseCase, "http://localhost:8080"))
	api.MountRedirect(router, redirect.NewHandler(redirectUseCase))
	return router, pool
}

func e2ePost(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	return rec
}

// e2eShortenCode runs the full POST /shorten round trip and returns the
// generated code, so cache tests can arrange data through the public HTTP
// path exactly like a real client.
func e2eShortenCode(t *testing.T, router http.Handler, longURL string) string {
	t.Helper()
	rec := e2ePost(t, router, `{"url":"`+longURL+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /shorten status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if created.Code == "" {
		t.Fatal("body[code] is empty, want a generated code")
	}
	return created.Code
}

// e2eDeleteRow removes the row for code from Postgres DIRECTLY through the
// shared pool — bypassing the application entirely, as T28's proof requires:
// whatever the next redirect returns, its source can no longer be the
// database.
func e2eDeleteRow(t *testing.T, pool *pgxpool.Pool, code string) {
	t.Helper()
	tag, err := pool.Exec(context.Background(), `DELETE FROM urls WHERE code = $1`, code)
	if err != nil {
		t.Fatalf("DELETE FROM urls WHERE code = %q: %v", code, err)
	}
	if tag.RowsAffected() != 1 {
		t.Fatalf("DELETE affected %d rows, want exactly 1 for code %q", tag.RowsAffected(), code)
	}
}

func e2eGet(t *testing.T, router http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec
}

func TestRedirectE2E301ToOriginalLongURL(t *testing.T) {
	// The full S2 round trip: POST /shorten -> GET /:code -> 301 whose
	// Location is the original long URL, byte-for-byte, with query string
	// and fragment intact.
	router, _ := newE2ERouter(t)

	const longURL = "https://example.com/very/long/path?q=1&lang=en#section-2"
	rec := e2ePost(t, router, `{"url":"`+longURL+`"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /shorten status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var created struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if created.Code == "" {
		t.Fatal("body[code] is empty, want a generated code")
	}

	rec = e2eGet(t, router, "/"+created.Code)

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /%s status = %d, want %d (body: %s)", created.Code, rec.Code, http.StatusMovedPermanently, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != longURL {
		t.Errorf("Location = %q, want exactly the original long URL %q", got, longURL)
	}
}

func TestRedirectE2EUnknownCodeIs404ProblemJSON(t *testing.T) {
	router, _ := newE2ERouter(t)

	rec := e2eGet(t, router, "/doesnotexist")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json (S9: complete RFC 9457)", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["status"] != float64(http.StatusNotFound) || body["title"] == "" || body["detail"] == "" {
		t.Errorf("body is not a problem details document: %v", body)
	}
}

func TestRedirectE2ECodeLookupIsCaseSensitive(t *testing.T) {
	// Codes are Postgres text compared with `=` under a case-sensitive
	// collation: "aBc", "abc" and "ABC" are three different lookups. Only
	// the exactly-stored code resolves; the other casings are 404. The
	// fixture row is inserted directly so the casing is fully controlled
	// (generated codes for early sequences are digits, which have no
	// case).
	router, pool := newE2ERouter(t)

	const code = "aBc"
	const longURL = "https://example.com/case"
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO urls (code, long_url) VALUES ($1, $2)`, code, longURL); err != nil {
		t.Fatalf("insert fixture row: %v", err)
	}

	rec := e2eGet(t, router, "/"+code)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /%s status = %d, want %d (body: %s)", code, rec.Code, http.StatusMovedPermanently, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != longURL {
		t.Errorf("Location = %q, want %q", got, longURL)
	}

	for _, other := range []string{"abc", "ABC", "Abc"} {
		rec := e2eGet(t, router, "/"+other)
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET /%s status = %d, want %d (codes are case-sensitive)", other, rec.Code, http.StatusNotFound)
		}
	}
}

// TestRedirectE2ESecondRedirectStillServesAfterRowDeleted is T28's behavioral
// cache proof (S26/S28): a code is redirected once (populating the cache),
// then its row is deleted from Postgres directly. The second redirect of the
// SAME code must still be 301 with the same Location — the only possible
// source is the cache, because the row no longer exists. No internals are
// inspected: only observable HTTP behavior.
func TestRedirectE2ESecondRedirectStillServesAfterRowDeleted(t *testing.T) {
	router, pool := newE2ERouter(t)

	const longURL = "https://example.com/cache-proof"
	code := e2eShortenCode(t, router, longURL)

	// First redirect: the miss path (Finder + cache Put).
	rec := e2eGet(t, router, "/"+code)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /%s status = %d, want %d (body: %s)", code, rec.Code, http.StatusMovedPermanently, rec.Body.String())
	}
	wantLocation := rec.Header().Get("Location")
	if wantLocation != longURL {
		t.Fatalf("Location = %q, want %q", wantLocation, longURL)
	}

	// Destroy the source of truth behind the application's back.
	e2eDeleteRow(t, pool, code)

	// Second redirect: the database is empty, so 301 + same Location can
	// only come from the cache.
	rec = e2eGet(t, router, "/"+code)
	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("GET /%s after DELETE status = %d, want %d served from cache (body: %s)", code, rec.Code, http.StatusMovedPermanently, rec.Body.String())
	}
	if got := rec.Header().Get("Location"); got != wantLocation {
		t.Errorf("Location after DELETE = %q, want the same %q", got, wantLocation)
	}
}

// TestRedirectE2ENeverCachedCodeIs404AfterRowDeleted pins the negative side
// of the same contract: a code that was WRITTEN but never READ (no prior
// redirect) must not be resurrected by the cache — after its row is deleted,
// the redirect is a plain 404. The cache only ever holds what a redirect
// actually served.
func TestRedirectE2ENeverCachedCodeIs404AfterRowDeleted(t *testing.T) {
	router, pool := newE2ERouter(t)

	code := e2eShortenCode(t, router, "https://example.com/never-cached")
	// Deliberately NO redirect: nothing may have entered the cache.

	e2eDeleteRow(t, pool, code)

	rec := e2eGet(t, router, "/"+code)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /%s after DELETE status = %d, want %d (never-read codes are never cached) (body: %s)", code, rec.Code, http.StatusNotFound, rec.Body.String())
	}
}
