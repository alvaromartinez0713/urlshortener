package redirect_test

// End-to-end test for the redirect slice (T5): real Gin router, real use
// case, real pgx adapter, REAL Postgres via testcontainers. It verifies
// the full HTTP path of S2's redirect contract (301 + Location, 404 for
// unknown codes, case-sensitive codes) against the same mounted router
// shape as the composition root. Skips gracefully when Docker is
// unavailable.
//
// Each test function starts its OWN container (startE2EPostgres is called
// per test through newE2ERouter), so tests never share database state and
// cannot interfere with each other — the same convention as
// internal/shorten/e2e_test.go, including its ryuk/wait-strategy
// compensations.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"urlshortener/internal/api"
	"urlshortener/internal/platform/postgres"
	"urlshortener/internal/redirect"
	"urlshortener/internal/shorten"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	postgresContainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func startE2EPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	// Same SELinux workaround as internal/platform/postgres and
	// internal/shorten: ryuk's bind-mounted Docker socket is denied by
	// the enforcing host policy; disable it and rely on t.Cleanup for
	// termination.
	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	ctr, err := postgresContainer.Run(ctx, "postgres:16-alpine",
		// Wait for BOTH "ready to accept connections" log lines: after
		// initdb the server restarts once, and connecting during that
		// window gets connections reset.
		postgresContainer.BasicWaitStrategies(),
		postgresContainer.WithDatabase("urlshortener"),
		postgresContainer.WithUsername("urlshortener"),
		postgresContainer.WithPassword("urlshortener"),
	)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "docker") {
			t.Skipf("docker unavailable, skipping e2e test: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(ctx) })

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("container connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := postgres.RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return pool
}

// newE2ERouter wires the composition root's real dependency chain: ONE
// postgres adapter implementing every core-owned port (shorten.Repository
// + shorturl.CodeGenerator + redirect.Finder) -> use cases -> handlers ->
// api.MountShorten + api.MountRedirect.
func newE2ERouter(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := startE2EPostgres(t)
	adapter := postgres.New(pool)

	shortenUseCase := shorten.NewUseCase(adapter, adapter, "http://localhost:8080")
	redirectUseCase := redirect.NewUseCase(adapter)

	router := api.NewRouter()
	api.MountShorten(router, shorten.NewHandler(shortenUseCase))
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
