package config_test

import (
	"testing"
	"time"

	"github.com/btc/drill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/drill")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "postgres://localhost:5432/drill", cfg.Database.URL)
	require.Equal(t, "sk-ant-test", cfg.LLM.APIKey)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.InterviewerModel)
	require.Equal(t, "claude-opus-4-20250514", cfg.LLM.EvaluatorModel)
	require.Equal(t, int64(4096), cfg.LLM.EvaluatorMaxTokens)
	require.Equal(t, "claude-opus-4-20250514", cfg.LLM.EducatorModel)
	require.Equal(t, int64(8192), cfg.LLM.EducatorMaxTokens)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.CoachModel)
	require.Equal(t, int64(4096), cfg.LLM.CoachMaxTokens)
	require.Equal(t, 8080, cfg.Server.Port)
	require.Equal(t, 720*time.Hour, cfg.Auth.SessionTTL)
	require.Equal(t, 12, cfg.Auth.BcryptCost)
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	_, err := config.Load()
	require.Error(t, err)
}

func TestLoadConfig_Override(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://db:5432/drill")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("INTERVIEWER_MODEL", "claude-opus-4-6")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, "claude-opus-4-6", cfg.LLM.InterviewerModel)
}

func TestSecureCookies(t *testing.T) {
	tests := []struct {
		name    string
		baseURL string
		want    bool
	}{
		{name: "https URL returns true", baseURL: "https://example.com", want: true},
		{name: "http localhost returns false", baseURL: "http://localhost:3000", want: false},
		{name: "empty BaseURL returns false", baseURL: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := config.Auth{BaseURL: tt.baseURL}
			require.Equal(t, tt.want, auth.SecureCookies())
		})
	}
}

func TestValidate_ShortTokenSecret(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/drill")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "too-short")

	_, err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "AUTH_TOKEN_SECRET must be at least 32 characters")
}

func TestValidate_EachRequiredFieldEmpty(t *testing.T) {
	// Base valid env that passes all validation.
	base := map[string]string{
		"DATABASE_URL":      "postgres://localhost:5432/drill",
		"ANTHROPIC_API_KEY": "sk-ant-test",
		"OPENAI_API_KEY":    "sk-test",
		"AUTH_TOKEN_SECRET":  "test-secret-at-least-32-bytes-long",
	}

	tests := []struct {
		name     string
		unsetKey string
		wantMsg  string
	}{
		{name: "missing DATABASE_URL", unsetKey: "DATABASE_URL", wantMsg: "DATABASE_URL"},
		{name: "missing ANTHROPIC_API_KEY", unsetKey: "ANTHROPIC_API_KEY", wantMsg: "ANTHROPIC_API_KEY"},
		{name: "missing OPENAI_API_KEY", unsetKey: "OPENAI_API_KEY", wantMsg: "OPENAI_API_KEY"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range base {
				if k == tt.unsetKey {
					t.Setenv(k, "")
				} else {
					t.Setenv(k, v)
				}
			}
			_, err := config.Load()
			require.Error(t, err)
			require.Contains(t, err.Error(), tt.wantMsg)
		})
	}
}
