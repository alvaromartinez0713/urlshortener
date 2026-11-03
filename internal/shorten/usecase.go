package shorten

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

	// baseURL is the public origin of generated short URLs (e.g.
	// "https://sho.rt"), used only to render Result.ShortURL. It is a
	// plain configuration field, deliberately NOT a port: a port exists
	// for interactions with the outside world that can fail or vary at
	// runtime; the base URL is a static deployment setting read by the
	// composition root. Making it a port would be ceremony, not
	// architecture.
	baseURL string
}

// NewUseCase constructs the use case. repo and gen are commonly the same
// adapter (the postgres repository also implements shorturl.CodeGenerator
// via the urls_id sequence), but they are separate ports on purpose: code
// generation and persistence fail and scale independently.
func NewUseCase(repo Repository, gen shorturl.CodeGenerator, baseURL string) *UseCase {
	return &UseCase{
		repo:    repo,
		gen:     gen,
		baseURL: strings.TrimRight(baseURL, "/"),
	}
}

// Result is the outcome of a successful Shorten: everything the response
// needs, nothing more.
type Result struct {
	Code     string
	ShortURL string
	LongURL  string
}

// Shorten validates and persists a new short URL (S2).
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
func (uc *UseCase) Shorten(ctx context.Context, longURL string) (Result, error) {
	for attempt := 0; attempt < maxCodeAttempts; attempt++ {
		// Domain construction re-validates each attempt, which is free
		// and keeps the invariant: a ShortURL is never invalid.
		su, err := shorturl.NewShortURL(longURL, uc.gen)
		if err != nil {
			return Result{}, err
		}

		err = uc.repo.Save(ctx, su)
		switch {
		case err == nil:
			return Result{
				Code:     su.Code(),
				ShortURL: uc.baseURL + "/" + su.Code(),
				LongURL:  su.Target(),
			}, nil
		case errors.Is(err, ErrCodeCollision):
			continue // S3: fresh sequence, fresh code, try again
		default:
			return Result{}, fmt.Errorf("shorten: save failed: %w", err)
		}
	}
	return Result{}, ErrCodeGenerationExhausted
}
