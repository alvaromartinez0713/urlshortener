// Command api is the composition root: the only place where
// configuration, the HTTP adapter, and (later) slice dependencies are
// constructed and wired together. It contains no business logic.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"urlshortener/internal/api"
	"urlshortener/internal/platform/config"
	"urlshortener/internal/platform/postgres"
	"urlshortener/internal/shorten"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
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

	// Composition: the ONE postgres adapter implements both core-owned
	// ports (shorten.Repository + shorturl.CodeGenerator); they stay
	// separate port types so the slices never know the two capabilities
	// share a connection pool.
	adapter := postgres.New(pool)
	useCase := shorten.NewUseCase(adapter, adapter, baseURL)

	router := api.NewRouter()
	api.MountShorten(router, shorten.NewHandler(useCase))

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
		log.Printf("api listening on %s", srv.Addr)
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

	log.Println("shutting down")
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
