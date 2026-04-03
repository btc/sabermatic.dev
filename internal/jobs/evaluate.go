package jobs

import (
	"context"
	"time"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
)

// EvaluateSessionArgs are the arguments for the EvaluateSession job.
type EvaluateSessionArgs struct {
	SessionID uuid.UUID `json:"session_id" river:"unique"`
}

func (EvaluateSessionArgs) Kind() string { return "evaluate_session" }

// EvaluateSessionInsertOpts returns the River insert options for evaluation jobs.
func EvaluateSessionInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       QueueAI,
		MaxAttempts: 4,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	}
}

// EvaluateSessionWorker processes EvaluateSession jobs.
type EvaluateSessionWorker struct {
	river.WorkerDefaults[EvaluateSessionArgs]
	Pool *pgxpool.Pool
	LLM  *ai.Client
	Cfg  *config.LLM
	Jobs *river.Client[pgx.Tx] // set after river.NewClient returns
}

func (w *EvaluateSessionWorker) Timeout(job *river.Job[EvaluateSessionArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *EvaluateSessionWorker) Work(ctx context.Context, job *river.Job[EvaluateSessionArgs]) error {
	// Stub — Task 8 replaces with full evaluation logic.
	return nil
}
