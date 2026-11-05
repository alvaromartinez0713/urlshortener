package config

import (
	"testing"
	"time"
)

// S14: behavior-only tests for the single env reader. They enter through the
// public Load() and assert the observable Config values — no internal
// coupling (ADR-002).
func TestLoad_BaseURL(t *testing.T) {
	tests := []struct {
		name    string
		port    string
		baseURL string // value of BASE_URL; empty means unset
		want    string
	}{
		{
			name: "default derived from default port",
			want: "http://localhost:8080",
		},
		{
			name: "default derived from custom port",
			port: "18099",
			want: "http://localhost:18099",
		},
		{
			name:    "explicit BASE_URL honored",
			baseURL: "https://short.example.com",
			want:    "https://short.example.com",
		},
		{
			name:    "explicit BASE_URL wins over custom port default",
			port:    "18099",
			baseURL: "http://example.test",
			want:    "http://example.test",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test") // required; not under test here
			if tt.port != "" {
				t.Setenv("PORT", tt.port)
			}
			if tt.baseURL != "" {
				t.Setenv("BASE_URL", tt.baseURL)
			}

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if cfg.BaseURL != tt.want {
				t.Errorf("BaseURL = %q, want %q", cfg.BaseURL, tt.want)
			}
		})
	}
}

// TestLoad_MissingDatabaseURL (S23): DATABASE_URL is required — the
// process refuses to start half-configured.
func TestLoad_MissingDatabaseURL(t *testing.T) {
	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil, want an error for missing DATABASE_URL")
	}
}

// TestLoad_PortValidation (S23): a garbage PORT must fail at CONFIG time,
// not later at Listen, where the failure would be detached from its cause.
func TestLoad_PortValidation(t *testing.T) {
	tests := []struct {
		name string
		port string
		want bool // want a validation error?
	}{
		{name: "default port", want: false},
		{name: "valid numeric port", port: "18099", want: false},
		{name: "port 1 is valid", port: "1", want: false},
		{name: "port 65535 is valid", port: "65535", want: false},
		{name: "garbage", port: "not-a-port", want: true},
		{name: "negative", port: "-1", want: true},
		{name: "zero is not a usable listen port", port: "0", want: true},
		{name: "above uint16 range", port: "65536", want: true},
		{name: "empty string falls back to default", port: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test")
			if tt.port != "" {
				t.Setenv("PORT", tt.port)
			} else {
				t.Setenv("PORT", "")
			}

			_, err := Load()
			if got := err != nil; got != tt.want {
				t.Errorf("Load() error = %v, want error: %v", err, tt.want)
			}
		})
	}
}

// TestLoad_CacheDefaults (S28): the in-memory cache settings default to
// sane values when the env vars are unset — the process must not depend on
// operators setting everything.
func TestLoad_CacheDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://test")
	t.Setenv("CACHE_TTL", "")
	t.Setenv("CACHE_MAX_ENTRIES", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CacheTTL != 5*time.Minute {
		t.Errorf("CacheTTL = %v, want 5m", cfg.CacheTTL)
	}
	if cfg.CacheMaxEntries != 10000 {
		t.Errorf("CacheMaxEntries = %d, want 10000", cfg.CacheMaxEntries)
	}
}

// TestLoad_CacheTTL (S28): a garbage CACHE_TTL must fail at CONFIG time
// (S23 principle applied to the new knob): an unparseable duration would
// otherwise surface as a mysterious clamp (or panic) deep in the cache
// adapter, detached from its cause.
func TestLoad_CacheTTL(t *testing.T) {
	tests := []struct {
		name    string
		ttl     string // value of CACHE_TTL; empty means unset
		want    time.Duration
		wantErr bool
	}{
		{name: "default 5m", want: 5 * time.Minute},
		{name: "seconds honored", ttl: "90s", want: 90 * time.Second},
		{name: "minutes honored", ttl: "2m", want: 2 * time.Minute},
		{name: "hour+minutes honored", ttl: "1h30m", want: 90 * time.Minute},
		{name: "empty falls back to default", ttl: "", want: 5 * time.Minute},
		{name: "unparseable", ttl: "five minutes", wantErr: true},
		{name: "bare number is not a duration", ttl: "300", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test")
			if tt.ttl != "" {
				t.Setenv("CACHE_TTL", tt.ttl)
			} else {
				t.Setenv("CACHE_TTL", "")
			}

			cfg, err := Load()
			if got := err != nil; got != tt.wantErr {
				t.Fatalf("Load() error = %v, want error: %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if cfg.CacheTTL != tt.want {
				t.Errorf("CacheTTL = %v, want %v", cfg.CacheTTL, tt.want)
			}
		})
	}
}

// TestLoad_CacheMaxEntries (S28): the cap must be a positive number —
// non-numeric, zero and negative values are garbage and fail at config
// time, never silently inside the adapter.
func TestLoad_CacheMaxEntries(t *testing.T) {
	tests := []struct {
		name       string
		maxEntries string // value of CACHE_MAX_ENTRIES; empty means unset
		want       int
		wantErr    bool
	}{
		{name: "default 10000", want: 10000},
		{name: "small cap honored", maxEntries: "250", want: 250},
		{name: "large cap honored", maxEntries: "999999", want: 999999},
		{name: "empty falls back to default", maxEntries: "", want: 10000},
		{name: "non-numeric", maxEntries: "lots", wantErr: true},
		{name: "zero is not a usable cap", maxEntries: "0", wantErr: true},
		{name: "negative", maxEntries: "-3", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://test")
			if tt.maxEntries != "" {
				t.Setenv("CACHE_MAX_ENTRIES", tt.maxEntries)
			} else {
				t.Setenv("CACHE_MAX_ENTRIES", "")
			}

			cfg, err := Load()
			if got := err != nil; got != tt.wantErr {
				t.Fatalf("Load() error = %v, want error: %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if cfg.CacheMaxEntries != tt.want {
				t.Errorf("CacheMaxEntries = %d, want %d", cfg.CacheMaxEntries, tt.want)
			}
		})
	}
}
