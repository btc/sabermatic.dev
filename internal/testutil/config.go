package testutil

import (
	"context"
	"testing"

	"github.com/sethvargo/go-envconfig"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/config"
)

// baseEnv returns the minimum env vars needed to satisfy config.Load's
// required fields, with sensible test defaults.
func baseEnv(databaseURL string) map[string]string {
	return map[string]string{
		"DATABASE_URL":          databaseURL,
		"ANTHROPIC_API_KEY":     "sk-ant-test",
		"OPENAI_API_KEY":        "sk-test",
		"AUTH_TOKEN_SECRET":     "test-secret-at-least-32-bytes-long",
		"AUTH_BCRYPT_COST":      "4",
		"GOOGLE_CLOUD_PROJECT":  "test-project",
		"GEMINI_LOCATION":       "us-central1",
	}
}

// Config returns a *config.Config populated with test defaults via
// MapLookuper. No environment variables are read or set, making this
// safe for use with t.Parallel(). The DATABASE_URL is set to a dummy
// value — no real database is created.
func Config(t *testing.T) *config.Config {
	t.Helper()
	return ConfigWithOverrides(t, "postgres://unused", nil)
}

// ConfigWithOverrides returns a *config.Config with the given database URL
// and any additional env-key overrides merged on top of the test defaults.
func ConfigWithOverrides(t *testing.T, databaseURL string, overrides map[string]string) *config.Config {
	t.Helper()
	env := baseEnv(databaseURL)
	for k, v := range overrides {
		env[k] = v
	}
	var cfg config.Config
	err := envconfig.ProcessWith(context.Background(), &envconfig.Config{
		Target:   &cfg,
		Lookuper: envconfig.MapLookuper(env),
	})
	require.NoError(t, err)
	return &cfg
}
