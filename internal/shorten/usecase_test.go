package shorten

// Package shorten is the "shorten" vertical slice: use case + Gin handler
// (T4). The use case imports only the domain (internal/shorturl) and the
// stdlib (S5.1); the handler is the HTTP adapter of this slice.

import (
	"context"
	"errors"
	"testing"

	"urlshortener/internal/shorturl"
)

// fakeSequences is a deterministic CodeGenerator fake (implements
// shorturl.CodeGenerator): each call returns the next value in values; once
// the configured values are exhausted it fails. Its bound is modeled through
// INPUTS (how many values it holds), never through a call counter — a spy is
// an interaction assertion; the values list is behavior the use case can
// observe through the port (ADR-002). Pure stdlib — proves the use case
// tests run without Docker or network (S5.5).
type fakeSequences struct {
	values []uint64
	err    error
}

func (f *fakeSequences) NextSequence() (uint64, error) {
	if f.err != nil {
		return 0, f.err
	}
	if len(f.values) == 0 {
		return 0, errors.New("fakeSequences: exhausted")
	}
	v := f.values[0]
	f.values = f.values[1:]
	return v, nil
}

// fakeRepo is a deterministic Repository stub: Save returns the errors
// in saveErrs in order (one per call; once exhausted, it succeeds), and
// records every saved ShortURL. Pure stdlib — proves the use case tests
// run without Docker or network (S5.5).
type fakeRepo struct {
	saveErrs []error
	saved    []shorturl.ShortURL
}

func (f *fakeRepo) Save(ctx context.Context, su shorturl.ShortURL) error {
	f.saved = append(f.saved, su)
	if len(f.saved) <= len(f.saveErrs) {
		return f.saveErrs[len(f.saved)-1]
	}
	return nil
}

func TestShortenSuccess(t *testing.T) {
	repo := &fakeRepo{}
	gen := &fakeSequences{values: []uint64{0}}
	uc := NewUseCase(repo, gen, "https://sho.rt")

	res, err := uc.Shorten(context.Background(), "https://example.com/a/b?q=1")

	if err != nil {
		t.Fatalf("Shorten unexpected error: %v", err)
	}
	if res.Code != "0" {
		t.Errorf("res.Code = %q, want %q", res.Code, "0")
	}
	if res.ShortURL != "https://sho.rt/0" {
		t.Errorf("res.ShortURL = %q, want %q", res.ShortURL, "https://sho.rt/0")
	}
	if res.LongURL != "https://example.com/a/b?q=1" {
		t.Errorf("res.LongURL = %q, want the validated target", res.LongURL)
	}
	if len(repo.saved) != 1 {
		t.Fatalf("Save calls = %d, want 1", len(repo.saved))
	}
	if repo.saved[0].Code() != "0" || repo.saved[0].Target() != "https://example.com/a/b?q=1" {
		t.Errorf("persisted ShortURL = code %q target %q, want the domain entity as built",
			repo.saved[0].Code(), repo.saved[0].Target())
	}
}

func TestShortenBaseURLTrailingSlashIsTrimmed(t *testing.T) {
	uc := NewUseCase(&fakeRepo{}, &fakeSequences{values: []uint64{62}}, "https://sho.rt/")

	res, err := uc.Shorten(context.Background(), "https://example.com")

	if err != nil {
		t.Fatalf("Shorten unexpected error: %v", err)
	}
	if res.ShortURL != "https://sho.rt/10" {
		t.Errorf("res.ShortURL = %q, want %q (no double slash)", res.ShortURL, "https://sho.rt/10")
	}
}

func TestShortenCollisionRetriesThenSucceeds(t *testing.T) {
	// S3: a unique violation on code triggers a new sequence and a new
	// code; the second attempt succeeds.
	repo := &fakeRepo{saveErrs: []error{ErrCodeCollision}}
	gen := &fakeSequences{values: []uint64{1, 2}}
	uc := NewUseCase(repo, gen, "https://sho.rt")

	res, err := uc.Shorten(context.Background(), "https://example.com")

	if err != nil {
		t.Fatalf("Shorten unexpected error: %v", err)
	}
	if res.Code != "2" {
		t.Errorf("res.Code = %q, want %q (code from the retried sequence)", res.Code, "2")
	}
	// Behavior, not interaction counts (ADR-002): the code from the
	// retried sequence is the one that ends up persisted, after the
	// collided first attempt. repo.saved is the fake repo's effect log —
	// the port's observable effect, not a spy.
	if repo.saved[0].Code() != "1" {
		t.Errorf("first attempt persisted code %q, want %q", repo.saved[0].Code(), "1")
	}
	if last := repo.saved[len(repo.saved)-1]; last.Code() != "2" {
		t.Errorf("successful attempt persisted code %q, want %q (the retried sequence)", last.Code(), "2")
	}
}

func TestShortenExhaustedRetries(t *testing.T) {
	// Every attempt collides: after the bounded number of retries the use
	// case stops with ErrCodeGenerationExhausted (S3).
	//
	// The bound is modeled through INPUTS, not counted (ADR-002): the
	// fake generator exposes exactly maxCodeAttempts sequence values, so
	// a use case that attempted even one retry beyond the bound would
	// surface the fake's own exhaustion error instead of
	// ErrCodeGenerationExhausted and fail this test.
	repo := &fakeRepo{saveErrs: []error{ErrCodeCollision, ErrCodeCollision, ErrCodeCollision}}
	gen := &fakeSequences{values: []uint64{1, 2, 3}}
	uc := NewUseCase(repo, gen, "https://sho.rt")

	res, err := uc.Shorten(context.Background(), "https://example.com")

	if !errors.Is(err, ErrCodeGenerationExhausted) {
		t.Fatalf("Shorten error = %v, want errors.Is ErrCodeGenerationExhausted", err)
	}
	if res != (Result{}) {
		t.Errorf("exhausted retries must yield zero Result, got %+v", res)
	}
}

func TestShortenInvalidURLPassthrough(t *testing.T) {
	// Domain validation happens inside NewShortURL; the use case must not
	// swallow or wrap domain errors, and rejected input must persist
	// nothing and burn no sequence (S2).
	tests := []struct {
		name    string
		longURL string
		wantErr error
	}{
		{name: "empty", longURL: "", wantErr: shorturl.ErrEmptyURL},
		{name: "no scheme", longURL: "example.com", wantErr: shorturl.ErrInvalidURL},
		{name: "ftp scheme", longURL: "ftp://example.com", wantErr: shorturl.ErrInvalidURLScheme},
		{name: "no host", longURL: "http://", wantErr: shorturl.ErrInvalidURL},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// "No sequence burned" is asserted by BEHAVIOR, not by counting
			// calls (ADR-002): the fake generator fails on its FIRST call,
			// so if the use case consumed a sequence before validating, the
			// returned error would be the generator's, not the domain's.
			// errors.Is(err, wantErr) therefore proves validation happens
			// before generation.
			genErr := errors.New("sequence source must not be reached")
			repo := &fakeRepo{}
			gen := &fakeSequences{err: genErr}
			uc := NewUseCase(repo, gen, "https://sho.rt")

			res, err := uc.Shorten(context.Background(), tc.longURL)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Shorten(%q) error = %v, want errors.Is %v", tc.longURL, err, tc.wantErr)
			}
			if errors.Is(err, genErr) {
				t.Fatalf("Shorten(%q) reached the sequence source before validating", tc.longURL)
			}
			if res != (Result{}) {
				t.Errorf("rejected input must yield zero Result, got %+v", res)
			}
			if len(repo.saved) != 0 {
				t.Errorf("rejected input persisted %d rows, want 0", len(repo.saved))
			}
		})
	}
}

func TestShortenGeneratorErrorPassthrough(t *testing.T) {
	genErr := errors.New("sequence source down")
	repo := &fakeRepo{}
	gen := &fakeSequences{err: genErr}
	uc := NewUseCase(repo, gen, "https://sho.rt")

	res, err := uc.Shorten(context.Background(), "https://example.com")

	if !errors.Is(err, genErr) {
		t.Fatalf("Shorten error = %v, want errors.Is %v", err, genErr)
	}
	if res != (Result{}) {
		t.Errorf("generator failure must yield zero Result, got %+v", res)
	}
	if len(repo.saved) != 0 {
		t.Errorf("generator failure persisted %d rows, want 0", len(repo.saved))
	}
}

func TestShortenRepositoryErrorPassthrough(t *testing.T) {
	// A non-collision repository failure (e.g. the database is down) must
	// surface as-is: the use case must not mask it as exhaustion or as a
	// client error.
	repoErr := errors.New("connection refused")
	repo := &fakeRepo{saveErrs: []error{repoErr}}
	gen := &fakeSequences{values: []uint64{1}}
	uc := NewUseCase(repo, gen, "https://sho.rt")

	res, err := uc.Shorten(context.Background(), "https://example.com")

	if !errors.Is(err, repoErr) {
		t.Fatalf("Shorten error = %v, want errors.Is %v", err, repoErr)
	}
	if res != (Result{}) {
		t.Errorf("repository failure must yield zero Result, got %+v", res)
	}
}
