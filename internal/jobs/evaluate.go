package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

// EvaluateSessionArgs are the arguments for the EvaluateSession job.
type EvaluateSessionArgs struct {
	SessionID uuid.UUID `json:"session_id"`
}

func (EvaluateSessionArgs) Kind() string { return "evaluate_session" }

// EvaluateSessionWorker processes EvaluateSession jobs.
type EvaluateSessionWorker struct {
	river.WorkerDefaults[EvaluateSessionArgs]
}

func (w *EvaluateSessionWorker) Work(ctx context.Context, job *river.Job[EvaluateSessionArgs]) error {
	// Stub — Phase 7 replaces with actual evaluation logic.
	return nil
}
