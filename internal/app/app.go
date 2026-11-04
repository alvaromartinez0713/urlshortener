// Package app is the application bootstrap: it owns every construction and
// wiring step of the process — logger, configuration, database pool,
// migrations, adapter + use case composition, router assembly, server
// lifecycle and graceful shutdown (S14). It contains no business logic; it
// is the composition root that cmd/api's minimal main simply invokes.
package app

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
	// S13: one structured logger for the whole process. JSON handler over
	// stdout: log aggregators and `jq` consume it without extra
	// dependencies, one record per line, and the standard library's
	// log/slog needs no third-party logging package (ADR-003).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Config.BaseURL (S14) now carries the public origin of short URLs;
	// it is read only in internal/platform/config and flows into the use
	// case as a plain string (see shorten.NewUseCase for why it is not a
	// port).

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
	shortenUseCase := shorten.NewUseCase(adapter, adapter, cfg.BaseURL)
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
