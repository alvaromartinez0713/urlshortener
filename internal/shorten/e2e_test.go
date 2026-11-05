package shorten_test

// End-to-end test for the shorten slice (T4): real Gin router, real use
// case, real pgx adapter, REAL Postgres via testcontainers. It verifies
// the full HTTP path of S2 (201 / 422 / 400) and that rejected input
// persists nothing. Redirect (GET /:code) is T5's slice and is out of
// scope here. Skips gracefully when Docker is unavailable.

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"urlshortener/internal/api"
	"urlshortener/internal/platform/postgres"
	"urlshortener/internal/shorten"
	"urlshortener/internal/testsupport"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"
)

// Shared container (S12 fast feedback, T11): ONE Postgres container per
// package, started lazily on first use and reused by every e2e test in the
// package; tests stay SEQUENTIAL (no t.Parallel) so the shared handle is
// consumed without races. TestMain terminates the container after the run
// (ryuk is disabled on this host; see startE2EPostgres).
// newE2ERouter wires the composition root's real dependency chain:
// postgres adapter (Repository + CodeGenerator) -> use case -> handler ->
// api.MountShorten.
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

func newE2ERouter(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := startE2EPostgres(t)
	adapter := postgres.New(pool)
	useCase := shorten.NewUseCase(adapter, adapter)

	router := api.NewRouter(slog.Default())
	api.MountShorten(router, shorten.NewHandler(useCase, "http://localhost:8080"))
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

func TestShortenE2ECreated(t *testing.T) {
	router, _ := newE2ERouter(t)

	rec := e2ePost(t, router, `{"url":"https://example.com/very/long/path"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var body struct {
		Code     string `json:"code"`
		ShortURL string `json:"short_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body.Code == "" {
		t.Fatal("body[code] is empty, want a generated code")
	}
	if body.ShortURL != "http://localhost:8080/"+body.Code {
		t.Errorf("body[short_url] = %q, want baseURL + %q", body.ShortURL, body.Code)
	}
}

func TestShortenE2EDistinctURLsGetDistinctCodes(t *testing.T) {
	router, _ := newE2ERouter(t)

	r1 := e2ePost(t, router, `{"url":"https://example.com/one"}`)
	r2 := e2ePost(t, router, `{"url":"https://example.com/two"}`)

	var b1, b2 struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(r1.Body.Bytes(), &b1); err != nil {
		t.Fatalf("first body not JSON: %v", err)
	}
	if err := json.Unmarshal(r2.Body.Bytes(), &b2); err != nil {
		t.Fatalf("second body not JSON: %v", err)
	}
	if b1.Code == "" || b2.Code == "" {
		t.Fatalf("empty codes: %q, %q", b1.Code, b2.Code)
	}
	if b1.Code == b2.Code {
		t.Errorf("distinct URLs produced the same code %q", b1.Code)
	}
}

func TestShortenE2EInvalidURLIs422AndPersistsNothing(t *testing.T) {
	router, pool := newE2ERouter(t)

	rec := e2ePost(t, router, `{"url":"javascript:alert(1)"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["status"] != float64(http.StatusUnprocessableEntity) || body["title"] == "" {
		t.Errorf("body is not a problem details document: %v", body)
	}

	var count int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM urls`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Errorf("rows persisted after rejected input = %d, want 0 (S2)", count)
	}
}

func TestShortenE2EMalformedJSONIs400(t *testing.T) {
	router, _ := newE2ERouter(t)

	rec := e2ePost(t, router, `{not json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}
