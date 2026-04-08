package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
)

// SweepMissingImagesArgs are the arguments for the sweep missing images job.
type SweepMissingImagesArgs struct{}

func (SweepMissingImagesArgs) Kind() string { return "sweep_missing_images" }

// SweepMissingImagesWorker periodically finds questions without images
// and enqueues GenerateQuestionImage jobs for them.
type SweepMissingImagesWorker struct {
	river.WorkerDefaults[SweepMissingImagesArgs]
	Pool *pgxpool.Pool
	Jobs *river.Client[pgx.Tx]
}

func (w *SweepMissingImagesWorker) Timeout(job *river.Job[SweepMissingImagesArgs]) time.Duration {
	return 1 * time.Minute
}

func (w *SweepMissingImagesWorker) Work(ctx context.Context, job *river.Job[SweepMissingImagesArgs]) error {
	q := db.New(w.Pool)

	ids, err := q.ListQuestionsWithoutImages(ctx, 50)
	if err != nil {
		return fmt.Errorf("list questions without images: %w", err)
	}

	if len(ids) == 0 {
		return nil
	}

	enqueued := 0
	for _, id := range ids {
		_, err := w.Jobs.Insert(ctx, GenerateQuestionImageArgs{
			QuestionID: id,
		}, GenerateQuestionImageInsertOpts())
		if err != nil {
			slog.Error("sweep: failed to enqueue image generation",
				"question_id", id, "error", err)
			continue
		}
		enqueued++
	}

	slog.Info("sweep: enqueued image generation jobs",
		"found", len(ids), "enqueued", enqueued)
	return nil
}
