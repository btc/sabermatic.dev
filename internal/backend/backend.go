package backend

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/riverqueue/river/rivertype"

	"riverqueue.com/riverui"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/events"
	"github.com/btc/drill/internal/feat/idleunsub"
	"github.com/btc/drill/internal/jobs"
	samplesvc "github.com/btc/drill/internal/rpc/sample"
	"github.com/btc/drill/internal/storage"
)

var tracer = drilotel.Tracer("backend")

// Backend holds shared dependencies and business logic. Handlers call its
// methods; it owns the database pool and River client lifecycle.
//
// The fields below are intentionally unexported. Do not add accessor methods
// that expose them -- consumers should call Backend methods instead.
type Backend struct {
	pool         *pgxpool.Pool
	jobs         Jobs
	riverUI      http.Handler
	closeRiverUI context.CancelFunc
	cfg          *config.Config
	llm          *ai.Client
	stt          ai.Transcriber
	tts          ai.Synthesizer
	store        storage.Store
	events       *events.Emitter
	idleunsub    *idleunsub.Service

	SampleService *samplesvc.SampleService
}

// New creates a pool, runs River migrations, and starts the River client.
// App migrations must be run before calling this (schema must exist).
func New(cfg *config.Config, em *events.Emitter) (*Backend, error) {
	// SampleService (no pool dependency -- initialize first).
	ss, err := samplesvc.NewSampleService()
	if err != nil {
		return nil, fmt.Errorf("sample service: %w", err)
	}

	// Object storage (no pool dependency -- initialize first).
	var store storage.Store
	switch cfg.Storage.Backend {
	case "gcs":
		store, err = storage.NewGCS(context.Background(), cfg.Storage.Bucket, cfg.Storage.PublicBucket)
		if err != nil {
			return nil, fmt.Errorf("gcs storage: %w", err)
		}
		slog.Info("storage: gcs", "audio_bucket", cfg.Storage.Bucket, "public_bucket", cfg.Storage.PublicBucket)
	case "local":
		store, err = storage.NewLocal(cfg)
		if err != nil {
			return nil, fmt.Errorf("local storage: %w", err)
		}
		slog.Info("storage: local", "dir", cfg.Storage.LocalDir)
	}

	// Gemini client for image generation.
	geminiClient, err := ai.NewGeminiClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("gemini client: %w", err)
	}
	slog.Info("gemini client initialized", "model", cfg.Gemini.Model)

	// NB(btc): if an object doesn't depend on the pool, initialize it
	// before so we don't need to clean up pool in case of error

	// Pool uses background context -- must outlive any request or signal context.
	poolCfg, err := pgxpool.ParseConfig(cfg.Database.URL)
	if err != nil {
		return nil, fmt.Errorf("parse database url: %w", err)
	}
	poolCfg.MaxConns = cfg.Database.MaxPoolConns
	poolCfg.ConnConfig.Tracer = otelpgx.NewTracer()
	pool, err := pgxpool.NewWithConfig(context.Background(), poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create pool: %w", err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
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

	// Idle auto-cancel signer. Degraded mode (no keep-link emails) when the
	// HMAC key is not configured: cancel decisions still fire, but the email
	// path is skipped per the spec. Never panic at init. The Service itself
	// is constructed after riverClient is available so we can wire the
	// River-backed EmailEnqueuer.
	var keepSigner *idleunsub.TokenSigner
	if cfg.Idleunsub.KeepTokenHMACKey != "" {
		keepSigner = idleunsub.NewTokenSigner([]byte(cfg.Idleunsub.KeepTokenHMACKey))
	} else {
		slog.Warn("KEEP_TOKEN_HMAC_KEY not configured; idle auto-cancel keep emails will be skipped")
	}

	workers, workerRefs := jobs.RegisterWorkers(cfg, emailSender, pool, llmClient, geminiClient, store, em)
	riverClient, err := river.NewClient(riverpgxv5.New(pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault:      {MaxWorkers: cfg.River.NumDefaultWorkers},
			jobs.QueueNotifications: {MaxWorkers: cfg.River.NumNotifyWorkers},
			jobs.QueueAI:            {MaxWorkers: cfg.River.NumAIWorkers},
			jobs.QueueGemini:        {MaxWorkers: cfg.River.NumGeminiWorkers},
			jobs.QueueMaintenance:   {MaxWorkers: cfg.River.NumMaintWorkers},
		},
		Workers:      workers,
		ErrorHandler: &jobs.ErrorHandler{Pool: pool},
		Middleware:   []rivertype.Middleware{&drilotel.JobTracer{}},
		PeriodicJobs: []*river.PeriodicJob{
			river.NewPeriodicJob(
				river.PeriodicInterval(3*time.Minute), // TODO extract to config
				func() (river.JobArgs, *river.InsertOpts) {
					return jobs.CleanupAbandonedSessionsArgs{}, nil
				},
				nil,
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(60*time.Second), // TODO extract to config
				func() (river.JobArgs, *river.InsertOpts) {
					return jobs.CleanupStaleGeneratingArgs{}, nil
				},
				nil,
			),
			river.NewPeriodicJob(
				river.PeriodicInterval(1*time.Minute), // TODO extract to config
				func() (river.JobArgs, *river.InsertOpts) {
					return jobs.SweepMissingImagesArgs{}, nil
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
	workerRefs.Coach.Jobs = riverClient
	workerRefs.SweepImages.Jobs = riverClient

	// Idle auto-cancel service: now that riverClient is alive, wire the
	// River-backed EmailEnqueuer so cancel/kept emails are sent out-of-band
	// instead of blocking the webhook handler or the auth middleware.
	idleunsubSvc := idleunsub.NewService(
		pool,
		realStripeClient{},
		&idleunsubEnqueuer{jobs: riverClient, cfg: &cfg.Email},
		keepSigner,
		cfg.Auth.BaseURL,
		slog.Default(),
	)

	if err := riverClient.Start(context.Background()); err != nil {
		pool.Close()
		return nil, fmt.Errorf("start river: %w", err)
	}
	slog.Info("river started")

	uiCtx, closeRiverUI := context.WithCancel(context.Background())
	cleanupUI := func() {
		closeRiverUI()
		stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Duration(cfg.River.ShutdownTimeoutSec)*time.Second)
		defer stopCancel()
		riverClient.Stop(stopCtx)
		pool.Close()
	}
	endpoints := riverui.NewEndpoints(riverClient, nil)
	// Note: riverui.NewHandler dereferences opts.Endpoints before checking if opts is nil,
	// so passing nil would panic. Always pass a non-nil literal.
	uiHandler, err := riverui.NewHandler(&riverui.HandlerOpts{
		Endpoints: endpoints,
		Prefix:    "/admin/jobs",
		Logger:    slog.Default(),
	})
	if err != nil {
		cleanupUI()
		return nil, fmt.Errorf("riverui handler: %w", err)
	}
	if err := uiHandler.Start(uiCtx); err != nil {
		cleanupUI()
		return nil, fmt.Errorf("start riverui: %w", err)
	}
	slog.Info("riverui started")

	return &Backend{
		pool:          pool,
		jobs:          riverClient,
		riverUI:       uiHandler,
		closeRiverUI:  closeRiverUI,
		cfg:           cfg,
		llm:           llmClient,
		stt:           stt,
		tts:           tts,
		store:         store,
		events:        em,
		idleunsub:     idleunsubSvc,
		SampleService: ss,
	}, nil
}

// Events returns the Backend's analytics emitter. Used by background jobs
// that hold a Backend reference and need to emit events.
func (b *Backend) Events() *events.Emitter {
	return b.events
}

// SetConfig sets the configuration on a Backend. Useful in tests where
// Backend is constructed manually (without New).
func (b *Backend) SetConfig(cfg *config.Config) { b.cfg = cfg }

// TestOverrides replaces injected dependencies for testing. Only call from
// tests. AI fields swap stub clients; Idleunsub swaps the idle-auto-cancel
// service so tests can inject a fake-Stripe-backed Service.
type TestOverrides struct {
	LLM       *ai.Client
	STT       ai.Transcriber
	TTS       ai.Synthesizer
	Store     storage.Store
	Idleunsub *idleunsub.Service
}

// ApplyTestOverrides replaces injected dependencies for testing. Only call
// from tests.
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
	if o.Store != nil {
		b.store = o.Store
	}
	if o.Idleunsub != nil {
		b.idleunsub = o.Idleunsub
	}
}

// Config returns the Backend's configuration.
func (b *Backend) Config() *config.Config { return b.cfg }

// Pool returns the underlying database pool. Used by tests and the auth
// middleware for direct DB queries. Prefer Backend methods for new code.
func (b *Backend) Pool() *pgxpool.Pool { return b.pool }

// Ping checks connectivity to all backend dependencies.
func (b *Backend) Ping(ctx context.Context) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.Ping")
	defer func() { drilotel.End(span, err) }()
	return b.pool.Ping(ctx)
}

// AuthenticateSession validates a session token hash and returns the
// authenticated user. It also touches the session's last_active timestamp
// in the background.
func (b *Backend) AuthenticateSession(ctx context.Context, tokenHash string) (_ *auth.AuthUser, err error) {
	ctx, span := tracer.Start(ctx, "Backend.AuthenticateSession")
	defer func() { drilotel.End(span, err) }()

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

	// AutoReverse hook: when both gates are set the current request is the
	// activity signal — reverse the auto-cancel in the background.
	if row.SubCancelAtPeriodEnd && row.SubCancelIsAuto {
		go func() {
			reverseCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := b.idleunsub.AutoReverse(reverseCtx, row.UserID); err != nil {
				slog.Warn("auto-reverse failed", "user_id", row.UserID, "err", err)
			}
		}()
	}

	return &auth.AuthUser{
		ID:                   row.UserID,
		Email:                row.Email,
		DisplayName:          row.DisplayName,
		Role:                 row.Role,
		Plan:                 row.Plan,
		EmailVerified:        row.EmailVerified,
		CreatedAt:            row.UserCreatedAt,
		SubCancelAtPeriodEnd: row.SubCancelAtPeriodEnd,
		SubCancelIsAuto:      row.SubCancelIsAuto,
		PendingKeptBanner:    row.PendingKeptBanner,
	}, nil
}

// Close stops River (finishing in-flight jobs) then closes the database pool.
// Implements io.Closer.
func (b *Backend) Close() error {
	// Cancel uiCtx first — the UI handler's background caching goroutines stop
	// asynchronously; they only read data, so racing with River stop is safe.
	b.closeRiverUI()

	timeout := time.Duration(b.cfg.River.ShutdownTimeoutSec) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := b.jobs.Stop(ctx); err != nil {
		slog.Warn("river stop error", "error", err)
	}
	slog.Info("river stopped")

	if err := b.store.Close(); err != nil {
		slog.Warn("storage close error", "error", err)
	}

	b.pool.Close()
	slog.Info("database pool closed")
	return nil
}

// RiverUIHandler returns the pre-built River UI http.Handler.
// Mount it under /admin/jobs/ in the HTTP mux.
func (b *Backend) RiverUIHandler() http.Handler { return b.riverUI }
