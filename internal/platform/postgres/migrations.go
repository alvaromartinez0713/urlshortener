package postgres

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// migrationsFS embeds the versioned SQL files. Embedding keeps the binary
// self-contained: the composition root needs no data directory on disk.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// RunMigrations applies the embedded SQL migrations in filename order,
// exactly once each, tracked in schema_migrations. It is called by the
// composition root at startup (T4 wiring) before the server accepts
// traffic.
//
// Design: deliberately small and boring — no external migration tool, no
// down migrations. Each file runs inside a transaction so a partial
// migration can never linger; the tracker row commits atomically with it.
func RunMigrations(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			filename   TEXT PRIMARY KEY,
			applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		return fmt.Errorf("postgres: ensure schema_migrations: %w", err)
	}

	files, err := fs.Glob(migrationsFS, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("postgres: list embedded migrations: %w", err)
	}
	sort.Strings(files) // lexical order == version order for zero-padded names

	for _, name := range files {
		filename := fsFilePath(name)
		applied, err := migrationApplied(ctx, pool, filename)
		if err != nil {
			return err
		}
		if applied {
			continue
		}

		sqlBytes, err := migrationsFS.ReadFile(name)
		if err != nil {
			return fmt.Errorf("postgres: read migration %s: %w", name, err)
		}

		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, string(sqlBytes)); err != nil {
				return fmt.Errorf("postgres: apply migration %s: %w", filename, err)
			}
			if _, err := tx.Exec(ctx,
				`INSERT INTO schema_migrations (filename) VALUES ($1)`, filename); err != nil {
				return fmt.Errorf("postgres: record migration %s: %w", filename, err)
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func migrationApplied(ctx context.Context, pool *pgxpool.Pool, filename string) (bool, error) {
	var exists bool
	err := pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE filename = $1)`, filename,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("postgres: check migration %s: %w", filename, err)
	}
	return exists, nil
}

// fsFilePath strips the "migrations/" prefix so the tracker stores bare
// filenames ("0001_init.sql").
func fsFilePath(name string) string {
	if len(name) > len("migrations/") && name[:len("migrations/")] == "migrations/" {
		return name[len("migrations/"):]
	}
	return name
}
