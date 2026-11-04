// Command api is the composition root: the only place where
// configuration, the HTTP adapter, and (later) slice dependencies are
// constructed and wired together. It contains no business logic.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"urlshortener/internal/api"
	"urlshortener/internal/platform/config"
	"urlshortener/internal/platform/postgres"
	"urlshortener/internal/redirect"
	"urlshortener/internal/shorten"
)

func main() {
	// S13: one structured logger for the whole process. JSON handler over
	// stdout: log aggregators and `jq` consume it without extra
	// dependencies, one record per line, and the standard library's
	// log/slog needs no third-party logging package (ADR-003).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("api: fatal", slog.String("error", err.Error()))
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// BaseURL is the public origin of generated short URLs (e.g.
	// "http://localhost:8080/hello" → "http://localhost:8080/<code>"). It
	// is read HERE, in the composition root, not in
	// internal/platform/config: config.go is outside this task's allowed
	// edit surfaces, and the composition root reading env for a
	// deployment setting is its documented job. It flows into the use
	// case as a plain string (see shorten.NewUseCase for why it is not a
	// port).
	baseURL := envOr("BASE_URL", "http://localhost:"+cfg.Port)

	// Database pool: established with a hard 5s budget — a database that
	// takes longer is down for startup purposes; fail fast and let the
	// supervisor restart.
	connectCtx, cancelConnect := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelConnect()
	pool, err := pgxpool.New(connectCtx, cfg.DatabaseURL)
	if err != nil {
		return errors.New("api: could not connect to database: " + err.Error())
	}
	defer pool.Close()

	if err := postgres.RunMigrations(connectCtx, pool); err != nil {
		return err
	}

	// Composition: the ONE postgres adapter implements every core-owned
	// port (shorten.Repository + shorturl.CodeGenerator + redirect.Finder);
	// they stay separate port types so the slices never know the
	// capabilities share a connection pool.
	adapter := postgres.New(pool)
	shortenUseCase := shorten.NewUseCase(adapter, adapter, baseURL)
	redirectUseCase := redirect.NewUseCase(adapter)

	router := api.NewRouter(logger)
	api.MountShorten(router, shorten.NewHandler(shortenUseCase))
	api.MountRedirect(router, redirect.NewHandler(redirectUseCase))

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           router,
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

	// Graceful shutdown on SIGINT/SIGTERM.
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

// envOr reads an env var with a fallback; duplicated locally because
// config.envOr is deliberately unexported to that package.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
