// Package cache is the in-memory Cache adapter of the redirect slice (S28,
// it.3): a TTL-bounded map with a max-entries cap, implementing the
// core-owned redirect.Cache port (S26).
//
// Import direction: like internal/platform/postgres (S5.2/S5.3), this
// adapter imports the core packages it translates for (internal/redirect
// for the port, internal/shorturl for the entity) — the adapter-to-core
// direction is the allowed one; the core never imports this package, it
// only knows the port.
//
// S28 insight, spelled out: codes are immutable in v1 (S2) — a code, once
// assigned, always redirects to the same target. Therefore the TTL bounds
// MEMORY, not consistency: an entry is stale only in the sense of being
// old, never in the sense of being wrong. There is no invalidation and
// none is needed; when Redis arrives in a later iteration (L20) it will
// implement the SAME port, and if v2 ever makes codes mutable, THAT is
// when invalidation enters this boundary.
package cache

import (
	"context"
	"sync"
	"time"

	"urlshortener/internal/redirect"
	"urlshortener/internal/shorturl"
)

// Defaults used by NewMemory when a caller passes a non-positive value.
// They mirror the config package's own defaults (CACHE_TTL=5m,
// CACHE_MAX_ENTRIES=10000) so both layers agree without importing each
// other.
const (
	// DefaultTTL is the entry expiry applied for ttl <= 0.
	DefaultTTL = 5 * time.Minute
	// DefaultMaxEntries is the capacity applied for maxEntries <= 0.
	DefaultMaxEntries = 10000
)

// Compile-time proof the adapter satisfies the core-owned port (S26): if
// redirect.Cache grows or changes, this line breaks the build here, where
// the translation lives — not in the use case.
var _ redirect.Cache = (*Memory)(nil)

// Memory is the in-memory Cache adapter. All exported methods are safe for
// concurrent use.
//
// Concurrency pick — RWMutex + map, not sync.Map: sync.Map is optimized for
// two narrower regimes (write-once/read-many key sets, or disjoint key
// sets per goroutine). This cache's regime is "read many, overwrite
// occasionally, and EVICT by scanning under the cap" — the eviction sweep
// needs to range over the whole map while excluding concurrent writers,
// which sync.Map offers no primitive for (Range provides no mutual
// exclusion). A plain map behind sync.RWMutex is type-safe, trivial to
// reason about, and its read lock is uncontended-cheap at this scale.
type Memory struct {
	mu      sync.Mutex
	ttl     time.Duration
	max     int
	entries map[string]entry
}

// entry is one cached code. The stored ShortURL is immutable (S2), so only
// the expiry ever "ages".
type entry struct {
	su        shorturl.ShortURL
	expiresAt time.Time
}

// NewMemory builds the in-memory Cache over a TTL and a max-entries cap.
//
// Defensive defaults, documented choice — CLAMP, don't error: a non-positive
// ttl or maxEntries is replaced with DefaultTTL / DefaultMaxEntries instead
// of returning an error or panicking. Why: the deployment knob is already
// guarded where it belongs — the single env reader (S14) fails Load on
// garbage CACHE_TTL / CACHE_MAX_ENTRIES — so a constructor error would be
// unreachable in the wired path (internal/app, T28) yet force every other
// caller into error ceremony. The cache is a best-effort accelerator (S27:
// a degraded cache degrades performance, never availability); clamping keeps
// the adapter total and panic-free, which is the honest shape for a
// component that must never be able to break redirect construction.
func NewMemory(ttl time.Duration, maxEntries int) *Memory {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if maxEntries <= 0 {
		maxEntries = DefaultMaxEntries
	}
	return &Memory{
		ttl:     ttl,
		max:     maxEntries,
		entries: make(map[string]entry),
	}
}

// Get returns the entity stored under code (redirect.Cache).
//
// Semantics: found=true with the live entry; found=false for an unknown
// code AND for an expired one. Expired entries are deleted LAZILY, on
// read — no background sweeper goroutine (no clock in the core, no
// goroutine lifecycle to manage, S5.1-friendly); memory is reclaimed as
// soon as the code is probed again, and the cap sweep (Put) is the second
// reclamation point. Under v1 immutability (S2) a lazily-served-until-
// probed expired entry would even be harmless; deletion only bounds memory.
//
// Error result: always nil. An in-process map lookup cannot fail; the error
// result exists for engines that can (Redis over a network), and S27's
// degrade path is theirs to exercise.
//
// Context: accepted for the port but unused — the operation is an in-process
// map access with no cancellation or deadline to honor.
func (m *Memory) Get(ctx context.Context, code string) (shorturl.ShortURL, bool, error) {
	// Whole operation under the WRITE lock (S29/R3-get-expired-race-extra-miss):
	// the earlier RLock-then-upgrade design left a window between the two
	// lock acquisitions in which a concurrent Put could REPLACE an expired
	// entry with a live one — the re-check saw it live, skipped the delete,
	// and still reported a miss: a spurious miss with a fresh value present.
	// One lock acquisition makes that interleaving structurally impossible.
	// The RWMutex read fast path was optimizing a map access that is
	// nanoseconds against a multi-millisecond DB round-trip — not worth the
	// race it introduced.
	m.mu.Lock()
	defer m.mu.Unlock()

	e, ok := m.entries[code]
	if !ok {
		return shorturl.ShortURL{}, false, nil
	}
	if time.Now().After(e.expiresAt) {
		// Lazy expiry: delete under the same lock that observed the
		// expiry — no window for a concurrent replacement.
		delete(m.entries, code)
		return shorturl.ShortURL{}, false, nil
	}
	return e.su, true, nil
}

// Put stores the entity under code until now+ttl (redirect.Cache).
//
// Cap policy (S28: max-entries cap, eviction order documented): when the
// map is already at maxEntries and code is NOT already present, room is
// made BEFORE storing —
//
//  1. expired entries are swept first (they are pure dead weight under v1
//     immutability, S28); then
//  2. if still full, ONE live entry is evicted AT RANDOM. Random eviction
//     is a deliberate, documented choice for v1: it needs no metadata (no
//     recency/frequency bookkeeping), is O(1), and for a cache whose only
//     job is shaving DB reads off an immutable mapping, the cost of
//     evicting a hot code is one extra Finder hit. Go's map iteration
//     order is deliberately randomized, so `for k := range` + delete-first
//     IS a random pick — no extra state, no policy to drift.
//
// Overwriting an existing key never evicts anything: the replacement
// occupies the slot the old entry already held.
//
// Error result: always nil (same rationale as Get). Context: accepted for
// the port, unused (in-process write).
func (m *Memory) Put(ctx context.Context, code string, su shorturl.ShortURL) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.entries[code]; !exists && len(m.entries) >= m.max {
		m.makeRoomLocked()
	}
	m.entries[code] = entry{su: su, expiresAt: time.Now().Add(m.ttl)}
	return nil
}

// makeRoomLocked brings len(entries) below max so one insert fits. Caller
// holds m.mu for writing.
func (m *Memory) makeRoomLocked() {
	// Pass 1: sweep every expired entry.
	now := time.Now()
	for code, e := range m.entries {
		if now.After(e.expiresAt) {
			delete(m.entries, code)
		}
	}
	// Pass 2: STILL full after the sweep → random eviction (see Put's
	// cap policy). Only reached when the sweep freed nothing.
	if len(m.entries) >= m.max {
		for code := range m.entries {
			delete(m.entries, code)
			break // exactly one slot is needed: we evict before inserting one
		}
	}
}
