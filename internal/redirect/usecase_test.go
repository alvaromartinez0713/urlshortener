package redirect

import (
	"context"
	"errors"
	"testing"

	"urlshortener/internal/shorturl"
)

// fakeFinder is a deterministic Finder stub: FindByCode returns the
// configured target and error, and records call count so tests can prove
// the use case did or did not reach the port. Pure stdlib — proves the
// use case tests run without Docker or network (S5.5).
type fakeFinder struct {
	target string
	err    error
	calls  int
}

func (f *fakeFinder) FindByCode(ctx context.Context, code string) (string, error) {
	f.calls++
	return f.target, f.err
}

func TestRedirectSuccessReturnsStoredTarget(t *testing.T) {
	// A code that matches a row resolves to the stored long URL, exactly
	// as persisted (S2: GET /:code redirects to the target).
	finder := &fakeFinder{target: "https://example.com/a/b?q=1#frag"}
	uc := NewUseCase(finder)

	target, err := uc.Redirect(context.Background(), "10")

	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if target != "https://example.com/a/b?q=1#frag" {
		t.Errorf("target = %q, want the stored long URL unchanged", target)
	}
	if finder.calls != 1 {
		t.Errorf("FindByCode calls = %d, want 1", finder.calls)
	}
}

func TestRedirectUnknownCodeIsErrNotFound(t *testing.T) {
	// A code that matches no row is ErrNotFound (matched via errors.Is),
	// which the handler maps to 404 (S2).
	finder := &fakeFinder{err: ErrNotFound}
	uc := NewUseCase(finder)

	target, err := uc.Redirect(context.Background(), "zzz")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Redirect error = %v, want errors.Is ErrNotFound", err)
	}
	if target != "" {
		t.Errorf("target = %q, want empty on failure", target)
	}
}

func TestRedirectInfraErrorPropagates(t *testing.T) {
	// Infrastructure failures from the port propagate as-is (S5.3): the
	// use case must not mask a database outage as a client error.
	infraErr := errors.New("connection refused")
	finder := &fakeFinder{err: infraErr}
	uc := NewUseCase(finder)

	target, err := uc.Redirect(context.Background(), "10")

	if !errors.Is(err, infraErr) {
		t.Fatalf("Redirect error = %v, want errors.Is %v", err, infraErr)
	}
	if target != "" {
		t.Errorf("target = %q, want empty on failure", target)
	}
}

func TestRedirectImpossibleCodeRejectedWithoutPortCall(t *testing.T) {
	// The set of existing codes is exactly the set of Base62Encode
	// outputs (S3): non-empty strings over [0-9A-Za-z]. A code outside
	// that alphabet can never have a row, so the use case rejects it as
	// ErrNotFound WITHOUT a port call — a correct, cheap fast path that
	// keeps garbage probes off the database. The decision lives in the
	// use case (S5.3); the adapter stays a dumb translator.
	tests := []struct {
		name string
		code string
	}{
		{name: "empty", code: ""},
		{name: "non-base62 char", code: "ab-c"},
		{name: "dot", code: "a.b"},
		{name: "slash", code: "a/b"},
		{name: "unicode", code: "cód"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			finder := &fakeFinder{}
			uc := NewUseCase(finder)

			target, err := uc.Redirect(context.Background(), tc.code)

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Redirect(%q) error = %v, want errors.Is ErrNotFound", tc.code, err)
			}
			if target != "" {
				t.Errorf("target = %q, want empty on failure", target)
			}
			if finder.calls != 0 {
				t.Errorf("FindByCode calls = %d, want 0 (impossible codes must not reach the port)", finder.calls)
			}
		})
	}
}

func TestRedirectPossibleCodesExactlyMatchBase62Outputs(t *testing.T) {
	// Consistency guard between the use case's cheap-rejection rule and
	// the domain's code alphabet (S3): every code the domain can produce
	// must pass the possible-code check — otherwise the fast path could
	// 404 a code that exists. This cross-checks without exporting the
	// domain's (unexported) alphabet constant.
	uc := NewUseCase(&fakeFinder{})

	values := []uint64{0, 1, 9, 10, 61, 62, 63, 3843, 3844, 1 << 20, ^uint64(0)}
	for _, v := range values {
		code := shorturl.Base62Encode(v)
		if _, err := uc.Redirect(context.Background(), code); err == ErrNotFound {
			t.Errorf("Base62Encode(%d) = %q rejected as impossible; the fast path would 404 a producible code", v, code)
		}
	}
}
