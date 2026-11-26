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
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, "postgres://localhost:5432/drill", cfg.Database.URL)
	require.Equal(t, "sk-ant-test", cfg.LLM.APIKey)
	require.True(t, cfg.LLM.RefusalFallbacks)
	require.Equal(t, "claude-sonnet-5-5", cfg.LLM.InterviewerModel)
	require.Equal(t, int64(8192), cfg.LLM.InterviewerMaxTokens)
	require.Equal(t, "low", cfg.LLM.InterviewerEffort)
	require.Equal(t, "claude-opus-5-5", cfg.LLM.EvaluatorModel)
	require.Equal(t, int64(32000), cfg.LLM.EvaluatorMaxTokens)
	require.Equal(t, "high", cfg.LLM.EvaluatorEffort)
	require.Equal(t, "claude-opus-5-5", cfg.LLM.EducatorModel)
	require.Equal(t, int64(32000), cfg.LLM.EducatorMaxTokens)
	require.Equal(t, "high", cfg.LLM.EducatorEffort)
	require.Equal(t, "claude-sonnet-5-5", cfg.LLM.CoachModel)
	require.Equal(t, int64(16000), cfg.LLM.CoachMaxTokens)
	require.Equal(t, "medium", cfg.LLM.CoachEffort)
	require.Equal(t, "claude-sonnet-5-5", cfg.LLM.ImagePromptModel)
	require.Equal(t, int64(4096), cfg.LLM.ImagePromptMaxTokens)
	require.Equal(t, "low", cfg.LLM.ImagePromptEffort)
	require.Equal(t, 8080, cfg.Server.Port)
	require.Equal(t, 720*time.Hour, cfg.Auth.SessionTTL)
	require.Equal(t, 12, cfg.Auth.BcryptCost)
	require.Equal(t, 10*time.Second, cfg.Speech.TTSSentenceTimeout)
	require.Equal(t, "gpt-transcribe", cfg.Speech.TranscriptionModel)
	require.Equal(t, "tts-1", cfg.Speech.TTSModel)
	require.Equal(t, "openai", cfg.Speech.TTSProvider)
	require.Equal(t, "gemini-3.8-flash-lite-tts", cfg.Speech.GeminiTTSModel)
	require.Equal(t, "us", cfg.Speech.GeminiTTSLocation)
	require.Equal(t, "Charon", cfg.Speech.GeminiTTSVoice)
	require.Empty(t, cfg.Speech.GeminiTTSStyle)
	require.Equal(t, "gemini-3.1-flash-image", cfg.Gemini.Model)
	require.Equal(t, "global", cfg.Gemini.Location)
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
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("SERVER_PORT", "9090")
	t.Setenv("INTERVIEWER_MODEL", "claude-opus-5-5")
	t.Setenv("INTERVIEWER_EFFORT", "medium")
	t.Setenv("LLM_REFUSAL_FALLBACKS", "false")

	cfg, err := config.Load()
	require.NoError(t, err)

	require.Equal(t, 9090, cfg.Server.Port)
	require.Equal(t, "claude-opus-5-5", cfg.LLM.InterviewerModel)
	require.Equal(t, "medium", cfg.LLM.InterviewerEffort)
	require.False(t, cfg.LLM.RefusalFallbacks)
}

func TestValidate_InvalidEffort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/drill")
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	t.Setenv("OPENAI_API_KEY", "sk-test")
	t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
	t.Setenv("COACH_EFFORT", "extreme")

	_, err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "COACH_EFFORT")
}

func TestValidate_MaxTokensRange(t *testing.T) {
	for _, tc := range []struct{ env, value string }{
		{"INTERVIEWER_MAX_TOKENS", "0"},
		{"EVALUATOR_MAX_TOKENS", "-1"},
		{"EDUCATOR_MAX_TOKENS", "128001"},
		{"COACH_MAX_TOKENS", "0"},
		{"IMAGE_PROMPT_MAX_TOKENS", "200000"},
	} {
		t.Run(tc.env+"="+tc.value, func(t *testing.T) {
			t.Setenv("DATABASE_URL", "postgres://localhost:5432/drill")
			t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
			t.Setenv("OPENAI_API_KEY", "sk-test")
			t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
			t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
			t.Setenv(tc.env, tc.value)

			_, err := config.Load()
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.env)
		})
	}

	t.Run("128000 accepted", func(t *testing.T) {
		t.Setenv("DATABASE_URL", "postgres://localhost:5432/drill")
		t.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
		t.Setenv("OPENAI_API_KEY", "sk-test")
		t.Setenv("AUTH_TOKEN_SECRET", "test-secret-at-least-32-bytes-long")
		t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")
		t.Setenv("EVALUATOR_MAX_TOKENS", "128000")

		cfg, err := config.Load()
		require.NoError(t, err)
		require.Equal(t, int64(128000), cfg.LLM.EvaluatorMaxTokens)
	})
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
	t.Setenv("GOOGLE_CLOUD_PROJECT", "test-project")

	_, err := config.Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "AUTH_TOKEN_SECRET must be at least 32 characters")
}

func TestValidate_EachRequiredFieldEmpty(t *testing.T) {
	// Base valid env that passes all validation.
	base := map[string]string{
		"DATABASE_URL":         "postgres://localhost:5432/drill",
		"ANTHROPIC_API_KEY":    "sk-ant-test",
		"OPENAI_API_KEY":       "sk-test",
		"AUTH_TOKEN_SECRET":    "test-secret-at-least-32-bytes-long",
		"GOOGLE_CLOUD_PROJECT": "test-project",
	}

	tests := []struct {
		name     string
		unsetKey string
		wantMsg  string
	}{
		{name: "missing DATABASE_URL", unsetKey: "DATABASE_URL", wantMsg: "DATABASE_URL"},
		{name: "missing ANTHROPIC_API_KEY", unsetKey: "ANTHROPIC_API_KEY", wantMsg: "ANTHROPIC_API_KEY"},
		{name: "missing OPENAI_API_KEY", unsetKey: "OPENAI_API_KEY", wantMsg: "OPENAI_API_KEY"},
		{name: "missing GOOGLE_CLOUD_PROJECT", unsetKey: "GOOGLE_CLOUD_PROJECT", wantMsg: "GOOGLE_CLOUD_PROJECT"},
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

func TestValidate_TTSProvider(t *testing.T) {
	base := map[string]string{
		"DATABASE_URL":         "postgres://localhost:5432/drill",
		"ANTHROPIC_API_KEY":    "sk-ant-test",
		"OPENAI_API_KEY":       "sk-test",
		"AUTH_TOKEN_SECRET":    "test-secret-at-least-32-bytes-long",
		"GOOGLE_CLOUD_PROJECT": "test-project",
	}
	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "openai", env: map[string]string{"TTS_PROVIDER": "openai"}},
		{name: "gemini with defaults", env: map[string]string{"TTS_PROVIDER": "gemini"}},
		{name: "gemini with overrides", env: map[string]string{
			"TTS_PROVIDER": "gemini", "GEMINI_TTS_MODEL": "gemini-3.8-flash-tts",
			"GEMINI_TTS_LOCATION": "eu", "GEMINI_TTS_VOICE": "Orus", "GEMINI_TTS_STYLE": "calm",
		}},
		{name: "unknown provider", env: map[string]string{"TTS_PROVIDER": "elevenlabs"}, wantErr: "TTS_PROVIDER must be"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range base {
				t.Setenv(k, v)
			}
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			cfg, err := config.Load()
			if tt.wantErr != "" {
				require.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.env["TTS_PROVIDER"], cfg.Speech.TTSProvider)
			if v, ok := tt.env["GEMINI_TTS_VOICE"]; ok {
				require.Equal(t, v, cfg.Speech.GeminiTTSVoice)
				require.Equal(t, tt.env["GEMINI_TTS_STYLE"], cfg.Speech.GeminiTTSStyle)
				require.Equal(t, tt.env["GEMINI_TTS_LOCATION"], cfg.Speech.GeminiTTSLocation)
				require.Equal(t, tt.env["GEMINI_TTS_MODEL"], cfg.Speech.GeminiTTSModel)
			}
		})
	}
}
