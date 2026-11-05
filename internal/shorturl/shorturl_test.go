package shorturl

import (
	"errors"
	"testing"
)

// fakeSequences is a deterministic CodeGenerator stub: each call to
// NextSequence returns the next value in the slice, and exhaustion is
// modeled through INPUTS (the values list runs out), never through a
// call counter (S22/ADR-002). Pure stdlib, no Docker/network — proves
// S5 testability for the domain core.
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

func TestNewShortURLValidURLs(t *testing.T) {
	tests := []struct {
		name       string
		longURL    string
		sequence   uint64
		wantTarget string
		wantCode   string
	}{
		{name: "http with path", longURL: "http://example.com", sequence: 0, wantTarget: "http://example.com", wantCode: "0"},
		{name: "https", longURL: "https://example.com/a/b?q=1", sequence: 61, wantTarget: "https://example.com/a/b?q=1", wantCode: "z"},
		{name: "https with port", longURL: "https://example.com:8443/x", sequence: 62, wantTarget: "https://example.com:8443/x", wantCode: "10"},
		{name: "large sequence", longURL: "https://go.dev/blog", sequence: 3844, wantTarget: "https://go.dev/blog", wantCode: "100"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gen := &fakeSequences{values: []uint64{tc.sequence}}

			su, err := NewShortURL(tc.longURL, gen)

			if err != nil {
				t.Fatalf("NewShortURL(%q) unexpected error: %v", tc.longURL, err)
			}
			if su.Target() != tc.wantTarget {
				t.Errorf("Target() = %q, want %q", su.Target(), tc.wantTarget)
			}
			if su.Code() != tc.wantCode {
				t.Errorf("Code() = %q, want %q", su.Code(), tc.wantCode)
			}
			// "The sequence was consumed" needs no call counter (S22):
			// the code IS the Base62 of the sequence value, so a correct
			// Code() proves the generator was used.
		})
	}
}

func TestNewShortURLInvalidURLs(t *testing.T) {
	tests := []struct {
		name    string
		longURL string
		wantErr error
	}{
		{name: "empty", longURL: "", wantErr: ErrEmptyURL},
		{name: "whitespace only", longURL: "   ", wantErr: ErrEmptyURL},
		{name: "no scheme", longURL: "example.com", wantErr: ErrInvalidURL},
		{name: "ftp scheme", longURL: "ftp://example.com/file", wantErr: ErrInvalidURLScheme},
		{name: "javascript scheme", longURL: "javascript:alert(1)", wantErr: ErrInvalidURLScheme},
		{name: "data scheme", longURL: "data:text/html,hello", wantErr: ErrInvalidURLScheme},
		{name: "malformed", longURL: "http://exa mple.com", wantErr: ErrInvalidURL},
		{name: "scheme but no host", longURL: "http://", wantErr: ErrInvalidURL},
		{name: "userinfo trick", longURL: "http://user:pass@example.com", wantErr: ErrInvalidURL},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// "Invalid input must not consume a sequence" is asserted by
			// BEHAVIOR, not by a call counter (S22): the fake generator
			// fails on its FIRST call, so if NewShortURL consumed a
			// sequence before validating, the returned error would be
			// the generator's, not the domain's.
			genErr := errors.New("sequence source must not be reached")
			gen := &fakeSequences{values: []uint64{1}, err: genErr}

			su, err := NewShortURL(tc.longURL, gen)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NewShortURL(%q) error = %v, want errors.Is %v", tc.longURL, err, tc.wantErr)
			}
			if errors.Is(err, genErr) {
				t.Fatalf("NewShortURL(%q) reached the sequence source before validating", tc.longURL)
			}
			if su.Code() != "" || su.Target() != "" {
				t.Errorf("rejected input must yield zero-value entity, got code=%q target=%q", su.Code(), su.Target())
			}
		})
	}
}

func TestNewShortURLGeneratorError(t *testing.T) {
	gen := &fakeSequences{err: errors.New("sequence source down")}

	su, err := NewShortURL("https://example.com", gen)

	if err == nil {
		t.Fatal("expected error from CodeGenerator, got nil")
	}
	if su.Code() != "" || su.Target() != "" {
		t.Errorf("generator failure must yield zero-value entity, got code=%q target=%q", su.Code(), su.Target())
	}
}

func TestBase62EncodeKnownValues(t *testing.T) {
	tests := []struct {
		name  string
		value uint64
		want  string
	}{
		{name: "zero", value: 0, want: "0"},
		{name: "nine", value: 9, want: "9"},
		{name: "ten", value: 10, want: "A"},
		{name: "thirty-five", value: 35, want: "Z"},
		{name: "thirty-six", value: 36, want: "a"},
		{name: "sixty-one", value: 61, want: "z"},
		{name: "sixty-two", value: 62, want: "10"},
		{name: "one hundred twenty-five", value: 125, want: "21"},
		{name: "three thousand eight hundred forty-four", value: 3844, want: "100"},
		{name: "very large", value: 1<<63 - 1, want: "AzL8n0Y58m7"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Base62Encode(tc.value)
			if got != tc.want {
				t.Errorf("Base62Encode(%d) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

// TestIsPossibleCode (S16): the domain owns the code-shape predicate —
// "could this string ever have been produced by Base62Encode?". The
// redirect slice consumes it instead of duplicating the alphabet.
func TestIsPossibleCode(t *testing.T) {
	tests := []struct {
		code string
		want bool
	}{
		{"0", true},
		{"10", true},
		{"abcXYZ019", true},
		{"", false},                // Base62Encode never returns empty
		{"-1", false},              // not in the alphabet
		{"abc def", false},         // space
		{"código", false},          // non-ASCII
		{"10/", false},             // path separator
		{"%20", false},             // percent-encoding
	}
	for _, tc := range tests {
		if got := IsPossibleCode(tc.code); got != tc.want {
			t.Errorf("IsPossibleCode(%q) = %v, want %v", tc.code, got, tc.want)
		}
	}
}

// TestIsPossibleCodeAcceptsEveryBase62Output (S16): every code the domain
// can produce must pass its own shape predicate — otherwise a consumer's
// fast path could reject a producible code.
func TestIsPossibleCodeAcceptsEveryBase62Output(t *testing.T) {
	values := []uint64{0, 1, 9, 10, 61, 62, 63, 3843, 3844, 1 << 20, ^uint64(0)}
	for _, v := range values {
		code := Base62Encode(v)
		if !IsPossibleCode(code) {
			t.Errorf("Base62Encode(%d) = %q rejected by IsPossibleCode", v, code)
		}
	}
}
