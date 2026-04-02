package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/handler"
)

//go:embed migrations/*.sql
var migrations embed.FS

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	return runWithContext(ctx)
}

// runWithContext is separated from run() so integration tests can pass a cancellable context.
// The ctx controls the shutdown signal only — long-lived resources are managed by Backend.
func runWithContext(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// App migrations run before Backend (schema must exist for pool/River).
	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// Backend owns pool + River lifecycle.
	b, err := handler.NewBackend(cfg)
	if err != nil {
		return fmt.Errorf("create backend: %w", err)
	}
	defer b.Close()

	mux := http.NewServeMux()
	b.RegisterRoutes(mux)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: mux,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("server starting", "port", cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		slog.Info("shutting down")
		shutdownTimeout := time.Duration(cfg.Server.ShutdownTimeoutSec) * time.Second
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer shutdownCancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			slog.Warn("http shutdown error", "error", err)
		}
		slog.Info("http server stopped")
		// b.Close() runs via defer: stops River, then closes pool.
		return nil
	}
}

func runMigrations(databaseURL string) error {
	d, err := iofs.New(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("create migration source: %w", err)
	}

	trimmed := strings.TrimPrefix(databaseURL, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	if err != nil {
		return fmt.Errorf("create migrate: %w", err)
	}

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}

	version, dirty, _ := m.Version()
	slog.Info("migrations complete", "version", version, "dirty", dirty)
	return nil
}
