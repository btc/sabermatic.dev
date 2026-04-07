package jobs

import (
	"context"
	"fmt"
	"log/slog"

	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
)

// CleanupStaleGeneratingArgs are the arguments for the CleanupStaleGenerating job.
type CleanupStaleGeneratingArgs struct{}

func (CleanupStaleGeneratingArgs) Kind() string { return "cleanup_stale_generating" }

// CleanupStaleGeneratingWorker resets sessions stuck in "generating" status.
// Sessions that remain generating for more than 5 minutes are reset to "active"
// so they can accept new turns (crash recovery).
type CleanupStaleGeneratingWorker struct {
	river.WorkerDefaults[CleanupStaleGeneratingArgs]
	Pool *pgxpool.Pool
	Jobs *river.Client[pgx.Tx]
}

func (w *CleanupStaleGeneratingWorker) Work(ctx context.Context, job *river.Job[CleanupStaleGeneratingArgs]) error {
	q := db.New(w.Pool)
	if err := q.CleanupStaleGenerating(ctx); err != nil {
		return fmt.Errorf("cleanup stale generating: %w", err)
	}
	slog.Debug("cleanup_stale_generating ran")
	return nil
}
