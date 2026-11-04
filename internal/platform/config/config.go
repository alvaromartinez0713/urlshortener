package config

import (
	"errors"
	"os"
)

// Config holds process-level settings. It is constructed only in the
// composition root (cmd/api); nothing in the business core depends on it.
type Config struct {
	Port        string
	DatabaseURL string
}

// Load reads configuration from the environment. Missing required values are
// an error: the process refuses to start half-configured.
func Load() (Config, error) {
	cfg := Config{
		Port:        envOr("PORT", "8080"),
		DatabaseURL: os.Getenv("DATABASE_URL"),
	}
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
