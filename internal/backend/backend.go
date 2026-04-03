package backend

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/jobs"
)

// Backend holds shared dependencies and business logic. Handlers call its
// methods; it owns the database pool and River client lifecycle.
//
// The fields below are intentionally unexported. Do not add accessor methods
// that expose them -- consumers should call Backend methods instead.
type Backend struct {
	pool *pgxpool.Pool
	jobs Jobs
	cfg  *config.Config
	llm  *ai.Client
	stt  ai.Transcriber
	tts  ai.Synthesizer
}

// New creates a pool, runs River migrations, and starts the River client.
// App migrations must be run before calling this (schema must exist).
func New(cfg *config.Config) (*Backend, error) {
	// Pool uses background context -- must outlive any request or signal context.
	pool, err := cfg.Database.NewPool(context.Background(), otelpgx.NewTracer())
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	slog.Info("database connected")

	// River migrations (creates River's internal tables)
	riverMigrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create river migrator: %w", err)
	}
	riverRes, err := riverMigrator.Migrate(context.Background(), rivermigrate.DirectionUp, nil)
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("river migrate: %w", err)
	}
	for _, v := range riverRes.Versions {
		slog.Info("river migration applied", "version", v.Version)
	}

	// AI clients
	llmClient := ai.NewClient(cfg.LLM.APIKey, pool)
	stt := ai.NewOpenAITranscriber(cfg.Speech.OpenAIAPIKey, cfg.Speech.WhisperModel)
	tts := ai.NewOpenAISynthesizer(cfg.Speech.OpenAIAPIKey, cfg.Speech.TTSModel, cfg.Speech.TTSVoice)

	// River client
	emailSender := email.NewSender(&cfg.Email)
	workers, workerRefs := jobs.RegisterWorkers(cfg, emailSender, pool, llmClient)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault:      {MaxWorkers: cfg.River.NumDefaultWorkers},
			jobs.QueueNotifications: {MaxWorkers: cfg.River.NumNotifyWorkers},
			jobs.QueueAI:            {MaxWorkers: cfg.River.NumAIWorkers},
			jobs.QueueMaintenance:   {MaxWorkers: cfg.River.NumMaintWorkers},
		},
		Workers:      workers,
		ErrorHandler: &jobs.ErrorHandler{Pool: pool},
		Middleware:   []rivertype.Middleware{&drilotel.JobTracer{}},
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(3*time.Minute),
				func() (river.JobArgs, *river.InsertOpts) {
					return jobs.CleanupAbandonedSessionsArgs{}, nil
				},
				nil,
			),
		},
	})
	if err != nil {
		pool.Close()
		return nil, fmt.Errorf("create river client: %w", err)
	}
	workerRefs.Evaluate.Jobs = riverClient
	workerRefs.Cleanup.Jobs = riverClient
	if err := riverClient.Start(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("start river: %w", err)
	}
	slog.Info("river started")

	return &Backend{
		pool: pool,
		jobs: riverClient,
		cfg:  cfg,
		llm:  llmClient,
		stt:  stt,
		tts:  tts,
	}, nil
}

// SetConfig sets the configuration on a Backend. Useful in tests where
// Backend is constructed manually (without New).
func (b *Backend) SetConfig(cfg *config.Config) { b.cfg = cfg }

// TestOverrides replaces AI dependencies for testing. Only call from tests.
type TestOverrides struct {
	LLM *ai.Client
	STT ai.Transcriber
	TTS ai.Synthesizer
}

// ApplyTestOverrides replaces AI dependencies for testing. Only call from tests.
func (b *Backend) ApplyTestOverrides(o TestOverrides) {
	if o.LLM != nil {
		b.llm = o.LLM
	}
	if o.STT != nil {
		b.stt = o.STT
	}
	if o.TTS != nil {
		b.tts = o.TTS
	}
}

// Config returns the Backend's configuration.
func (b *Backend) Config() *config.Config { return b.cfg }

// Pool returns the underlying database pool. Used by tests and the auth
// middleware for direct DB queries. Prefer Backend methods for new code.
func (b *Backend) Pool() *pgxpool.Pool { return b.pool }

// Ping checks connectivity to all backend dependencies.
func (b *Backend) Ping(ctx context.Context) error {
	return b.pool.Ping(ctx)
}

// AuthenticateSession validates a session token hash and returns the
// authenticated user. It also touches the session's last_active timestamp
// in the background.
func (b *Backend) AuthenticateSession(ctx context.Context, tokenHash string) (*auth.AuthUser, error) {
	queries := db.New(b.pool)
	row, err := queries.GetAuthSessionByToken(ctx, tokenHash)
	if err != nil {
		return nil, err
	}

	// Touch session last_active (fire-and-forget, don't block the request).
	go func() {
		touchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		queries.TouchAuthSession(touchCtx, row.ID)
	}()

	return &auth.AuthUser{
		ID:            row.UserID,
		Email:         row.Email,
		DisplayName:   row.DisplayName,
		Role:          row.Role,
		Plan:          row.Plan,
		EmailVerified: row.EmailVerified,
	}, nil
}

// Close stops River (finishing in-flight jobs) then closes the database pool.
// Implements io.Closer.
func (b *Backend) Close() error {
	timeout := time.Duration(b.cfg.River.ShutdownTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := b.jobs.Stop(ctx); err != nil {
		slog.Warn("river stop error", "error", err)
	}
	slog.Info("river stopped")

	b.pool.Close()
	slog.Info("database pool closed")
	return nil
}
