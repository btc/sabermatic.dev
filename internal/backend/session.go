package backend

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
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
func (b *Backend) CreateSession(ctx context.Context, p CreateSessionParams) (_ db.InterviewSession, err error) {
	ctx, span := tracer.Start(ctx, "Backend.CreateSession")
	defer func() { drilotel.End(span, err) }()

	if p.DurationMinutes < 1 || p.DurationMinutes > 180 {
		return db.InterviewSession{}, ErrInvalidDuration
	}

	// NOTE: Free trial grants are provisioned once at account creation
	// (Signup in auth.go, OAuthLogin in oauth.go). Do NOT call
	// EnsureFreeGrant here — CreateSession must reflect the user's actual
	// balance, not silently top it up.

	// Begin transaction for all checks and mutations.
	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return db.InterviewSession{}, fmt.Errorf("begin create-session tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := db.New(tx)

	ent, err := b.CheckTx(ctx, tx, p.UserID)
	if err != nil {
		return db.InterviewSession{}, err
	}

	if !ent.DurationAllowed(p.DurationMinutes) {
		return db.InterviewSession{}, ErrDurationExceedsPlan
	}

	activeCount, err := q.CountActiveSessionsByUser(ctx, p.UserID)
	if err != nil {
		return db.InterviewSession{}, fmt.Errorf("count active sessions: %w", err)
	}
	if !ent.ConcurrentSessionsAllowed(int(activeCount)) {
		return db.InterviewSession{}, ErrConcurrentSessionLimit
	}

	if !ent.BalanceSufficient(p.DurationMinutes) {
		return db.InterviewSession{}, ErrInsufficientBalance
	}

	// Verify the question exists (within tx for consistency).
	if _, err := q.GetQuestion(ctx, p.QuestionID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return db.InterviewSession{}, ErrQuestionNotFound
		}
		return db.InterviewSession{}, fmt.Errorf("get question: %w", err)
	}

	// Create session with reserved_minutes set at insert time.
	duration := int32(p.DurationMinutes)
	session, err := q.CreateSession(ctx, db.CreateSessionParams{
		UserID:                p.UserID,
		QuestionID:            p.QuestionID,
		ConfigDurationMinutes: duration,
		ConfigTtsEnabled:      p.TTSEnabled,
		ReservedMinutes:       pgtype.Int4{Int32: duration, Valid: true},
	})
	if err != nil {
		return db.InterviewSession{}, fmt.Errorf("create session: %w", err)
	}

	// Reserve minutes from the user's grants.
	if err := b.reserveMinutesTx(ctx, tx, p.UserID, session.ID, duration); err != nil {
		return db.InterviewSession{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return db.InterviewSession{}, fmt.Errorf("commit create-session tx: %w", err)
	}

	return session, nil
}

// GetSession returns the interview session with the given ID, including the
// joined question fields. Returns ErrSessionNotFound if no such session exists.
func (b *Backend) GetSession(ctx context.Context, id uuid.UUID) (_ db.GetSessionRow, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetSession")
	defer func() { drilotel.End(span, err) }()

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
func (b *Backend) GetSessionForUser(ctx context.Context, id, userID uuid.UUID) (_ db.GetSessionRow, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetSessionForUser")
	defer func() { drilotel.End(span, err) }()

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
func (b *Backend) ListSessions(ctx context.Context, userID uuid.UUID) (_ []db.ListSessionsByUserRow, err error) {
	ctx, span := tracer.Start(ctx, "Backend.ListSessions")
	defer func() { drilotel.End(span, err) }()

	queries := db.New(b.pool)
	rows, err := queries.ListSessionsByUser(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	if rows == nil {
		rows = []db.ListSessionsByUserRow{}
	}
	return rows, nil
}

// ArchiveSessions sets or clears archived_at for sessions owned by userID.
// Returns the number of sessions updated.
func (b *Backend) ArchiveSessions(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID, archive bool) (_ int, err error) {
	ctx, span := tracer.Start(ctx, "Backend.ArchiveSessions")
	defer func() { drilotel.End(span, err) }()

	if len(sessionIDs) == 0 {
		return 0, nil
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin archive tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var updated int
	for _, sid := range sessionIDs {
		// Use raw SQL since sqlc doesn't have a bulk archive query.
		var sqlStr string
		if archive {
			sqlStr = `UPDATE interview_sessions SET archived_at = NOW(), updated_at = NOW() WHERE id = $1 AND user_id = $2 AND archived_at IS NULL`
		} else {
			sqlStr = `UPDATE interview_sessions SET archived_at = NULL, updated_at = NOW() WHERE id = $1 AND user_id = $2 AND archived_at IS NOT NULL`
		}

		ct, err := tx.Exec(ctx, sqlStr, sid, userID)
		if err != nil {
			return 0, fmt.Errorf("archive session %s: %w", sid, err)
		}
		updated += int(ct.RowsAffected())
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit archive tx: %w", err)
	}

	return updated, nil
}

// GetTranscript returns the messages for a session owned by the given user.
func (b *Backend) GetTranscript(ctx context.Context, sessionID, userID uuid.UUID) (_ []db.Message, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetTranscript")
	defer func() { drilotel.End(span, err) }()

	// Verify ownership first.
	_, err = b.GetSessionForUser(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}

	msgs, err := b.GetMessagesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if msgs == nil {
		msgs = []db.Message{}
	}
	return msgs, nil
}

// ---------------------------------------------------------------------------
// Conductor-facing methods
// ---------------------------------------------------------------------------

// AcquireSessionLock acquires a Postgres advisory lock for the given session.
// Returns the dedicated connection (caller must release it) and whether the
// lock was acquired. If acquired is false, no lock is held and conn is nil.
func (b *Backend) AcquireSessionLock(ctx context.Context, sessionID uuid.UUID) (_ *pgxpool.Conn, _ bool, err error) {
	ctx, span := tracer.Start(ctx, "Backend.AcquireSessionLock")
	defer func() { drilotel.End(span, err) }()

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
func (b *Backend) GetMessagesBySession(ctx context.Context, sessionID uuid.UUID) (_ []db.Message, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetMessagesBySession")
	defer func() { drilotel.End(span, err) }()

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
func (b *Backend) persistMessage(ctx context.Context, dbtx db.DBTX, p PersistMessageParams) (_ db.Message, err error) {
	ctx, span := tracer.Start(ctx, "Backend.persistMessage")
	defer func() { drilotel.End(span, err) }()

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
func (b *Backend) PersistInterviewerTurn(ctx context.Context, stream *ai.TokenStream, p PersistMessageParams) (_ db.Message, err error) {
	ctx, span := tracer.Start(ctx, "Backend.PersistInterviewerTurn")
	defer func() { drilotel.End(span, err) }()

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
func (b *Backend) CompleteSession(ctx context.Context, sessionID uuid.UUID, turnCount int) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.CompleteSession")
	defer func() { drilotel.End(span, err) }()

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

	// Refund unused reserved minutes. The SQL derives user_id and duration
	// from the session, distributes the refund, and writes ledger entries.
	if _, err := q.RefundSessionMinutes(ctx, pgtype.UUID{Bytes: sessionID, Valid: true}); err != nil {
		slog.Warn("session refund failed", "session_id", sessionID, "error", err)
	}

	evalOpts := jobs.EvaluateSessionInsertOpts()
	drilotel.SetTraceMetadata(ctx, evalOpts)
	_, err = b.jobs.InsertTx(ctx, tx, jobs.EvaluateSessionArgs{SessionID: sessionID}, evalOpts)
	if err != nil {
		return fmt.Errorf("enqueue evaluate_session: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit end-session: %w", err)
	}

	return nil
}

// CancelSession ends a session early at the user's request. Sets status to
// cancelled + archived (excluded from coach analysis and session lists).
// Refunds unused minutes based on wall-clock duration. Does NOT enqueue evaluation.
func (b *Backend) CancelSession(ctx context.Context, sessionID uuid.UUID, turnCount int) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.CancelSession")
	defer func() { drilotel.End(span, err) }()

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cancel-session tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := db.New(tx)

	if err := q.CancelSession(ctx, db.CancelSessionParams{
		ID:        sessionID,
		TurnCount: int32(turnCount),
	}); err != nil {
		return fmt.Errorf("cancel session: %w", err)
	}

	if _, err := q.RefundSessionMinutes(ctx, pgtype.UUID{Bytes: sessionID, Valid: true}); err != nil {
		slog.Warn("cancel session refund failed", "session_id", sessionID, "error", err)
	}

	return tx.Commit(ctx)
}

// FailSession transitions a session to "failed" and refunds all reserved minutes.
func (b *Backend) FailSession(ctx context.Context, sessionID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.FailSession")
	defer func() { drilotel.End(span, err) }()

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

	// Full refund — SQL derives user_id and reserved_minutes from the session.
	if _, err := q.FullRefundSessionMinutes(ctx, db.FullRefundSessionMinutesParams{
		Reason:    "error_refund",
		SessionID: pgtype.UUID{Bytes: sessionID, Valid: true},
	}); err != nil {
		slog.Warn("fail-session refund failed", "session_id", sessionID, "error", err)
	}

	return tx.Commit(ctx)
}

// Transcribe delegates to the STT provider.
func (b *Backend) Transcribe(ctx context.Context, audio []byte, format string) (_ string, err error) {
	ctx, span := tracer.Start(ctx, "Backend.Transcribe")
	defer func() { drilotel.End(span, err) }()
	return b.stt.Transcribe(ctx, audio, format)
}

// Synthesizer returns the TTS synthesizer, or nil if not configured.
// The conductor uses this to construct a TTSAccumulator.
func (b *Backend) Synthesizer() (ai.Synthesizer, error) {
	return b.tts, nil
}

// Synthesize delegates to the TTS provider.
func (b *Backend) Synthesize(ctx context.Context, text string) (_ io.ReadCloser, err error) {
	ctx, span := tracer.Start(ctx, "Backend.Synthesize")
	defer func() { drilotel.End(span, err) }()
	return b.tts.Synthesize(ctx, text)
}

// StreamLLM creates a streaming LLM call via the Anthropic SDK.
func (b *Backend) StreamLLM(ctx context.Context, p ai.StreamParams) (_ *ai.TokenStream, err error) {
	ctx, span := tracer.Start(ctx, "Backend.StreamLLM")
	defer func() { drilotel.End(span, err) }()
	return b.llm.StreamAndLog(ctx, p)
}

// StoreAudio uploads audio bytes to object storage and returns the URL.
func (b *Backend) StoreAudio(ctx context.Context, key string, data []byte, contentType string) (_ string, err error) {
	ctx, span := tracer.Start(ctx, "Backend.StoreAudio")
	defer func() { drilotel.End(span, err) }()
	return b.store.Put(ctx, key, data, contentType)
}

// SetAudioURL updates the audio_url column for a message.
func (b *Backend) SetAudioURL(ctx context.Context, id uuid.UUID, url string) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.SetAudioURL")
	defer func() { drilotel.End(span, err) }()
	return db.New(b.pool).SetAudioURL(ctx, db.SetAudioURLParams{
		ID:       id,
		AudioUrl: pgtype.Text{String: url, Valid: true},
	})
}
