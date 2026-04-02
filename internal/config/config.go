package config

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/btc/drill/internal/email"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sethvargo/go-envconfig"
)

type Config struct {
	Server   Server
	Database Database
	LLM      LLM
	Speech   Speech
	Email    Email
	River    River
}

type Server struct {
	Port int `env:"SERVER_PORT,default=8080"`
}

type Database struct {
	URL         string `env:"DATABASE_URL,required"`
	MaxPoolSize int32  `env:"DATABASE_MAX_POOL_SIZE,default=5"`
}

// NewPool creates a pgxpool connected to the configured database.
func (d *Database) NewPool(ctx context.Context) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(d.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = d.MaxPoolSize

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}

type LLM struct {
	APIKey           string `env:"ANTHROPIC_API_KEY,required"`
	InterviewerModel string `env:"INTERVIEWER_MODEL,default=claude-sonnet-4-20250514"`
	EvaluatorModel   string `env:"EVALUATOR_MODEL,default=claude-sonnet-4-20250514"`
	EducatorModel    string `env:"EDUCATOR_MODEL,default=claude-sonnet-4-20250514"`
	CoachModel       string `env:"COACH_MODEL,default=claude-sonnet-4-20250514"`
}

type Speech struct {
	OpenAIAPIKey string `env:"OPENAI_API_KEY,required"`
	TTSVoice     string `env:"TTS_VOICE,default=onyx"`
	TTSModel     string `env:"TTS_MODEL,default=tts-1"`
	WhisperModel string `env:"WHISPER_MODEL,default=whisper-1"`
}

type Email struct {
	MailgunAPIKey string `env:"MAILGUN_API_KEY,default=test-key"`
	MailgunDomain string `env:"MAILGUN_DOMAIN,default=localhost"`
	FromAddress   string `env:"EMAIL_FROM,default=noreply@drill.dev"`
}

// NewSender creates the appropriate email sender based on configuration.
// Returns a LogSender if Mailgun is not configured (test-key default).
func (e *Email) NewSender() email.Sender {
	if e.MailgunAPIKey == "test-key" {
		slog.Warn("using log email sender (MAILGUN_API_KEY not configured)")
		return email.NewLogSender()
	}
	return email.NewMailgunSender(e.MailgunAPIKey, e.MailgunDomain, e.FromAddress)
}

type River struct {
	ShutdownTimeoutSec   int `env:"RIVER_SHUTDOWN_TIMEOUT_SEC,default=15"`
	NumDefaultWorkers    int `env:"RIVER_DEFAULT_WORKERS,default=5"`
	NumNotifyWorkers     int `env:"RIVER_NOTIFY_WORKERS,default=5"`
	NumAIWorkers         int `env:"RIVER_AI_WORKERS,default=10"`
	NumMaintWorkers      int `env:"RIVER_MAINT_WORKERS,default=2"`
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
