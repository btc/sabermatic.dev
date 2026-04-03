package jobs

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	"github.com/btc/drill/internal/db"
)

// EvalErrorHandler sets evaluation_failed status when the evaluate_session
// job exhausts all retries. Implements river.ErrorHandler.
type EvalErrorHandler struct {
	Pool *pgxpool.Pool
}

func (h *EvalErrorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	if job.Kind != "evaluate_session" {
		return nil
	}
	if job.Attempt < job.MaxAttempts {
		return nil
	}

	// Final attempt failed — mark session as evaluation_failed.
	var args EvaluateSessionArgs
	if unmarshalErr := json.Unmarshal(job.EncodedArgs, &args); unmarshalErr != nil {
		slog.Error("unmarshal evaluate_session args in error handler", "error", unmarshalErr)
		return nil
	}

	slog.Warn("evaluation exhausted retries, marking failed",
		"session_id", args.SessionID,
		"attempts", job.Attempt,
		"error", err,
	)

	q := db.New(h.Pool)
	statusErr := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     args.SessionID,
		Status: "evaluation_failed",
	})
	if statusErr != nil {
		slog.Error("failed to set evaluation_failed status", "error", statusErr, "session_id", args.SessionID)
	}

	return nil
}

func (h *EvalErrorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	if job.Kind == "evaluate_session" && job.Attempt >= job.MaxAttempts {
		slog.Error("evaluation panicked on final attempt",
			"panic", panicVal,
		)
		return h.HandleError(ctx, job, nil)
	}
	return nil
}
