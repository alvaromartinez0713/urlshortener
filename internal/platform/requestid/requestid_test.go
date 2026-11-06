package requestid_test

// Unit tests for the request-id platform package (S30/T32): id generation
// (shape + uniqueness), context roundtrip through an unexported key, and
// sanitization of incoming X-Request-ID values — an attacker-controlled
// header must never reach logs or response headers verbatim. Pure stdlib.

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"urlshortener/internal/platform/requestid"
)

var idShape = regexp.MustCompile(`^[0-9a-f]{16}$`)

func TestNewIDIsSixteenLowercaseHexChars(t *testing.T) {
	id := requestid.NewID()
	if !idShape.MatchString(id) {
		t.Errorf("NewID() = %q, want 16 lowercase hex chars", id)
	}
}

func TestNewIDIsUniqueAcrossCalls(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		id := requestid.NewID()
		if seen[id] {
			t.Fatalf("NewID() produced a duplicate id %q in 1000 calls", id)
		}
		seen[id] = true
	}
}

func TestPutThenFromContextRoundtrip(t *testing.T) {
	ctx := requestid.Put(context.Background(), "corr-42")
	got, ok := requestid.FromContext(ctx)
	if !ok {
		t.Fatal("FromContext(Put(ctx, id)) reported ok=false, want true")
	}
	if got != "corr-42" {
		t.Errorf("FromContext(Put(ctx, id)) = %q, want %q", got, "corr-42")
	}
}

func TestFromContextWithoutIDIsFalse(t *testing.T) {
	if got, ok := requestid.FromContext(context.Background()); ok || got != "" {
		t.Errorf("FromContext(plain ctx) = (%q, %v), want (\"\", false)", got, ok)
	}
}

func TestSanitizeAcceptsHeaderSafeValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "single char", value: "a"},
		{name: "alphanumeric", value: "8f3c2a1b9d4e5f60"},
		{name: "all allowed punctuation", value: "._-"},
		{name: "mixed", value: "req-42_abc.def"},
		{name: "max length 64", value: strings.Repeat("a", 64)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := requestid.Sanitize(tc.value)
			if !ok {
				t.Fatalf("Sanitize(%q) rejected a valid value", tc.value)
			}
			if got != tc.value {
				t.Errorf("Sanitize(%q) = %q, want the value verbatim", tc.value, got)
			}
		})
	}
}

func TestSanitizeRejectsUnsafeValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "empty", value: ""},
		{name: "space", value: "req 42"},
		{name: "newline injection", value: "req\n42"},
		{name: "header separator", value: "a\r\nX-Evil: 1"},
		{name: "angle bracket", value: "<script>"},
		{name: "quote", value: `"id"`},
		{name: "percent escape", value: "%2e%2e"},
		{name: "unicode", value: "cód"},
		{name: "too long (65)", value: strings.Repeat("a", 65)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := requestid.Sanitize(tc.value)
			if ok {
				t.Errorf("Sanitize(%q) accepted an unsafe value", tc.value)
			}
			if got != "" {
				t.Errorf("Sanitize(%q) = %q, want empty on rejection", tc.value, got)
			}
		})
	}
}

// TestPutClientRoundTrip (S31/T35): the client-supplied id travels under
// its OWN context key — it can never collide with (or be mistaken for)
// the server-generated internal id.
func TestPutClientRoundTrip(t *testing.T) {
	ctx := requestid.PutClient(context.Background(), "client-correlation.42")
	if got, ok := requestid.ClientFromContext(ctx); !ok || got != "client-correlation.42" {
		t.Errorf("ClientFromContext = (%q, %v), want the stored client id", got, ok)
	}
	// The two keys are independent.
	if _, ok := requestid.FromContext(ctx); ok {
		t.Error("FromContext = present, want absent: the client key must not leak into the internal one")
	}
}
