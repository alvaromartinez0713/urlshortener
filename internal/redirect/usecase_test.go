package redirect

import (
	"context"
	"errors"
	"testing"

	"urlshortener/internal/shorturl"
)

// fakeFinder is a deterministic Finder fake: FindByCode returns the
// configured rehydrated entity and error. It holds no call counter —
// interactions are asserted through behavior (what the use case returns
// given what the fake would answer), never through spying on the port
// (ADR-002). Pure stdlib — proves the use case tests run without Docker
// or network (S5.5).
type fakeFinder struct {
	su  shorturl.ShortURL
	err error
}

func (f *fakeFinder) FindByCode(ctx context.Context, code string) (shorturl.ShortURL, error) {
	return f.su, f.err
}

// mustRehydrate builds a stored entity through the domain's trusted
// rehydration constructor (S11), failing the test if it refuses.
func mustRehydrate(t *testing.T, code, target string) shorturl.ShortURL {
	t.Helper()
	su, err := shorturl.NewShortURLFromRepository(code, target)
	if err != nil {
		t.Fatalf("rehydrate %q -> %q: %v", code, target, err)
	}
	return su
}

func TestRedirectSuccessReturnsStoredEntity(t *testing.T) {
	// S11: the read port returns the ShortURL entity, not a raw string.
	// A code that matches a row resolves to the rehydrated entity whose
	// Target() is the stored long URL exactly as persisted (S2: GET
	// /:code redirects to the target).
	want := "https://example.com/a/b?q=1#frag"
	finder := &fakeFinder{su: mustRehydrate(t, "10", want)}
	uc := NewUseCase(finder)

	su, err := uc.Redirect(context.Background(), "10")

	if err != nil {
		t.Fatalf("Redirect unexpected error: %v", err)
	}
	if su.Target() != want {
		t.Errorf("Target() = %q, want the stored long URL unchanged", su.Target())
	}
	if su.Code() != "10" {
		t.Errorf("Code() = %q, want the looked-up code", su.Code())
	}
}

func TestRedirectUnknownCodeIsErrNotFound(t *testing.T) {
	// A code that matches no row is ErrNotFound (matched via errors.Is),
	// which the handler maps to 404 (S2).
	finder := &fakeFinder{err: ErrNotFound}
	uc := NewUseCase(finder)

	su, err := uc.Redirect(context.Background(), "zzz")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Redirect error = %v, want errors.Is ErrNotFound", err)
	}
	if su != (shorturl.ShortURL{}) {
		t.Errorf("entity = %+v, want the zero value on failure", su)
	}
}

func TestRedirectInfraErrorPropagates(t *testing.T) {
	// Infrastructure failures from the port propagate as-is (S5.3): the
	// use case must not mask a database outage as a client error.
	infraErr := errors.New("connection refused")
	finder := &fakeFinder{err: infraErr}
	uc := NewUseCase(finder)

	su, err := uc.Redirect(context.Background(), "10")

	if !errors.Is(err, infraErr) {
		t.Fatalf("Redirect error = %v, want errors.Is %v", err, infraErr)
	}
	if su != (shorturl.ShortURL{}) {
		t.Errorf("entity = %+v, want the zero value on failure", su)
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
			// "The port is not reached" is asserted by BEHAVIOR, not by a
			// call counter (ADR-002): the fake finder is configured to
			// SUCCEED if it were reached, so only the use case's fast path
			// can produce ErrNotFound for an impossible code.
			finder := &fakeFinder{su: mustRehydrate(t, "10", "PORT-MUST-NOT-BE-REACHED")}
			uc := NewUseCase(finder)

			su, err := uc.Redirect(context.Background(), tc.code)

			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("Redirect(%q) error = %v, want errors.Is ErrNotFound", tc.code, err)
			}
			if su != (shorturl.ShortURL{}) {
				t.Errorf("entity = %+v, want the zero value on failure", su)
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
