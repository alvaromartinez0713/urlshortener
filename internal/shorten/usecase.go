package shorten

import (
	"context"
	"errors"
	"fmt"

	"urlshortener/internal/shorturl"
)

// maxCodeAttempts bounds the S3 collision retry loop: on a unique-index
// collision the use case consumes a fresh sequence and saves again. Three
// attempts is already astronomically generous for a 62^n keyspace fed by
// a monotonic sequence; a bound keeps the loop provably terminating.
const maxCodeAttempts = 3

// UseCase shortens long URLs. It orchestrates the domain (ShortURL
// construction and validation live in internal/shorturl, S7) with
// persistence and code generation through core-owned ports (S5.2). It
// imports no framework and makes no HTTP or SQL decision (S5.1).
type UseCase struct {
	repo Repository
	gen  shorturl.CodeGenerator
}

// NewUseCase constructs the use case. repo and gen are commonly the same
// adapter (the postgres repository also implements shorturl.CodeGenerator
// via the urls_id sequence), but they are separate ports on purpose: code
// generation and persistence fail and scale independently.
func NewUseCase(repo Repository, gen shorturl.CodeGenerator) *UseCase {
	return &UseCase{repo: repo, gen: gen}
}

// Shorten validates and persists a new short URL (S2) and returns the
// built ShortURL ENTITY (S15): the use case speaks in domain terms only —
// it renders no presentation. Composing the public "short_url" is the
// handler's job (the HTTP adapter owns response shape, S5.3).
//
// Flow: the domain builds the ShortURL (validation + code assignment, S7)
// and only then is it persisted. Because NewShortURL validates before
// consuming a sequence, rejected input persists nothing and burns no
// sequence (S2). A uniqueness collision on save (S3) retries with a fresh
// sequence, bounded by maxCodeAttempts.
//
// Returned errors, matched via errors.Is:
//
//   - shorturl.ErrEmptyURL / shorturl.ErrInvalidURL /
//     shorturl.ErrInvalidURLScheme — client-supplied invalid input (422);
//   - ErrCodeCollision after exhausting retries (500);
//   - anything else — infrastructure failure from the ports, propagated
//     as-is (500).
func (uc *UseCase) Shorten(ctx context.Context, longURL string) (shorturl.ShortURL, error) {
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		// Domain construction re-validates each attempt, which is free
		// and keeps the invariant: a ShortURL is never invalid.
		su, err := shorturl.NewShortURL(longURL, uc.gen)
		if err != nil {
			return shorturl.ShortURL{}, err
		}

		err = uc.repo.Save(ctx, su)
		switch {
		case err == nil:
			return su, nil
		case errors.Is(err, ErrCodeCollision):
			continue // S3: fresh sequence, fresh code, try again
		default:
			return shorturl.ShortURL{}, fmt.Errorf("shorten: save failed: %w", err)
		}
	}
	return shorturl.ShortURL{}, ErrCodeGenerationExhausted
}
