package backend

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/billing"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// CreateSessionParams holds the parameters for CreateSession.
type CreateSessionParams struct {
	UserID          uuid.UUID
	QuestionID      uuid.UUID
	DurationMinutes int
	TTSEnabled      bool
	Plan            string
}

// CreateSession creates a new interview session after validating duration,
// enforcing plan limits (max duration, concurrent sessions, minute balance),
// and reserving minutes from the user's grants.
func (b *Backend) CreateSession(ctx context.Context, p CreateSessionParams) (db.CreateSessionRow, error) {
	if p.DurationMinutes < 1 || p.DurationMinutes > 180 {
		return db.CreateSessionRow{}, ErrInvalidDuration
	}

	// Look up plan; fall back to "free" if unset or unknown.
	planName := p.Plan
	if planName == "" {
		planName = "free"
	}
	plan, ok := billing.PlanByName(planName)
	if !ok {
		plan, _ = billing.PlanByName("free")
	}

	// Enforce plan duration limit.
	if p.DurationMinutes > plan.MaxDurationMinutes {
		return db.CreateSessionRow{}, ErrDurationExceedsPlan
	}

	// Ensure the current-month free grant exists (idempotent, outside tx).
	if err := b.EnsureFreeGrant(ctx, p.UserID, planName); err != nil {
		return db.CreateSessionRow{}, fmt.Errorf("ensure free grant: %w", err)
	}

	// Begin transaction for all remaining checks and mutations.
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return db.CreateSessionRow{}, fmt.Errorf("begin create-session tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	// Check concurrent session limit.
	activeCount, err := q.CountActiveSessionsByUser(ctx, p.UserID)
	if err != nil {
		return db.CreateSessionRow{}, fmt.Errorf("count active sessions: %w", err)
	}
	if int(activeCount) >= plan.ConcurrentSessions {
		return db.CreateSessionRow{}, ErrConcurrentSessionLimit
	}

	// Verify the question exists (within tx for consistency).
	if _, err := q.GetQuestion(ctx, p.QuestionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.CreateSessionRow{}, ErrQuestionNotFound
		}
		return db.CreateSessionRow{}, fmt.Errorf("get question: %w", err)
	}

	// Create session first (need ID for ledger entries).
	duration := int32(p.DurationMinutes)
	session, err := q.CreateSession(ctx, db.CreateSessionParams{
		UserID:                p.UserID,
		QuestionID:            p.QuestionID,
		ConfigDurationMinutes: duration,
		ConfigTtsEnabled:      p.TTSEnabled,
	})
	if err != nil {
		return db.CreateSessionRow{}, fmt.Errorf("create session: %w", err)
	}

	// Reserve minutes via shared FIFO walk (locks grants, checks balance, debits).
	if err := b.reserveMinutesTx(ctx, tx, p.UserID, session.ID, duration); err != nil {
		return db.CreateSessionRow{}, err
	}

	// Set reserved_minutes on the session.
	if err := q.UpdateSessionReservedMinutes(ctx, db.UpdateSessionReservedMinutesParams{
		ID:              session.ID,
		ReservedMinutes: pgtype.Int4{Int32: duration, Valid: true},
	}); err != nil {
		return db.CreateSessionRow{}, fmt.Errorf("set reserved minutes: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return db.CreateSessionRow{}, fmt.Errorf("commit create-session tx: %w", err)
	}

	return session, nil
}

// GetSession returns the interview session with the given ID, including the
// joined question fields. Returns ErrSessionNotFound if no such session exists.
func (b *Backend) GetSession(ctx context.Context, id uuid.UUID) (db.GetSessionRow, error) {
	queries := db.New(b.pool)
	session, err := queries.GetSession(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.GetSessionRow{}, ErrSessionNotFound
		}
		return db.GetSessionRow{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

// GetSessionForUser returns the session only if it belongs to the given user.
// Returns ErrSessionNotFound if the session does not exist, ErrSessionNotOwned
// if it belongs to a different user.
func (b *Backend) GetSessionForUser(ctx context.Context, id, userID uuid.UUID) (db.GetSessionRow, error) {
	session, err := b.GetSession(ctx, id)
	if err != nil {
		return db.GetSessionRow{}, err
	}
	if session.UserID != userID {
		return db.GetSessionRow{}, ErrSessionNotOwned
	}
	return session, nil
}

// ListSessions returns all sessions for the given user, ordered by creation
// time descending.
func (b *Backend) ListSessions(ctx context.Context, userID uuid.UUID) ([]db.ListSessionsByUserRow, error) {
	queries := db.New(b.pool)
	rows, err := queries.ListSessionsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return rows, nil
}

// ---------------------------------------------------------------------------
// Conductor-facing methods
// ---------------------------------------------------------------------------

// AcquireSessionLock acquires a Postgres advisory lock for the given session.
// Returns the dedicated connection (caller must release it) and whether the
// lock was acquired. If acquired is false, no lock is held and conn is nil.
func (b *Backend) AcquireSessionLock(ctx context.Context, sessionID uuid.UUID) (*pgxpool.Conn, bool, error) {
	lockConn, err := b.pool.Acquire(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("acquire lock conn: %w", err)
	}

	key1 := int32(binary.BigEndian.Uint32(sessionID[:4]))
	key2 := int32(binary.BigEndian.Uint32(sessionID[4:8]))
	var locked bool
	err = lockConn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1, $2)", key1, key2).Scan(&locked)
	if err != nil {
		lockConn.Release()
		return nil, false, fmt.Errorf("advisory lock query: %w", err)
	}
	if !locked {
		lockConn.Release()
		return nil, false, nil
	}
	return lockConn, true, nil
}

// GetMessagesBySession returns all messages for the given session.
func (b *Backend) GetMessagesBySession(ctx context.Context, sessionID uuid.UUID) ([]db.Message, error) {
	msgs, err := db.New(b.pool).GetMessagesBySession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}
	return msgs, nil
}

// PersistMessageParams holds the data for persisting a message.
type PersistMessageParams struct {
	MessageID   uuid.UUID
	SessionID   uuid.UUID
	Seq         int
	Role        string
	Content     string
	InputMethod string
}

// persistMessage is the internal helper that inserts a message using any DBTX (pool or tx).
func (b *Backend) persistMessage(ctx context.Context, dbtx db.DBTX, p PersistMessageParams) (db.Message, error) {
	var im pgtype.Text
	if p.InputMethod != "" {
		im = pgtype.Text{String: p.InputMethod, Valid: true}
	}

	msg, err := db.New(dbtx).InsertMessage(ctx, db.InsertMessageParams{
		ID:          p.MessageID,
		SessionID:   p.SessionID,
		Seq:         int32(p.Seq),
		Role:        p.Role,
		Content:     p.Content,
		InputMethod: im,
	})
	if err != nil {
		return db.Message{}, fmt.Errorf("insert message: %w", err)
	}
	return msg, nil
}

// PersistMessage inserts a message using the pool.
func (b *Backend) PersistMessage(ctx context.Context, p PersistMessageParams) (db.Message, error) {
	return b.persistMessage(ctx, b.pool, p)
}

// PersistInterviewerTurn atomically persists the interviewer message AND the
// LLM call record. The TokenStream's CloseWithTx is called within the
// transaction.
func (b *Backend) PersistInterviewerTurn(ctx context.Context, stream *ai.TokenStream, p PersistMessageParams) (db.Message, error) {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return db.Message{}, fmt.Errorf("begin tx for interviewer msg: %w", err)
	}
	defer tx.Rollback(ctx)

	msg, err := b.persistMessage(ctx, tx, p)
	if err != nil {
		return db.Message{}, fmt.Errorf("persist interviewer message: %w", err)
	}

	if err := stream.CloseWithTx(ctx, tx); err != nil {
		// Non-fatal: the LLM call logging failed but we still persist the message.
		slog.Warn("persist llm_call record", "error", err, "session_id", p.SessionID)
	}

	if err := tx.Commit(ctx); err != nil {
		return db.Message{}, fmt.Errorf("commit interviewer message: %w", err)
	}

	return msg, nil
}

// CompleteSession atomically updates session status to completed, sets turn
// count, and enqueues EvaluateSession.
func (b *Backend) CompleteSession(ctx context.Context, sessionID uuid.UUID, turnCount int) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin end-session tx: %w", err)
	}
	defer tx.Rollback(ctx)

	q := db.New(tx)

	err = q.UpdateSessionStatus(ctx, db.UpdateSessionStatusParams{
		ID:        sessionID,
		Status:    "completed",
		TurnCount: int32(turnCount),
	})
	if err != nil {
		return fmt.Errorf("update session status: %w", err)
	}

	// Refund unused reserved minutes based on actual session duration.
	session, err := q.GetSessionByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session for refund: %w", err)
	}
	if session.ReservedMinutes.Valid && session.ReservedMinutes.Int32 > 0 {
		actualMinutes := 1 // minimum 1 minute
		if session.EndedAt.Valid {
			// StartedAt is time.Time (NOT NULL), EndedAt is pgtype.Timestamptz (nullable).
			dur := session.EndedAt.Time.Sub(session.StartedAt)
			// Integer ceiling: round up to nearest minute.
			actualMinutes = int((dur + time.Minute - 1) / time.Minute)
			if actualMinutes < 1 {
				actualMinutes = 1
			}
		}
		refund := int(session.ReservedMinutes.Int32) - actualMinutes
		if refund > 0 {
			if err := b.refundMinutesTx(ctx, tx, session.UserID, sessionID, int32(refund), "session_refund"); err != nil {
				slog.Warn("session refund failed", "session_id", sessionID, "error", err)
				// Non-fatal: session still completes.
			}
		}
	}

	_, err = b.jobs.InsertTx(ctx, tx, jobs.EvaluateSessionArgs{SessionID: sessionID}, jobs.EvaluateSessionInsertOpts())
	if err != nil {
		return fmt.Errorf("enqueue evaluate_session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit end-session: %w", err)
	}

	return nil
}

// FailSession transitions a session to "failed" and refunds all reserved minutes.
func (b *Backend) FailSession(ctx context.Context, sessionID uuid.UUID) error {
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin fail-session tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := db.New(tx)
	session, err := q.GetSessionByID(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if session.Status != "active" {
		return ErrSessionNotActive
	}

	// Use UpdateSessionStatusOnly to avoid overwriting turn_count or ended_at.
	if err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     sessionID,
		Status: "failed",
	}); err != nil {
		return fmt.Errorf("update session status: %w", err)
	}

	// Full refund of reserved minutes.
	if session.ReservedMinutes.Valid && session.ReservedMinutes.Int32 > 0 {
		if err := b.refundMinutesTx(ctx, tx, session.UserID, sessionID, session.ReservedMinutes.Int32, "error_refund"); err != nil {
			slog.Warn("fail-session refund failed", "session_id", sessionID, "error", err)
		}
	}

	return tx.Commit(ctx)
}

// Transcribe delegates to the STT provider.
func (b *Backend) Transcribe(ctx context.Context, audio []byte, format string) (string, error) {
	return b.stt.Transcribe(ctx, audio, format)
}

// Synthesizer returns the TTS synthesizer, or nil if not configured.
// The conductor uses this to construct a TTSAccumulator.
func (b *Backend) Synthesizer() (ai.Synthesizer, error) {
	return b.tts, nil
}

// Synthesize delegates to the TTS provider.
func (b *Backend) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	return b.tts.Synthesize(ctx, text)
}

// StreamLLM creates a streaming LLM call via the Anthropic SDK.
func (b *Backend) StreamLLM(ctx context.Context, p ai.StreamParams) (*ai.TokenStream, error) {
	return b.llm.StreamAndLog(ctx, p)
}
