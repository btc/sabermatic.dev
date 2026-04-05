// Package testutil provides shared test helpers for integration tests that need
// a real Postgres database, backend, and authenticated sessions.
package testutil

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/handler"
	"github.com/btc/drill/internal/ratelimit"
	migrations "github.com/btc/drill/sql/migrations"
)

// TestRateLimiters returns permissive rate limiters suitable for tests.
// High burst values prevent tests from hitting rate limits unintentionally.
func TestRateLimiters() *handler.RateLimiters {
	return &handler.RateLimiters{
		Auth: ratelimit.NewLimiter(ratelimit.Config{
			Rate:       1000,
			Burst:      1000,
			MaxEntries: 10000,
		}),
		User: ratelimit.NewLimiter(ratelimit.Config{
			Rate:       1000,
			Burst:      1000,
			MaxEntries: 10000,
		}),
	}
}

// MustRegisterRoutes calls handler.RegisterRoutes with permissive test rate
// limiters and fails the test on error.
func MustRegisterRoutes(t *testing.T, mux *http.ServeMux, b *backend.Backend) {
	t.Helper()
	require.NoError(t, handler.RegisterRoutes(mux, b, TestRateLimiters()))
}

// StartPostgres starts a Postgres 16 container, runs app migrations, and
// returns the connection string. The container is terminated on test cleanup.
func StartPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("drill_test"),
		postgres.WithUsername("test"),
		postgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}

	// Run migrations using the embedded FS — works regardless of test CWD.
	d, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("create migration source: %v", err)
	}

	trimmed := strings.TrimPrefix(connStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	if err != nil {
		t.Fatalf("create migrate: %v", err)
	}
	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	srcErr, dbErr := m.Close()
	if srcErr != nil {
		t.Fatalf("close migration source: %v", srcErr)
	}
	if dbErr != nil {
		t.Fatalf("close migration db: %v", dbErr)
	}

	return connStr
}

// LoadTestConfig loads a config.Config suitable for integration tests.
func LoadTestConfig(t *testing.T, databaseURL string) *config.Config {
	t.Helper()
	t.Setenv("DATABASE_URL", databaseURL)
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	t.Setenv("AUTH_BCRYPT_COST", "4")
	cfg, err := config.Load()
	require.NoError(t, err)
	return cfg
}

// NewTestBackend starts Postgres, creates a real Backend (with pool + River),
// and returns it. The Backend is closed on test cleanup.
func NewTestBackend(t *testing.T) *backend.Backend {
	t.Helper()
	connStr := StartPostgres(t)
	cfg := LoadTestConfig(t, connStr)

	b, err := backend.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() })

	return b
}

// SignupAndLogin creates a user via HTTP signup+login endpoints and returns
// the raw session token string.
func SignupAndLogin(t *testing.T, b *backend.Backend) string {
	t.Helper()
	mux := http.NewServeMux()
	MustRegisterRoutes(t, mux, b)

	body := `{"email":"testuser@example.com","password":"securepass123","display_name":"Test User"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/signup", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusCreated, w.Code)

	loginBody := `{"email":"testuser@example.com","password":"securepass123"}`
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", bytes.NewBufferString(loginBody))
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)

	for _, c := range w.Result().Cookies() {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("session cookie not found after login")
	return ""
}
