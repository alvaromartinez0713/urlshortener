package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

// Config holds process-level settings. It is constructed only in the
// composition root (via internal/app); nothing in the business core depends
// on it.
//
// S14: this package is the SINGLE environment reader of the process. No
// other package — the composition root included — may call os.Getenv or
// re-implement env fallbacks; every deployment setting enters through
// Config.Load and flows down as plain values.
type Config struct {
	Port        string
	DatabaseURL string
	// BaseURL is the public origin of generated short URLs (e.g.
	// "http://localhost:8080/hello" → "http://localhost:8080/<code>").
	// It defaults to "http://localhost:"+Port; the default depends on
	// Port, so it is built here in Load, the one place that sees both.
	BaseURL string
	// CacheTTL is the expiry of in-memory redirect-cache entries (S28):
	// CACHE_TTL, a Go duration string ("5m", "90s", ...), default 5m.
	// Codes are immutable in v1 (S2), so the TTL bounds MEMORY, not
	// consistency — no invalidation exists or is needed.
	CacheTTL time.Duration
	// CacheMaxEntries is the hard cap on cached codes (S28):
	// CACHE_MAX_ENTRIES, a positive integer, default 10000. Zero and
	// negative caps are garbage (the cache could hold nothing) and fail
	// Load — the same fail-early rule as PORT (S23).
	CacheMaxEntries int
}

// Load reads configuration from the environment. Missing required values are
// an error: the process refuses to start half-configured (S23: also a
// malformed PORT — failing at Listen would detach the failure from its
// cause).
func Load() (Config, error) {
	port := envOr("PORT", "8080")
	if err := validatePort(port); err != nil {
		return Config{}, err
	}

	cfg := Config{
		Port:        port,
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}
	cfg.BaseURL = envOr("BASE_URL", "http://localhost:"+cfg.Port)
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: DATABASE_URL is required")
	}

	// Cache knobs (S28) come from the same single env reader (S14).
	// S29: a non-positive TTL is a config error, not a silently clamped
	// value — the same fail-at-config-time rule as PORT (S23).
	ttl, err := time.ParseDuration(envOr("CACHE_TTL", "5m"))
	if err != nil {
		return Config{}, fmt.Errorf("config: CACHE_TTL is not a valid duration: %w", err)
	}
	if ttl <= 0 {
		return Config{}, fmt.Errorf("config: CACHE_TTL %q must be positive", ttl)
	}
	maxEntries, err := strconv.Atoi(envOr("CACHE_MAX_ENTRIES", "10000"))
	if err != nil {
		return Config{}, fmt.Errorf("config: CACHE_MAX_ENTRIES is not a number")
	}
	if maxEntries <= 0 {
		return Config{}, fmt.Errorf("config: CACHE_MAX_ENTRIES %d must be positive", maxEntries)
	}
	cfg.CacheTTL = ttl
	cfg.CacheMaxEntries = maxEntries

	return cfg, nil
}

// validatePort accepts a usable TCP listen port: a decimal integer in
// [1, 65535]. Port 0 would make the OS assign a random port — fine for
// ephemeral services, wrong for a configured deployment whose BaseURL
// must match, so it is rejected.
func validatePort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil {
		return fmt.Errorf("config: PORT %q is not a number", port)
	}
	if n < 1 || n > 65535 {
		return fmt.Errorf("config: PORT %q is outside the valid range 1-65535", port)
	}
	return nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
