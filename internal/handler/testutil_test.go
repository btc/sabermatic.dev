package handler_test

import (
	"testing"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/testutil"
)

func startPostgres(t *testing.T) string {
	t.Helper()
	return testutil.StartPostgres(t)
}

func newTestBackend(t *testing.T) *backend.Backend {
	t.Helper()
	return testutil.NewTestBackend(t)
}

func loadTestConfig(t *testing.T, databaseURL string) *config.Config {
	t.Helper()
	return testutil.LoadTestConfig(t, databaseURL)
}
