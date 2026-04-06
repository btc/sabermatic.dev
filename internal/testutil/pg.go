package testutil

import (
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	migrations "github.com/btc/drill/sql/migrations"
)

// PG holds a shared Postgres container. Create one per package via
// SharedPostgres in TestMain.
type PG struct {
	connStr   string
	container *postgres.PostgresContainer
}

// SharedPostgres starts a single Postgres 16 container and returns a PG
// factory. Call pg.RunTests(m) in TestMain to run tests, clean up, and exit.
//
//	func TestMain(m *testing.M) {
//	    pg = testutil.SharedPostgres()
//	    pg.RunTests(m)
//	}
func SharedPostgres() PG {
	ctx := context.Background()

	container, err := postgres.Run(ctx,
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
		panic(fmt.Sprintf("testutil.SharedPostgres: start container: %v", err))
	}

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		container.Terminate(ctx)
		panic(fmt.Sprintf("testutil.SharedPostgres: connection string: %v", err))
	}

	return PG{connStr: connStr, container: container}
}

// RunTests runs the test suite, cleans up the container, and exits.
func (pg PG) RunTests(m *testing.M) {
	code := m.Run()
	pg.Cleanup()
	os.Exit(code)
}

// Cleanup terminates the shared container.
func (pg PG) Cleanup() {
	if pg.container != nil {
		pg.container.Terminate(context.Background())
	}
}

var unsafeChars = regexp.MustCompile(`[^a-z0-9_]`)

// dbName returns a Postgres-safe database name derived from the test name.
func dbName(t *testing.T) string {
	name := strings.ToLower(t.Name())
	name = unsafeChars.ReplaceAllString(name, "_")
	if len(name) > 50 {
		name = name[:50]
	}
	b := make([]byte, 4)
	rand.Read(b)
	return fmt.Sprintf("test_%s_%x", name, b)
}

// NewDatabase creates a fresh database on the shared container, runs app
// migrations, and returns the connection string. The database is dropped
// on test cleanup.
func (pg PG) NewDatabase(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping integration test")
	}
	ctx := context.Background()
	name := dbName(t)

	conn, err := pgx.Connect(ctx, pg.connStr)
	require.NoError(t, err)
	_, err = conn.Exec(ctx, fmt.Sprintf("CREATE DATABASE %s", name))
	require.NoError(t, err)
	conn.Close(ctx)

	testConnStr := replaceDBName(pg.connStr, name)

	d, err := iofs.New(migrations.FS, ".")
	require.NoError(t, err)
	trimmed := strings.TrimPrefix(testConnStr, "postgresql://")
	trimmed = strings.TrimPrefix(trimmed, "postgres://")
	pgxURL := "pgx5://" + trimmed
	m, err := migrate.NewWithSourceInstance("iofs", d, pgxURL)
	require.NoError(t, err)
	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		t.Fatalf("migrate up: %v", err)
	}
	srcErr, dbErr := m.Close()
	require.NoError(t, srcErr)
	require.NoError(t, dbErr)

	t.Cleanup(func() {
		conn, err := pgx.Connect(context.Background(), pg.connStr)
		if err != nil {
			return
		}
		conn.Exec(context.Background(), fmt.Sprintf(
			"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = '%s'", name))
		conn.Exec(context.Background(), fmt.Sprintf("DROP DATABASE IF EXISTS %s", name))
		conn.Close(context.Background())
	})

	return testConnStr
}

// replaceDBName swaps the database name in a Postgres connection string.
func replaceDBName(connStr, newDB string) string {
	qIdx := strings.Index(connStr, "?")
	var base, query string
	if qIdx >= 0 {
		base = connStr[:qIdx]
		query = connStr[qIdx:]
	} else {
		base = connStr
		query = ""
	}
	slashIdx := strings.LastIndex(base, "/")
	if slashIdx < 0 {
		return connStr
	}
	return base[:slashIdx+1] + newDB + query
}
