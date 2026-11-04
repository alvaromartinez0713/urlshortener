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
