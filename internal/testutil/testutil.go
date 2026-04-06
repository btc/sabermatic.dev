// Package testutil provides shared test helpers for integration tests that need
// a real Postgres database, backend, and authenticated sessions.
package testutil

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
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
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
	migrations "github.com/btc/drill/sql/migrations"
)

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

// SignupAndLogin creates a user via ConnectRPC AuthService and returns the raw
// session token string.
func SignupAndLogin(t *testing.T, b *backend.Backend) string {
	t.Helper()
	mux := http.NewServeMux()
	require.NoError(t, handler.RegisterRoutes(mux, b))

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// Signup via ConnectRPC.
	pubClient := drillv1connect.NewAuthServiceClient(&http.Client{}, srv.URL)
	_, err := pubClient.Signup(context.Background(), connect.NewRequest(&drillv1.SignupRequest{
		Email:       "testuser@example.com",
		Password:    "securepass123",
		DisplayName: "Test User",
	}))
	require.NoError(t, err)

	// Login via ConnectRPC with a cookie jar to capture Set-Cookie.
	jar, err := cookiejar.New(nil)
	require.NoError(t, err)
	cookieClient := drillv1connect.NewAuthServiceClient(&http.Client{Jar: jar}, srv.URL)
	_, err = cookieClient.Login(context.Background(), connect.NewRequest(&drillv1.LoginRequest{
		Email:    "testuser@example.com",
		Password: "securepass123",
	}))
	require.NoError(t, err)

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	for _, c := range jar.Cookies(u) {
		if c.Name == auth.SessionCookieName {
			return c.Value
		}
	}
	t.Fatal("session cookie not found after login")
	return ""
}
