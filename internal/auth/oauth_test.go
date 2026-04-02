package auth_test

import (
	"testing"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/config"
	"github.com/markbates/goth"
	"github.com/stretchr/testify/require"
)

func TestSetupGothProviders_BothConfigured(t *testing.T) {
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	cfg := &config.OAuth{
		GoogleClientID:     "google-id",
		GoogleClientSecret: "google-secret",
		GitHubClientID:     "github-id",
		GitHubClientSecret: "github-secret",
	}
	auth.SetupGothProviders(cfg, "http://localhost:3000", []byte("test-key-32-bytes-long-for-store"))

	providers := goth.GetProviders()
	require.Contains(t, providers, "google")
	require.Contains(t, providers, "github")
}

func TestSetupGothProviders_NoneConfigured(t *testing.T) {
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	cfg := &config.OAuth{}
	auth.SetupGothProviders(cfg, "http://localhost:3000", []byte("test-key-32-bytes-long-for-store"))

	providers := goth.GetProviders()
	require.Empty(t, providers)
}

func TestSetupGothProviders_GoogleOnly(t *testing.T) {
	goth.ClearProviders()
	t.Cleanup(goth.ClearProviders)

	cfg := &config.OAuth{
		GoogleClientID:     "google-id",
		GoogleClientSecret: "google-secret",
	}
	auth.SetupGothProviders(cfg, "http://localhost:3000", []byte("test-key-32-bytes-long-for-store"))

	providers := goth.GetProviders()
	require.Contains(t, providers, "google")
	require.NotContains(t, providers, "github")
}
