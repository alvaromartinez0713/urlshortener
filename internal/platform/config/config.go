package config

import (
	"errors"
	"os"
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
}

// Load reads configuration from the environment. Missing required values are
// an error: the process refuses to start half-configured.
func Load() (Config, error) {
	cfg := Config{
		Port:        envOr("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}
	cfg.BaseURL = envOr("BASE_URL", "http://localhost:"+cfg.Port)
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("config: DATABASE_URL is required")
	}
	return cfg, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
