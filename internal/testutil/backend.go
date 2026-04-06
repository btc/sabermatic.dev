package testutil

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
)

// NewBackend creates a *backend.Backend from a caller-provided config and
// registers cleanup. Use this when you need to customize config before
// creating the backend (e.g. setting Auth.BaseURL for OAuth tests).
func NewBackend(t *testing.T, cfg *config.Config) *backend.Backend {
	t.Helper()
	b, err := backend.New(cfg)
	require.NoError(t, err)
	t.Cleanup(func() { b.Close() }) //nolint:errcheck // test cleanup
	return b
}

// NewBackend creates a database on the shared container and returns a
// fully-initialized *backend.Backend. This is the common one-liner for
// most integration tests.
func (pg PG) NewBackend(t *testing.T) *backend.Backend {
	t.Helper()
	cfg := pg.Config(t)
	return NewBackend(t, cfg)
}

// Config creates a database on the shared container and returns a
// *config.Config with the real database URL. Use this when you need
// to customize the config before creating a backend.
func (pg PG) Config(t *testing.T) *config.Config {
	t.Helper()
	return pg.ConfigWithOverrides(t, nil)
}

// ConfigWithOverrides creates a database and returns a *config.Config with
// the real database URL and any additional env-key overrides merged in.
func (pg PG) ConfigWithOverrides(t *testing.T, overrides map[string]string) *config.Config {
	t.Helper()
	dbURL := pg.NewDatabase(t)
	return ConfigWithOverrides(t, dbURL, overrides)
}
