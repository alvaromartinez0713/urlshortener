package cache

import (
	"context"
	"testing"
	"time"

	"urlshortener/internal/redirect"
	"urlshortener/internal/shorturl"
)

// Unit tests for the in-memory redirect.Cache adapter (S28). Pure stdlib +
// the two core packages the adapter is allowed to import — no Docker, no
// network (S5.5). Assertions are behavior-only (ADR-002): everything enters
// through the public Cache port (Get/Put) and asserts observable outcomes —
// what Get reports as found or not — never internal map state.
func mustEntity(t *testing.T, code, target string) shorturl.ShortURL {
	t.Helper()
	su, err := shorturl.NewShortURLFromRepository(code, target)
	if err != nil {
		t.Fatalf("NewShortURLFromRepository(%q, %q) error = %v", code, target, err)
	}
	return su
}

// TestMemory_SetThenGet_Hit: a stored code is returned with found=true,
// carrying the exact entity that was Put (the immutable v1 code/target pair).
func TestMemory_SetThenGet_Hit(t *testing.T) {
	c := NewMemory(time.Minute, 10)
	ctx := context.Background()
	want := mustEntity(t, "abc", "https://example.com/hello")

	if err := c.Put(ctx, "abc", want); err != nil {
		t.Fatalf("Put() error = %v, want nil", err)
	}

	got, found, err := c.Get(ctx, "abc")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if !found {
		t.Fatal("Get() found = false, want true")
	}
	if got.Code() != want.Code() || got.Target() != want.Target() {
		t.Errorf("Get() = (%q, %q), want (%q, %q)", got.Code(), got.Target(), want.Code(), want.Target())
	}
}

// TestMemory_UnknownKey_Miss: a code that was never stored reports
// found=false (and no error) — the miss that sends the use case to the
// Finder (S26).
func TestMemory_UnknownKey_Miss(t *testing.T) {
	c := NewMemory(time.Minute, 10)

	_, found, err := c.Get(context.Background(), "never-stored")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if found {
		t.Fatal("Get() found = true, want false for an unknown code")
	}
}

// TestMemory_TTLExpiry: an entry whose TTL elapsed reports found=false on
// read — the lazy expiry of S28. The entry is also deleted from memory on
// that read, which is observable only as "it stays a miss", asserted here
// by reading again.
//
// Timing tolerance: TTL is 5ms and the sleep is 50ms — a 10x margin, so the
// test only fails if the machine stalls for ~45ms between Put and Get,
// which is orders of magnitude above normal scheduler jitter. The
// asymmetry (huge sleep vs tiny TTL) keeps the test fast (S12) and stable.
func TestMemory_TTLExpiry(t *testing.T) {
	c := NewMemory(5*time.Millisecond, 10)
	ctx := context.Background()
	c.Put(ctx, "abc", mustEntity(t, "abc", "https://example.com/expired"))

	time.Sleep(50 * time.Millisecond)

	_, found, err := c.Get(ctx, "abc")
	if err != nil {
		t.Fatalf("Get() error = %v, want nil", err)
	}
	if found {
		t.Fatal("Get() found = true, want false for an expired entry")
	}

	// The expired entry was lazily deleted: it stays a miss, forever.
	_, found, _ = c.Get(ctx, "abc")
	if found {
		t.Fatal("Get() found = true after a prior lazy delete, want false")
	}
}

// TestMemory_CapEviction: with the cache full, storing one more code evicts
// exactly one entry (S28: max-entries cap). WHICH code is evicted is random
// (documented in memory.go: Go's randomized map iteration as the eviction
// pick), so the assertion counts Get outcomes — observable behavior, like a
// real caller would see — instead of pinning a survivor identity: with a
// cap of 2 and 3 distinct codes, exactly 2 Get calls hit and exactly 1
// misses.
func TestMemory_CapEviction(t *testing.T) {
	c := NewMemory(time.Minute, 2)
	ctx := context.Background()
	codes := []string{"aaa", "bbb", "ccc"}
	for _, code := range codes {
		if err := c.Put(ctx, code, mustEntity(t, code, "https://example.com/"+code)); err != nil {
			t.Fatalf("Put(%q) error = %v", code, err)
		}
	}

	hits, misses := 0, 0
	for _, code := range codes {
		_, found, _ := c.Get(ctx, code)
		if found {
			hits++
		} else {
			misses++
		}
	}
	if hits != 2 || misses != 1 {
		t.Errorf("after capping 3 codes into a 2-entry cache: hits = %d, misses = %d, want 2 hits and 1 miss", hits, misses)
	}
}

// TestMemory_CapPrefersEvictingExpired: at the cap, Put first evicts EXPIRED
// entries before touching live ones (S28 eviction order). Entry "old" is put,
// left to expire, then two fresh puts fill the cap — the sweep makes room, so
// both fresh codes survive and only the expired one misses.
func TestMemory_CapPrefersEvictingExpired(t *testing.T) {
	c := NewMemory(5*time.Millisecond, 2)
	ctx := context.Background()

	c.Put(ctx, "old", mustEntity(t, "old", "https://example.com/old"))
	time.Sleep(50 * time.Millisecond) // "old" is now expired

	c.Put(ctx, "new1", mustEntity(t, "new1", "https://example.com/new1"))
	c.Put(ctx, "new2", mustEntity(t, "new2", "https://example.com/new2"))

	_, found := checkGet(t, c, ctx, "old")
	if found {
		t.Fatal("expired 'old' survived a cap-pressure Put, want it evicted first")
	}
	_, found = checkGet(t, c, ctx, "new1")
	if !found {
		t.Error("'new1' was evicted although an expired entry could have made room")
	}
	_, found = checkGet(t, c, ctx, "new2")
	if !found {
		t.Error("'new2' was evicted although an expired entry could have made room")
	}
}

// checkGet wraps Get for the few tests that only need the found bit.
func checkGet(t *testing.T, c redirect.Cache, ctx context.Context, code string) (shorturl.ShortURL, bool) {
	t.Helper()
	su, found, err := c.Get(ctx, code)
	if err != nil {
		t.Fatalf("Get(%q) error = %v", code, err)
	}
	return su, found
}
