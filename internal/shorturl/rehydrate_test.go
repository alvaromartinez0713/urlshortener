package shorturl

import (
	"errors"
	"testing"
)

// S11: the domain provides an explicit rehydration constructor for stored
// entities. These tests pin its trusted-boundary semantics.

func TestNewShortURLFromRepositoryRehydratesStoredEntity(t *testing.T) {
	// The rehydrated entity round-trips through the same accessors the
	// write path used: Save persists Code()/Target(), FindByCode
	// rehydrates with NewShortURLFromRepository — write and read are
	// symmetric over one trusted boundary (S11). Values come back
	// verbatim.
	su, err := NewShortURLFromRepository("10", "https://example.com/a/b?q=1&lang=en#section-2")
	if err != nil {
		t.Fatalf("NewShortURLFromRepository unexpected error: %v", err)
	}
	if su.Code() != "10" {
		t.Errorf("Code() = %q, want %q", su.Code(), "10")
	}
	if su.Target() != "https://example.com/a/b?q=1&lang=en#section-2" {
		t.Errorf("Target() = %q, want the stored target verbatim", su.Target())
	}
}

func TestNewShortURLFromRepositoryDoesNotRevalidate(t *testing.T) {
	// Trusted boundary (S11): the row was validated at write time by
	// NewShortURL, so rehydration deliberately does NOT re-run URL
	// validation — re-validating would be redundant ceremony on every
	// read and would 500 redirects for rows written under older rules or
	// manually altered afterwards. Tampering is an operator concern, not
	// a per-request validation job. A target the current write path would
	// reject must still rehydrate verbatim.
	su, err := NewShortURLFromRepository("1", "javascript:alert(1)")
	if err != nil {
		t.Fatalf("rehydration must not revalidate stored data: unexpected error %v", err)
	}
	if su.Target() != "javascript:alert(1)" {
		t.Errorf("Target() = %q, want the stored value verbatim", su.Target())
	}
	if su.Code() != "1" {
		t.Errorf("Code() = %q, want %q", su.Code(), "1")
	}
}

func TestNewShortURLFromRepositoryRejectsEmptyCodeOrTarget(t *testing.T) {
	// The one refusal a trusted constructor still makes: a row missing
	// its code or target cannot form a meaningful ShortURL (an empty
	// target would redirect to "", an empty code identifies nothing).
	// Only a corrupted/tampered row can produce this (the schema's NOT
	// NULL guards it), and it is refused as ErrCorruptStoredShortURL —
	// never silently rehydrated into a nonsense entity.
	tests := []struct {
		name   string
		code   string
		target string
	}{
		{name: "empty code", code: "", target: "https://example.com"},
		{name: "empty target", code: "10", target: ""},
		{name: "both empty", code: "", target: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			su, err := NewShortURLFromRepository(tc.code, tc.target)
			if !errors.Is(err, ErrCorruptStoredShortURL) {
				t.Fatalf("NewShortURLFromRepository(%q, %q) error = %v, want errors.Is ErrCorruptStoredShortURL", tc.code, tc.target, err)
			}
			if su != (ShortURL{}) {
				t.Errorf("on error the returned entity must be the zero value, got %+v", su)
			}
		})
	}
}
