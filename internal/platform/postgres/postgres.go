// Package postgres is the persistence adapter of the slices: a dumb
// translator (S5.3) between the core-owned ports (shorten.Repository,
// shorturl.CodeGenerator) and PostgreSQL via pgx v5. It decides nothing:
// no validation, no business rules — it stores what it is given and maps
// storage outcomes onto the domain-level errors the slices define.
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"urlshortener/internal/shorten"
	"urlshortener/internal/shorturl"
)

// pgErrUniqueViolation is the Postgres SQLSTATE for a unique constraint
// violation (integrity_constraint_violation class 23, 23505).
const pgErrUniqueViolation = "23505"

// codeUniqueConstraint is the constraint backing `code TEXT NOT NULL
// UNIQUE` in migration 0001 (Postgres default name: table + column +
// _key). Matching on constraint name — not just the 23505 class — keeps
// ErrCodeCollision precise: a hypothetical future unique constraint on
// another column must not be mistaken for a code collision.
const codeUniqueConstraint = "urls_code_key"

// Repository implements shorten.Repository (write side) and
// shorturl.CodeGenerator (sequence source) over a pgx pool.
type Repository struct {
	pool *pgxpool.Pool
}

// New constructs the adapter over an established pool.
func New(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Save implements shorten.Repository: one INSERT, nothing else.
//
// A unique violation on urls_code_key maps to shorten.ErrCodeCollision so
// the use case can run its S3 retry. Every other failure is returned
// wrapped with context — the slice does not attempt to interpret storage
// outages.
func (r *Repository) Save(ctx context.Context, su shorturl.ShortURL) error {
	_, err := r.pool.Exec(ctx,
		`INSERT INTO urls (code, long_url) VALUES ($1, $2)`,
		su.Code(), su.Target(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) &&
			pgErr.Code == pgErrUniqueViolation &&
			pgErr.ConstraintName == codeUniqueConstraint {
			return shorten.ErrCodeCollision
		}
		return fmt.Errorf("postgres: save short url: %w", err)
	}
	return nil
}

// NextSequence implements shorturl.CodeGenerator: the adapter decides
// WHERE the sequence comes from (the urls_id identity sequence backing
// the bigserial, S6); the domain already decided WHAT a code is
// (Base62Encode of this value, S3).
//
// Documented trade-off (deliberate): if a process reads a sequence and
// then fails to insert (crash, invalid... it cannot happen through
// NewShortURL's validate-first ordering, but the port cannot assume it),
// the sequence value is consumed and the code never exists — a gap. Gaps
// are FINE: codes stay unique, opaque and monotonic-ish; nothing in the
// specs requires gaplessness, and enforcing it would serialize every
// insert. Prefer simple uniqueness.
//
// The port signature is context-free (domain decision, S5.1: the domain
// imports no context plumbing); the adapter runs against a background
// context. The pool carries per-query timeouts.
func (r *Repository) NextSequence() (uint64, error) {
	var seq uint64
	err := r.pool.QueryRow(context.Background(), `SELECT nextval('urls_id_seq')`).Scan(&seq)
	if err != nil {
		return 0, fmt.Errorf("postgres: next sequence: %w", err)
	}
	return seq, nil
}

// ensure pgx import stays used if Exec error handling changes shape.
var _ = pgx.ErrNoRows
