package backend

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/events"
	"github.com/btc/drill/internal/jobs"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
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

	b.events.Emit(
		events.WithSessionID(ctx, session.ID.String()),
		"session_created",
		slog.String("question_id", p.QuestionID.String()),
	)

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

// ---------------------------------------------------------------------------
// Session data access
// ---------------------------------------------------------------------------

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

// GetTranscript returns all messages for the given session, verifying that the
// session belongs to userID. Returns ErrSessionNotFound if the session does not
// exist, ErrSessionNotOwned if it belongs to a different user.
func (b *Backend) GetTranscript(ctx context.Context, sessionID, userID uuid.UUID) (_ []db.Message, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetTranscript")
	defer func() { drilotel.End(span, err) }()

	if _, err := b.GetSessionForUser(ctx, sessionID, userID); err != nil {
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

// ArchiveSessions archives or unarchives sessions in bulk, filtered by userID
// to prevent cross-user modification.
func (b *Backend) ArchiveSessions(ctx context.Context, userID uuid.UUID, sessionIDs []uuid.UUID, archive bool) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.ArchiveSessions")
	defer func() { drilotel.End(span, err) }()

	_, err = db.New(b.pool).ArchiveSessionsBulk(ctx, db.ArchiveSessionsBulkParams{
		Archive:    archive,
		SessionIds: sessionIDs,
		UserID:     userID,
	})
	if err != nil {
		return fmt.Errorf("archive sessions: %w", err)
	}
	return nil
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
	defer tx.Rollback(ctx) //nolint:errcheck

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

	// Only evaluate sessions with real candidate interaction. A session with
	// only the opening question (turnCount <= 1) has nothing to evaluate.
	if turnCount > 1 {
		evalOpts := jobs.EvaluateSessionInsertOpts()
		drilotel.SetTraceMetadata(ctx, evalOpts)
		_, err = b.jobs.InsertTx(ctx, tx, jobs.EvaluateSessionArgs{SessionID: sessionID}, evalOpts)
		if err != nil {
			return fmt.Errorf("enqueue evaluate_session: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit end-session: %w", err)
	}

	b.events.Emit(
		events.WithSessionID(ctx, sessionID.String()),
		"session_ended",
		slog.String("reason", "completed"),
		slog.Int("turn_count", turnCount),
	)

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

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cancel-session: %w", err)
	}

	b.events.Emit(
		events.WithSessionID(ctx, sessionID.String()),
		"session_ended",
		slog.String("reason", "cancelled"),
		slog.Int("turn_count", turnCount),
	)

	return nil
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
	return b.store.Audio().Put(ctx, key, data, contentType)
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

// ---------------------------------------------------------------------------
// InterviewService helpers
// ---------------------------------------------------------------------------

// VerifySessionOwnership checks that the session exists and belongs to the
// given user. Returns ErrSessionNotFound or ErrSessionNotOwned on failure.
func (b *Backend) VerifySessionOwnership(ctx context.Context, sessionID, userID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.VerifySessionOwnership")
	defer func() { drilotel.End(span, err) }()

	session, err := db.New(b.pool).GetSessionByID(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrSessionNotFound
		}
		return fmt.Errorf("get session: %w", err)
	}
	if session.UserID != userID {
		return ErrSessionNotOwned
	}
	return nil
}

// GetSessionState loads the session info and messages for the
// GetSessionState RPC. If knownCount > 0, only messages after that offset
// are returned (cursor-based delta).
func (b *Backend) GetSessionState(ctx context.Context, sessionID uuid.UUID, knownCount int) (_ *drillv1.GetSessionStateResponse, err error) {
	ctx, span := tracer.Start(ctx, "Backend.GetSessionState")
	defer func() { drilotel.End(span, err) }()

	q := db.New(b.pool)

	session, err := q.GetSessionForTurn(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSessionNotFound
		}
		return nil, fmt.Errorf("get session for state: %w", err)
	}

	var msgs []db.Message
	if knownCount > 0 {
		msgs, err = q.GetMessagesBySessionOffset(ctx, db.GetMessagesBySessionOffsetParams{
			SessionID: sessionID,
			Offset:    int32(knownCount),
		})
	} else {
		msgs, err = q.GetMessagesBySession(ctx, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("get messages: %w", err)
	}

	// Coerce nil slices to empty (proto convention).
	protoMsgs := make([]*drillv1.Message, len(msgs))
	for i := range msgs {
		protoMsgs[i] = dbMessageToProto(&msgs[i])
	}

	return &drillv1.GetSessionStateResponse{
		SessionInfo: &drillv1.InterviewSessionInfo{
			SessionId:       sessionID.String(),
			QuestionTitle:   session.QuestionTitle,
			QuestionPrompt:  session.QuestionPrompt,
			DurationMinutes: session.ConfigDurationMinutes,
			TtsEnabled:      session.ConfigTtsEnabled,
			StartTime:       timestamppb.New(session.StartedAt),
		},
		Messages: protoMsgs,
		Status:   statusStringToProto(session.Status),
	}, nil
}

// WaitAndCompleteSession waits for any in-progress generation to finish,
// then completes the session and enqueues evaluation.
func (b *Backend) WaitAndCompleteSession(ctx context.Context, sessionID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.WaitAndCompleteSession")
	defer func() { drilotel.End(span, err) }()

	if err := b.waitForGeneration(ctx, sessionID); err != nil {
		return fmt.Errorf("wait for generation: %w", err)
	}

	turnCount, err := db.New(b.pool).CountInterviewerMessages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("count interviewer messages: %w", err)
	}

	return b.CompleteSession(ctx, sessionID, int(turnCount))
}

// WaitAndCancelSession waits for any in-progress generation to finish,
// then cancels the session and refunds unused minutes.
func (b *Backend) WaitAndCancelSession(ctx context.Context, sessionID uuid.UUID) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.WaitAndCancelSession")
	defer func() { drilotel.End(span, err) }()

	if err := b.waitForGeneration(ctx, sessionID); err != nil {
		return fmt.Errorf("wait for generation: %w", err)
	}

	turnCount, err := db.New(b.pool).CountInterviewerMessages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("count interviewer messages: %w", err)
	}

	return b.CancelSession(ctx, sessionID, int(turnCount))
}

// waitForGeneration polls the session status until it is no longer
// "generating". If the generation has been stale for >5 minutes, it
// triggers inline recovery. Times out after 60 seconds.
func (b *Backend) waitForGeneration(ctx context.Context, sessionID uuid.UUID) error {
	const (
		pollInterval    = 500 * time.Millisecond
		timeout         = 60 * time.Second
		staleThreshold  = 5 * time.Minute
	)

	deadline := time.Now().Add(timeout)
	q := db.New(b.pool)

	for {
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for generation to complete")
		}

		row, err := q.GetSessionStatus(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("get session status: %w", err)
		}

		if row.Status != "generating" {
			return nil
		}

		// Check for stale generation and attempt inline recovery.
		if row.GeneratingSince.Valid && time.Since(row.GeneratingSince.Time) > staleThreshold {
			slog.Warn("waitForGeneration: stale generating detected, recovering",
				"session_id", sessionID,
				"generating_since", row.GeneratingSince.Time)
			if recoverErr := q.InlineRecoverStaleGenerating(ctx, sessionID); recoverErr != nil {
				slog.Error("waitForGeneration: inline recovery failed",
					"error", recoverErr, "session_id", sessionID)
			}
			// Re-check on next iteration.
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}

// ---------------------------------------------------------------------------
// Proto conversion helpers (shared with InterviewService)
// ---------------------------------------------------------------------------

// dbMessageToProto converts a db.Message to its proto representation.
func dbMessageToProto(m *db.Message) *drillv1.Message {
	msg := &drillv1.Message{
		Id:         m.ID.String(),
		SessionId:  m.SessionID.String(),
		Seq:        m.Seq,
		Role:       m.Role,
		Content:    m.Content,
		CreateTime: timestamppb.New(m.CreatedAt),
	}
	if m.InputMethod.Valid {
		msg.InputMethod = &m.InputMethod.String
	}
	if m.AudioUrl.Valid {
		msg.AudioUrl = &m.AudioUrl.String
	}
	return msg
}

// statusStringToProto maps a status string to the proto enum.
func statusStringToProto(s string) drillv1.SessionStatus {
	switch s {
	case "active":
		return drillv1.SessionStatus_SESSION_STATUS_ACTIVE
	case "generating":
		return drillv1.SessionStatus_SESSION_STATUS_GENERATING
	case "completed":
		return drillv1.SessionStatus_SESSION_STATUS_COMPLETED
	case "evaluating":
		return drillv1.SessionStatus_SESSION_STATUS_EVALUATING
	case "reviewed":
		return drillv1.SessionStatus_SESSION_STATUS_REVIEWED
	case "evaluation_failed":
		return drillv1.SessionStatus_SESSION_STATUS_EVALUATION_FAILED
	case "failed":
		return drillv1.SessionStatus_SESSION_STATUS_FAILED
	case "cancelled":
		return drillv1.SessionStatus_SESSION_STATUS_CANCELLED
	default:
		return drillv1.SessionStatus_SESSION_STATUS_UNSPECIFIED
	}
}
