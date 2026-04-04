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

// CleanupAbandonedSessionsWorker marks timed-out active sessions as completed.
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
	defer tx.Rollback(ctx)

	// Batch-mark all abandoned sessions as completed in a single UPDATE.
	ids, err := db.New(tx).MarkAbandonedSessionsCompleted(ctx)
	if err != nil {
		return fmt.Errorf("mark abandoned sessions: %w", err)
	}

	q := db.New(tx)

	// Refund unused minutes and enqueue evaluation for each.
	for _, id := range ids {
		if _, err := q.RefundSessionMinutes(ctx, pgtype.UUID{Bytes: id, Valid: true}); err != nil {
			slog.Warn("cleanup: refund failed", "session_id", id, "error", err)
		}

		if _, err := w.Jobs.InsertTx(ctx, tx, EvaluateSessionArgs{SessionID: id}, EvaluateSessionInsertOpts()); err != nil {
			return fmt.Errorf("enqueue evaluation for session %s: %w", id, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit cleanup: %w", err)
	}

	if len(ids) > 0 {
		slog.Info("cleaned up abandoned sessions", "count", len(ids))
	}
	return nil
}

