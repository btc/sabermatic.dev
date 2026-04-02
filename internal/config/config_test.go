package config_test

import (
	"testing"

	"github.com/btc/drill/internal/config"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/drill")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "postgres://localhost:5432/drill", cfg.Database.URL)
	require.Equal(t, "sk-ant-test", cfg.LLM.APIKey)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.InterviewerModel)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.EvaluatorModel)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.EducatorModel)
	require.Equal(t, "claude-sonnet-4-20250514", cfg.LLM.CoachModel)
	require.Equal(t, 8080, cfg.Server.Port)
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
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("INTERVIEWER_MODEL", "claude-opus-4-6")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, "claude-opus-4-6", cfg.LLM.InterviewerModel)
}
