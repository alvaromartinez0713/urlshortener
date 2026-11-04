package postgres

// Sociable tests (S5.5): these verify the pgx adapter against a REAL
// Postgres via testcontainers. They exercise adapter behavior only —
// persistence shape, uniqueness-violation mapping, and the sequence
// source — never business rules (those live in the domain/use case unit
// tests, which run without Docker).
//
// If Docker is unavailable the tests skip gracefully instead of failing.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"urlshortener/internal/shorten"
	"urlshortener/internal/shorturl"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	postgresContainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Shared container (S12 fast feedback, T11): ONE Postgres container per
// package, started lazily on first use and reused by every container test
// in the package. Container startup (~7-8s) dominates wall time, so N
// starts collapse into one. Tests stay SEQUENTIAL (no t.Parallel), so the
// shared handle is consumed without races, and TestMain terminates the
// container after the run (ryuk is disabled on this host; see below).
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

// startPostgres returns the package-wide Postgres pool with a clean urls
// table. Skips (instead of failing) when Docker is unavailable.
func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	// Environmental workaround, documented: this host runs SELinux in
	// enforcing mode, which denies testcontainers' ryuk reaper container
	// access to the bind-mounted Docker socket (the mount works only with
	// --security-opt label=disable, which the reaper mount cannot carry).
	// Ryuk is therefore disabled for these tests; cleanup is instead
	// guaranteed by TestMain, which terminates the shared container after
	// the run. On hosts without this SELinux restriction, ryuk remains
	// enabled unless this env var is set.
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

		if err := RunMigrations(context.Background(), pool); err != nil {
			pool.Close()
			sharedErr = fmt.Errorf("run migrations: %w", err)
			return
		}
		sharedPool = pool
	})

	if sharedErr != nil {
		if dockerUnavailable(sharedErr) {
			t.Skipf("docker unavailable, skipping sociable test: %v", sharedErr)
		}
		t.Fatalf("start shared postgres: %v", sharedErr)
	}

	// Per-test DATA isolation (S12): TRUNCATE ... RESTART IDENTITY before
	// each test, chosen over unique fixtures because several assertions
	// here are absolute over the whole table ("rejected input persists
	// nothing" counts rows; the collision test counts rows with code
	// "10"), and RESTART IDENTITY keeps the sequence deterministic for
	// sequence-adjacent assertions (NextSequence is asserted relatively,
	// second > first, so it holds either way). Tests are sequential, so a
	// truncate at test start is race-free.
	if _, err := sharedPool.Exec(context.Background(), `TRUNCATE urls RESTART IDENTITY`); err != nil {
		t.Fatalf("reset urls table: %v", err)
	}
	return sharedPool
}

func dockerUnavailable(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "docker") || strings.Contains(msg, "daemon") ||
		errors.Is(err, context.DeadlineExceeded)
}

// TestRunMigrationsIdempotentAndTracked pins the S10 contract: migrations
// are tracked by goose's own table (not the hand-rolled schema_migrations),
// and running the migrations twice is a no-op the second time.
func TestRunMigrationsIdempotentAndTracked(t *testing.T) {
	// The shared container applied the migrations once at startup; this
	// second RunMigrations call pins the S10 contract: tracking makes a
	// re-run a no-op, not an error.
	pool := startPostgres(t)
	ctx := context.Background()

	// Re-run: tracking must make this a no-op, not an error.
	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("second RunMigrations: %v", err)
	}

	var tracked bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'goose_db_version')`,
	).Scan(&tracked)
	if err != nil {
		t.Fatalf("check goose_db_version: %v", err)
	}
	if !tracked {
		t.Fatal("goose_db_version table missing: migrations must be tracked by the tool (S10)")
	}

	// Migration 0001 is the only one; it must be recorded as applied.
	var version int64
	if err := pool.QueryRow(ctx,
		`SELECT max(version_id) FROM goose_db_version`,
	).Scan(&version); err != nil {
		t.Fatalf("read goose tracking table: %v", err)
	}
	if version != 1 {
		t.Errorf("goose_db_version max(version_id) = %d, want 1", version)
	}

	// Schema semantics unchanged (S6): insert + select works and the
	// unique constraint on code is present.
	if _, err := pool.Exec(ctx,
		`INSERT INTO urls (code, long_url) VALUES ($1, $2)`, "zzz", "https://example.com/zzz"); err != nil {
		t.Fatalf("insert into urls: %v", err)
	}
	var longURL string
	if err := pool.QueryRow(ctx,
		`SELECT long_url FROM urls WHERE code = $1`, "zzz").Scan(&longURL); err != nil {
		t.Fatalf("select from urls: %v", err)
	}
	if longURL != "https://example.com/zzz" {
		t.Errorf("long_url = %q, want https://example.com/zzz", longURL)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO urls (code, long_url) VALUES ($1, $2)`, "zzz", "https://example.com/other"); err == nil {
		t.Error("duplicate code insert succeeded, want unique violation on urls.code")
	}
}

func TestSavePersistsRow(t *testing.T) {
	pool := startPostgres(t)
	repo := New(pool)

	su, err := shorturl.NewShortURL("https://example.com/a/b?q=1", &stubGen{seq: 62})
	if err != nil {
		t.Fatalf("domain construction: %v", err)
	}

	if err := repo.Save(context.Background(), su); err != nil {
		t.Fatalf("Save: %v", err)
	}

	var code, longURL string
	var createdAt time.Time
	err = pool.QueryRow(context.Background(),
		`SELECT code, long_url, created_at FROM urls WHERE code = $1`, su.Code(),
	).Scan(&code, &longURL, &createdAt)
	if err != nil {
		t.Fatalf("saved row not found: %v", err)
	}
	if code != su.Code() || longURL != su.Target() {
		t.Errorf("row = (%q, %q), want (%q, %q)", code, longURL, su.Code(), su.Target())
	}
	if createdAt.IsZero() {
		t.Error("created_at is zero, want the DB default now()")
	}
}

func TestSaveCollisionReturnsErrCodeCollision(t *testing.T) {
	// S3: the unique index on code is the collision source of truth; the
	// adapter's job is to translate 23505 into the slice-owned
	// ErrCodeCollision so the use case can retry.
	pool := startPostgres(t)
	repo := New(pool)
	ctx := context.Background()

	// Seed the conflicting row directly through the pool: this test owns
	// adapter mapping, not domain behavior.
	if _, err := pool.Exec(ctx, `INSERT INTO urls (code, long_url) VALUES ($1, $2)`, "10", "https://seed.example.com"); err != nil {
		t.Fatalf("seed conflicting row: %v", err)
	}

	su, err := shorturl.NewShortURL("https://example.com", &stubGen{seq: 62}) // code "10"
	if err != nil {
		t.Fatalf("domain construction: %v", err)
	}

	err = repo.Save(ctx, su)
	if !errors.Is(err, shorten.ErrCodeCollision) {
		t.Fatalf("Save error = %v, want errors.Is ErrCodeCollision", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM urls WHERE code = $1`, "10").Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Errorf("rows with code %q = %d, want 1 (collision must not duplicate)", "10", count)
	}
}

func TestNextSequenceMonotonic(t *testing.T) {
	pool := startPostgres(t)
	repo := New(pool)

	first, err := repo.NextSequence()
	if err != nil {
		t.Fatalf("NextSequence: %v", err)
	}
	second, err := repo.NextSequence()
	if err != nil {
		t.Fatalf("NextSequence: %v", err)
	}
	if second <= first {
		t.Errorf("NextSequence not monotonic: %d then %d", first, second)
	}
}

// stubGen adapts a fixed sequence to shorturl.CodeGenerator for building
// domain entities in adapter tests.
type stubGen struct{ seq uint64 }

func (g *stubGen) NextSequence() (uint64, error) { return g.seq, nil }
