package config

import (
	"testing"
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
