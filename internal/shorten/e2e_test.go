package shorten_test

// End-to-end test for the shorten slice (T4): real Gin router, real use
// case, real pgx adapter, REAL Postgres via testcontainers. It verifies
// the full HTTP path of S2 (201 / 422 / 400) and that rejected input
// persists nothing. Redirect (GET /:code) is T5's slice and is out of
// scope here. Skips gracefully when Docker is unavailable.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"urlshortener/internal/api"
	"urlshortener/internal/platform/postgres"
	"urlshortener/internal/shorten"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	postgresContainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Shared container (S12 fast feedback, T11): ONE Postgres container per
// package, started lazily on first use and reused by every e2e test in the
// package; tests stay SEQUENTIAL (no t.Parallel) so the shared handle is
// consumed without races. TestMain terminates the container after the run
// (ryuk is disabled on this host; see startE2EPostgres).
var (
	sharedOnce sync.Once
	sharedPool *pgxpool.Pool
	sharedCtr  *postgresContainer.PostgresContainer
	sharedErr  error
)

func TestMain(m *testing.M) {
	code := m.Run()
	if sharedPool != nil {
		sharedPool.Close()
	}
	if sharedCtr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sharedCtr.Terminate(ctx)
	}
	os.Exit(code)
}

// startE2EPostgres returns the package-wide Postgres pool with a clean urls
// table. Skips (instead of failing) when Docker is unavailable.
func startE2EPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	// Same SELinux workaround as internal/platform/postgres: ryuk's
	// bind-mounted Docker socket is denied by the enforcing host policy;
	// disable it and rely on TestMain for termination.
	t.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	sharedOnce.Do(func() {
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
			sharedErr = fmt.Errorf("start postgres container: %w", err)
			return
		}
		sharedCtr = ctr

		dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
		if err != nil {
			sharedErr = fmt.Errorf("container connection string: %w", err)
			return
		}
		pool, err := pgxpool.New(context.Background(), dsn)
		if err != nil {
			sharedErr = fmt.Errorf("connect pool: %w", err)
			return
		}

		if err := postgres.RunMigrations(context.Background(), pool); err != nil {
			pool.Close()
			sharedErr = fmt.Errorf("run migrations: %w", err)
			return
		}
		sharedPool = pool
	})

	if sharedErr != nil {
		if strings.Contains(strings.ToLower(sharedErr.Error()), "docker") {
			t.Skipf("docker unavailable, skipping e2e test: %v", sharedErr)
		}
		t.Fatalf("start shared postgres: %v", sharedErr)
	}

	// Per-test DATA isolation (S12): TRUNCATE ... RESTART IDENTITY before
	// each test. Chosen over unique fixtures because the rejected-input
	// test asserts count(*) = 0 over the WHOLE urls table; RESTART IDENTITY
	// additionally keeps the shared sequence deterministic for every test.
	// Tests are sequential, so a truncate at test start is race-free.
	if _, err := sharedPool.Exec(context.Background(), `TRUNCATE urls RESTART IDENTITY`); err != nil {
		t.Fatalf("reset urls table: %v", err)
	}
	return sharedPool
}

// newE2ERouter wires the composition root's real dependency chain:
// postgres adapter (Repository + CodeGenerator) -> use case -> handler ->
// api.MountShorten.
func newE2ERouter(t *testing.T) (http.Handler, *pgxpool.Pool) {
	t.Helper()
	pool := startE2EPostgres(t)
	adapter := postgres.New(pool)
	useCase := shorten.NewUseCase(adapter, adapter, "http://localhost:8080")

	router := api.NewRouter()
	api.MountShorten(router, shorten.NewHandler(useCase))
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
