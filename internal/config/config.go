package config

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
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
	Auth     Auth
	OAuth    OAuth
	Otel     Otel
	Stripe   Stripe
}

type Server struct {
	Port               int `env:"SERVER_PORT,default=8080"`
	ShutdownTimeoutSec int `env:"SERVER_SHUTDOWN_TIMEOUT_SEC,default=30"`
}

type Database struct {
	URL         string `env:"DATABASE_URL,required"`
	MaxPoolConns int32  `env:"DATABASE_MAX_POOL_SIZE,default=5"`
}

// NewPool creates a pgxpool connected to the configured database.
// Pass a pgx.QueryTracer to instrument queries (e.g. otelpgx.NewTracer()), or nil for none.
func (d *Database) NewPool(ctx context.Context, tracer pgx.QueryTracer) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(d.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = d.MaxPoolConns
	if tracer != nil {
		poolCfg.ConnConfig.Tracer = tracer
	}

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
	APIKey             string `env:"ANTHROPIC_API_KEY,required"`
	InterviewerModel   string `env:"INTERVIEWER_MODEL,default=claude-sonnet-4-20250514"`
	EvaluatorModel     string `env:"EVALUATOR_MODEL,default=claude-opus-4-20250514"`
	EvaluatorMaxTokens int64  `env:"EVALUATOR_MAX_TOKENS,default=4096"`
	EducatorModel      string `env:"EDUCATOR_MODEL,default=claude-opus-4-20250514"`
	EducatorMaxTokens  int64  `env:"EDUCATOR_MAX_TOKENS,default=8192"`
	CoachModel         string `env:"COACH_MODEL,default=claude-sonnet-4-20250514"`
	CoachMaxTokens     int64  `env:"COACH_MAX_TOKENS,default=4096"`
}

type Speech struct {
	OpenAIAPIKey string `env:"OPENAI_API_KEY,required"`
	TTSVoice     string `env:"TTS_VOICE,default=onyx"`
	TTSModel     string `env:"TTS_MODEL,default=tts-1"`
	WhisperModel string `env:"WHISPER_MODEL,default=whisper-1"`
}

type Email struct {
	MailgunAPIKey   string        `env:"MAILGUN_API_KEY,default=test-key"`
	MailgunDomain   string        `env:"MAILGUN_DOMAIN,default=localhost"`
	FromAddress     string        `env:"EMAIL_FROM,default=noreply@drill.dev"`
	SendTimeout     time.Duration `env:"EMAIL_SEND_TIMEOUT,default=1m"`
	MaxSendAttempts int           `env:"EMAIL_MAX_SEND_ATTEMPTS,default=3"`
}

type River struct {
	ShutdownTimeoutSec   int `env:"RIVER_SHUTDOWN_TIMEOUT_SEC,default=15"`
	NumDefaultWorkers    int `env:"RIVER_DEFAULT_WORKERS,default=5"`
	NumNotifyWorkers     int `env:"RIVER_NOTIFY_WORKERS,default=5"`
	NumAIWorkers         int `env:"RIVER_AI_WORKERS,default=10"`
	NumMaintWorkers      int `env:"RIVER_MAINT_WORKERS,default=2"`
}

type Auth struct {
	TokenSecret    string        `env:"AUTH_TOKEN_SECRET,required"`
	SessionTTL     time.Duration `env:"AUTH_SESSION_TTL,default=720h"`
	VerifyTokenTTL time.Duration `env:"AUTH_VERIFY_TOKEN_TTL,default=24h"`
	ResetTokenTTL  time.Duration `env:"AUTH_RESET_TOKEN_TTL,default=1h"`
	BcryptCost     int           `env:"AUTH_BCRYPT_COST,default=12"`
	BaseURL        string        `env:"BASE_URL,default=http://localhost:3000"`
}

type OAuth struct {
	GoogleClientID     string `env:"OAUTH_GOOGLE_CLIENT_ID"`
	GoogleClientSecret string `env:"OAUTH_GOOGLE_CLIENT_SECRET"`
	GitHubClientID     string `env:"OAUTH_GITHUB_CLIENT_ID"`
	GitHubClientSecret string `env:"OAUTH_GITHUB_CLIENT_SECRET"`
}

type Otel struct {
	Enabled      bool    `env:"OTEL_ENABLED,default=false"`
	Exporter     string  `env:"OTEL_EXPORTER,default=stdout"`
	SampleRate   float64 `env:"OTEL_SAMPLE_RATE,default=1.0"`
	ServiceName  string  `env:"OTEL_SERVICE_NAME,default=drill"`
	GCPProjectID string  `env:"GOOGLE_CLOUD_PROJECT"`
}

type Stripe struct {
	SecretKey      string `env:"STRIPE_SECRET_KEY"`
	WebhookSecret  string `env:"STRIPE_WEBHOOK_SECRET"`
	ProPriceID     string `env:"STRIPE_PRO_PRICE_ID"`
	Pack120PriceID string `env:"STRIPE_PACK_120_PRICE_ID"`
	Pack300PriceID string `env:"STRIPE_PACK_300_PRICE_ID"`
	Pack600PriceID string `env:"STRIPE_PACK_600_PRICE_ID"`
}

// SecureCookies returns true if BaseURL uses HTTPS, indicating cookies
// should have the Secure flag set.
func (a *Auth) SecureCookies() bool {
	return strings.HasPrefix(a.BaseURL, "https://")
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
	if len(cfg.Auth.TokenSecret) < 32 {
		return fmt.Errorf("AUTH_TOKEN_SECRET must be at least 32 characters")
	}
	return nil
}
