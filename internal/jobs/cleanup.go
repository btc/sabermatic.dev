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
)

// CleanupAbandonedSessionsArgs are the arguments for the CleanupAbandonedSessions job.
type CleanupAbandonedSessionsArgs struct{}

func (CleanupAbandonedSessionsArgs) Kind() string { return "cleanup_abandoned_sessions" }

// CleanupAbandonedSessionsWorker handles timed-out active sessions.
// Sessions with candidate messages are completed and evaluated.
// Sessions with no candidate messages are cancelled and archived.
type CleanupAbandonedSessionsWorker struct {
	river.WorkerDefaults[CleanupAbandonedSessionsArgs]
	Pool *pgxpool.Pool
	Jobs *river.Client[pgx.Tx] // set after river.NewClient returns
}

func (w *CleanupAbandonedSessionsWorker) Work(ctx context.Context, job *river.Job[CleanupAbandonedSessionsArgs]) error {
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin cleanup tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is no-op

	q := db.New(tx)

	// 1. Cancel empty abandoned sessions (no candidate messages).
	cancelledIDs, err := q.CancelAbandonedEmptySessions(ctx)
	if err != nil {
		return fmt.Errorf("cancel abandoned empty sessions: %w", err)
	}
	for _, id := range cancelledIDs {
		if _, err := q.FullRefundSessionMinutes(ctx, db.FullRefundSessionMinutesParams{
			Reason:    "session_refund",
			SessionID: pgtype.UUID{Bytes: id, Valid: true},
		}); err != nil {
			return fmt.Errorf("full refund for cancelled session %s: %w", id, err)
		}
	}

	// 2. Complete abandoned sessions with candidate messages.
	// No refund: wall clock always exceeds reserved time for abandoned sessions
	// (config_duration_minutes + 5 min threshold), so RefundSessionMinutes
	// would return 0. Just enqueue evaluation.
	completedIDs, err := q.CompleteAbandonedActiveSessions(ctx)
	if err != nil {
		return fmt.Errorf("complete abandoned active sessions: %w", err)
	}
	for _, id := range completedIDs {
		if _, err := w.Jobs.InsertTx(ctx, tx, EvaluateSessionArgs{SessionID: id}, EvaluateSessionInsertOpts()); err != nil {
			return fmt.Errorf("enqueue evaluation for session %s: %w", id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cleanup: %w", err)
	}

	if n := len(cancelledIDs) + len(completedIDs); n > 0 {
		slog.Info("cleaned up abandoned sessions",
			"cancelled", len(cancelledIDs),
			"completed", len(completedIDs))
	}
	return nil
}
