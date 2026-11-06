// Package app is the application bootstrap: it owns every construction and
// wiring step of the process — logger, configuration, database pool,
// migrations, adapter + use case composition, router assembly, server
// lifecycle and graceful shutdown (S14). It contains no business logic; it
// is the composition root that cmd/api's minimal main simply invokes.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"urlshortener/internal/api"
	"urlshortener/internal/platform/cache"
	"urlshortener/internal/platform/config"
	"urlshortener/internal/platform/postgres"
	"urlshortener/internal/redirect"
	"urlshortener/internal/shorten"
)

// Run bootstraps the whole application and blocks until the server stops.
// It takes no context parameter: the process owns its own lifetime — it
// listens for SIGINT/SIGTERM itself and shuts the server down gracefully,
// and the 5s database connect budget creates its own deadline context
// below. Callers only need the terminal outcome (error or nil), which is
// exactly what the minimal main in cmd/api consumes.
//
// Logging: Run's FIRST step is to build the structured JSON logger and
// install it as the process default with slog.SetDefault. This guarantees
// that cmd/api's fatal log line (which runs after Run returns an error)
// already uses the ADR-003 JSON logger, even when the error is a config
// failure that happens before anything else could log. It also makes
// slog.Default() usable by anything deeper in the process.
func Run() error {
	logger := newLogger()

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	pool, err := connectDatabase(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	router := buildRouter(logger, pool, cfg.BaseURL, cfg.CacheTTL, cfg.CacheMaxEntries)
	return serve(logger, router, cfg.Port)
}

// newLogger builds the structured JSON logger and installs it as the
// process default (S13, ADR-003). It is Run's FIRST step so cmd/api's
// fatal log line already uses the JSON logger even on early failures.
func newLogger() *slog.Logger {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
	return logger
}

// connectDatabase owns the DATABASE PHASE (S20): connect with a hard 5s
// budget (a database that takes longer is down for startup purposes —
// fail fast and let the supervisor restart), then migrations under their
// OWN deadline. One shared context was a real bug: a database that
// connected at 4.9s left migrations a ~0.1s budget and spurious startup
// failures. Wrapping uses %w so callers can errors.Is/As through it (S20).
func connectDatabase(databaseURL string) (*pgxpool.Pool, error) {
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelConnect()
	pool, err := postgres.Connect(connectCtx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("api: could not connect to database: %w", err)
	}

	migrateCtx, cancelMigrate := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelMigrate()
	if err := postgres.RunMigrations(migrateCtx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("api: could not run migrations: %w", err)
	}
	return pool, nil
}

// buildRouter composes the application: the ONE postgres adapter
// implements every core-owned port (shorten.Repository +
// shorturl.CodeGenerator + redirect.Finder); they stay separate port
// types so the slices never know the capabilities share a connection
// pool (S5.2). The redirect cache (S26/S28) is ONE cache.Memory built
// here from the config knobs — the composition root is the single place
// that decides WHICH cache engine backs the redirect.Cache port; the use
// case only knows THAT a fast source exists.
func buildRouter(logger *slog.Logger, pool *pgxpool.Pool, baseURL string, cacheTTL time.Duration, cacheMaxEntries int) http.Handler {
	adapter := postgres.New(pool)
	cacheAdapter := cache.NewMemory(cacheTTL, cacheMaxEntries)
	shortenUseCase := shorten.NewUseCase(adapter, adapter)
	redirectUseCase := redirect.NewUseCase(adapter, cacheAdapter, logger)

	router := api.NewRouter(logger)
	api.MountShorten(router, shorten.NewHandler(shortenUseCase, baseURL))
	api.MountRedirect(router, redirect.NewHandler(redirectUseCase))
	return router
}

// serve runs the HTTP server until SIGINT/SIGTERM or a listen error, and
// shuts down gracefully within a 10s budget.
func serve(logger *slog.Logger, handler http.Handler, port string) error {
	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("api listening", slog.String("addr", srv.Addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return err
	case <-stop:
	}

	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
