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
	"strings"
	"testing"
	"time"

	"urlshortener/internal/shorten"
	"urlshortener/internal/shorturl"

	pgxpool "github.com/jackc/pgx/v5/pgxpool"
	postgresContainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// startPostgres starts a disposable Postgres container, applies the
// embedded migrations, and returns a connected pool. Terminates on test
// cleanup. Skips (instead of failing) when Docker is unavailable.
func startPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	// Environmental workaround, documented: this host runs SELinux in
	// enforcing mode, which denies testcontainers' ryuk reaper container
	// access to the bind-mounted Docker socket (the mount works only with
	// --security-opt label=disable, which the reaper mount cannot carry).
	// Ryuk is therefore disabled for these tests; cleanup is still
	// guaranteed by t.Cleanup below, which terminates every container it
	// started. On hosts without this SELinux restriction, ryuk remains
	// enabled unless this env var is set.
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
		if dockerUnavailable(err) {
			t.Skipf("docker unavailable, skipping sociable test: %v", err)
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

	if err := RunMigrations(ctx, pool); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return pool
}

func dockerUnavailable(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "docker") || strings.Contains(msg, "daemon") ||
		errors.Is(err, context.DeadlineExceeded)
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
