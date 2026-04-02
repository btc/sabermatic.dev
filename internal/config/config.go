package config

import (
	"context"
	"fmt"

	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	Server   ServerConfig
	Database DatabaseConfig
	LLM      LLMConfig
	Speech   SpeechConfig
	Email    EmailConfig
}

type ServerConfig struct {
	Port int `env:"SERVER_PORT,default=8080"`
}

type DatabaseConfig struct {
	URL         string `env:"DATABASE_URL,required"`
	MaxPoolSize int    `env:"DATABASE_MAX_POOL_SIZE,default=5"`
}

type LLMConfig struct {
	APIKey           string `env:"ANTHROPIC_API_KEY,required"`
	InterviewerModel string `env:"INTERVIEWER_MODEL,default=claude-sonnet-4-20250514"`
	EvaluatorModel   string `env:"EVALUATOR_MODEL,default=claude-sonnet-4-20250514"`
	EducatorModel    string `env:"EDUCATOR_MODEL,default=claude-sonnet-4-20250514"`
	CoachModel       string `env:"COACH_MODEL,default=claude-sonnet-4-20250514"`
}

type SpeechConfig struct {
	OpenAIAPIKey string `env:"OPENAI_API_KEY,required"`
	TTSVoice     string `env:"TTS_VOICE,default=onyx"`
	TTSModel     string `env:"TTS_MODEL,default=tts-1"`
	WhisperModel string `env:"WHISPER_MODEL,default=whisper-1"`
}

type EmailConfig struct {
	MailgunAPIKey string `env:"MAILGUN_API_KEY,default=test-key"`
	MailgunDomain string `env:"MAILGUN_DOMAIN,default=localhost"`
	FromAddress   string `env:"EMAIL_FROM,default=noreply@drill.dev"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := envconfig.Process(context.Background(), &cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	if err := validate(&cfg); err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}
	return &cfg, nil
}

// validate checks that required fields are non-empty. go-envconfig's required
// tag only catches absent variables; this catches variables set to empty string.
func validate(cfg *Config) error {
	if cfg.Database.URL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}
	if cfg.LLM.APIKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY is required")
	}
	if cfg.Speech.OpenAIAPIKey == "" {
		return fmt.Errorf("OPENAI_API_KEY is required")
	}
	return nil
}
