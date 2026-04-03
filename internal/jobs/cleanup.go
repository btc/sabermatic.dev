package jobs

import (
	"context"
	"log/slog"

	pgx "github.com/jackc/pgx/v5"
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
	q := db.New(w.Pool)
	ids, err := q.FindAbandonedSessions(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := q.MarkSessionCompleted(ctx, id); err != nil {
			slog.Warn("failed to mark abandoned session completed", "session_id", id, "error", err)
			continue
		}
		// TODO: enqueue EvaluateSession for each abandoned session (Phase 7)
		slog.Info("marked abandoned session completed", "session_id", id)
	}
	return nil
}
