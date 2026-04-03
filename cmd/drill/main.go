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
	"github.com/gorilla/csrf"
	stripe "github.com/stripe/stripe-go/v82"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"

	drill "github.com/btc/drill"
	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/billing"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/handler"
)

//go:embed migrations/*.sql
var migrations embed.FS

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %s\n", err)
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

	// Structured logger with trace correlation (must be after config load for GCPProjectID).
	jsonHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})
	logger := slog.New(drilotel.NewTraceHandler(jsonHandler, cfg.Otel.GCPProjectID))
	slog.SetDefault(logger)

	if err := runMigrations(cfg.Database.URL); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	// OTel providers (must be before Backend so pool tracer is active).
	providers, err := drilotel.Init(&cfg.Otel)
	if err != nil {
		return fmt.Errorf("init otel: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := providers.Shutdown(shutdownCtx); err != nil {
			slog.Warn("otel shutdown error", "error", err)
		}
	}()

	// Initialize Stripe — set API key once (package-level global, must not be set per-request).
	if cfg.Stripe.SecretKey != "" {
		stripe.Key = cfg.Stripe.SecretKey
		billing.SetProPriceID(cfg.Stripe.ProPriceID)
		billing.SetPackPriceIDs(cfg.Stripe.Pack120PriceID, cfg.Stripe.Pack300PriceID, cfg.Stripe.Pack600PriceID)
		slog.Info("stripe configured")
	} else {
		slog.Warn("stripe not configured — billing endpoints will return errors")
	}

	b, err := backend.New(cfg)
	if err != nil {
		return fmt.Errorf("create backend: %w", err)
	}
	defer b.Close()

	oauthStateKey := auth.DeriveKey(cfg.Auth.TokenSecret, "oauth-state")
	auth.SetupGothProviders(&cfg.OAuth, cfg.Auth.BaseURL, oauthStateKey)

	mux := http.NewServeMux()
	handler.RegisterRoutes(mux, b)
	mux.Handle("/", handler.SPAHandler(drill.WebFS))

	csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
	csrfMiddleware := csrf.Protect(
		csrfKey,
		csrf.Secure(cfg.Auth.SecureCookies()),
		csrf.HttpOnly(false),
		csrf.CookieName("drill_csrf"),
		csrf.Path("/"),
		csrf.SameSite(csrf.SameSiteLaxMode),
	)

	otelHandler := otelhttp.NewMiddleware("drill")(mux)
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Server.Port),
		Handler: csrfMiddleware(otelHandler),
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
