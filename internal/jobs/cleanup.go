package jobs

import (
	"context"
	"fmt"
	"log/slog"

	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/events"
)

// CleanupAbandonedSessionsArgs are the arguments for the CleanupAbandonedSessions job.
type CleanupAbandonedSessionsArgs struct{}

func (CleanupAbandonedSessionsArgs) Kind() string { return "cleanup_abandoned_sessions" }

// CleanupAbandonedSessionsWorker handles timed-out active sessions.
// Sessions with candidate messages are completed and evaluated.
// Sessions with no candidate messages are cancelled and archived.
type CleanupAbandonedSessionsWorker struct {
	river.WorkerDefaults[CleanupAbandonedSessionsArgs]
	Pool   *pgxpool.Pool
	Jobs   *river.Client[pgx.Tx] // set after river.NewClient returns
	Events *events.Emitter
}

func (w *CleanupAbandonedSessionsWorker) Work(ctx context.Context, job *river.Job[CleanupAbandonedSessionsArgs]) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cleanup tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := db.New(tx)

	// 1. Cancel empty abandoned sessions (no candidate messages).
	cancelledRows, err := q.CancelAbandonedEmptySessions(ctx)
	if err != nil {
		return fmt.Errorf("cancel abandoned empty sessions: %w", err)
	}
	for _, row := range cancelledRows {
		if _, err := q.FullRefundSessionMinutes(ctx, db.FullRefundSessionMinutesParams{
			Reason:    "session_refund",
			SessionID: pgtype.UUID{Bytes: row.ID, Valid: true},
		}); err != nil {
			return fmt.Errorf("full refund for cancelled session %s: %w", row.ID, err)
		}
	}

	// 2. Complete abandoned sessions with candidate messages.
	// No refund: wall clock always exceeds reserved time for abandoned sessions
	// (config_duration_minutes + 5 min threshold), so RefundSessionMinutes
	// would return 0. Just enqueue evaluation.
	completedRows, err := q.CompleteAbandonedActiveSessions(ctx)
	if err != nil {
		return fmt.Errorf("complete abandoned active sessions: %w", err)
	}
	for _, row := range completedRows {
		if _, err := w.Jobs.InsertTx(ctx, tx, EvaluateSessionArgs{SessionID: row.ID}, EvaluateSessionInsertOpts()); err != nil {
			return fmt.Errorf("enqueue evaluation for session %s: %w", row.ID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cleanup: %w", err)
	}

	// Emit session_ended events after commit so events reflect committed state.
	for _, row := range cancelledRows {
		emitCtx := events.WithUserID(ctx, row.UserID.String())
		emitCtx = events.WithSessionID(emitCtx, row.ID.String())
		w.Events.Emit(emitCtx, "session_ended",
			slog.String("reason", "abandoned_empty"),
		)
	}
	for _, row := range completedRows {
		emitCtx := events.WithUserID(ctx, row.UserID.String())
		emitCtx = events.WithSessionID(emitCtx, row.ID.String())
		w.Events.Emit(emitCtx, "session_ended",
			slog.String("reason", "abandoned"),
		)
	}

	if n := len(cancelledRows) + len(completedRows); n > 0 {
		slog.Info("cleaned up abandoned sessions",
			"cancelled", len(cancelledRows),
			"completed", len(completedRows))
	}
	return nil
}
