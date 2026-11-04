package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

// migrationsFS embeds the goose SQL migrations (Up/Down annotated files).
// Embedding keeps the binary self-contained: the composition root needs
// no data directory on disk.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies the embedded goose migrations. It is called by
// the composition root at startup (T4 wiring) before the server accepts
// traffic, and by the sociable tests before exercising the adapter.
//
// Design (S10): ordering, tracking and concurrent-start safety are owned
// by goose, not custom code. The provider runs against a database/sql
// view of the existing *pgxpool.Pool (stdlib.OpenDBFromPool); closing
// that view does not close the caller-owned pool. Provider defaults are
// deliberately quiet (no verbose logging) — startup logs stay the
// composition root's business. Down migrations exist in every file for
// operators; they are never run automatically.
//
// Fresh-database semantics only (v1 dev): no compatibility with the old
// hand-rolled schema_migrations table is provided.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	db := stdlib.OpenDBFromPool(pool)
	defer db.Close()

	// Goose collects migration files with a root-level glob, so it gets
	// the FS rooted AT the migrations directory, not the package root.
	migrationFiles, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("postgres: open embedded migrations: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrationFiles)
	if err != nil {
		return fmt.Errorf("postgres: create goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("postgres: apply migrations: %w", err)
	}
	return nil
}
