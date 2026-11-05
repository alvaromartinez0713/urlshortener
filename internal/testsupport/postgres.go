// Package testsupport is TEST-ONLY infrastructure (S24): the shared
// container scaffolding for sociable and e2e tests. Production code never
// imports it.
//
// It owns the whole lifecycle of ONE process-wide Postgres container per
// test binary: lazy start on first use, embedded goose migrations, and
// per-test data isolation (TRUNCATE ... RESTART IDENTITY before each
// test). Tests stay SEQUENTIAL (no t.Parallel) so the shared handle is
// consumed without races. Termination belongs to the consuming package's
// TestMain via CloseE2EPostgres (ryuk is disabled on this host; see the
// comment inside E2EPostgres).
package testsupport

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	postgresContainer "github.com/testcontainers/testcontainers-go/modules/postgres"

	"urlshortener/internal/platform/postgres"
)

var (
	sharedOnce sync.Once
	sharedPool *pgxpool.Pool
	sharedCtr  *postgresContainer.PostgresContainer
	sharedErr  error
)

// E2EPostgres returns the process-wide Postgres pool with a clean urls
// table and migrations applied. Skips (instead of failing) when Docker is
// unavailable.
func E2EPostgres(t *testing.T) *pgxpool.Pool {
	t.Helper()

	// Environmental workaround, documented: this host runs SELinux in
	// enforcing mode, which denies testcontainers' ryuk reaper container
	// access to the bind-mounted Docker socket (the mount works only with
	// --security-opt label=disable, which the reaper mount cannot carry).
	// Ryuk is therefore disabled for these tests; cleanup is instead
	// guaranteed by CloseE2EPostgres, called from the consuming package's
	// TestMain after the run. On hosts without this SELinux restriction,
	// ryuk remains enabled unless this env var is set.
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
		pool, err := postgres.Connect(ctx, dsn)
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
		if dockerUnavailable(sharedErr) {
			t.Skipf("docker unavailable, skipping test: %v", sharedErr)
		}
		t.Fatalf("start shared postgres: %v", sharedErr)
	}

	// Per-test DATA isolation (S12): TRUNCATE ... RESTART IDENTITY before
	// each test. Chosen over unique fixtures because several assertions
	// are absolute over the whole urls table ("rejected input persists
	// nothing" counts rows), and RESTART IDENTITY keeps the shared
	// sequence deterministic for every test. Tests are sequential, so a
	// truncate at test start is race-free.
	if _, err := sharedPool.Exec(context.Background(), `TRUNCATE urls RESTART IDENTITY`); err != nil {
		t.Fatalf("reset urls table: %v", err)
	}
	return sharedPool
}

// CloseE2EPostgres releases the shared container. Call it from the
// consuming package's TestMain after m.Run().
func CloseE2EPostgres() {
	if sharedPool != nil {
		sharedPool.Close()
	}
	if sharedCtr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = sharedCtr.Terminate(ctx)
	}
}

// dockerUnavailable reports whether err is an environment failure (no
// Docker daemon) rather than a real test setup failure — such tests SKIP
// instead of failing so the suite runs on hosts without Docker.
func dockerUnavailable(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "docker") || strings.Contains(msg, "daemon") ||
		strings.Contains(msg, "cannot connect to the docker")
}
