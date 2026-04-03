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

// EvalErrorHandler sets failure status when jobs exhaust all retries.
// Handles evaluate_session and generate_educator_content.
// Implements river.ErrorHandler.
type EvalErrorHandler struct {
	Pool *pgxpool.Pool
}

func (h *EvalErrorHandler) HandleError(ctx context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	if job.Attempt < job.MaxAttempts {
		return nil
	}

	switch job.Kind {
	case "evaluate_session":
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

	case "generate_educator_content":
		var args GenerateEducatorContentArgs
		if unmarshalErr := json.Unmarshal(job.EncodedArgs, &args); unmarshalErr != nil {
			slog.Error("unmarshal educator args in error handler", "error", unmarshalErr)
			return nil
		}

		slog.Warn("educator exhausted retries, marking failed",
			"session_id", args.SessionID,
			"attempts", job.Attempt,
			"error", err,
		)

		q := db.New(h.Pool)
		ea, getErr := q.GetEducatorAnalysisBySession(ctx, args.SessionID)
		if getErr != nil {
			slog.Error("get educator analysis in error handler", "error", getErr)
			return nil
		}
		statusErr := q.UpdateEducatorAnalysisStatus(ctx, db.UpdateEducatorAnalysisStatusParams{
			ID:     ea.ID,
			Status: "failed",
		})
		if statusErr != nil {
			slog.Error("set educator failed status", "error", statusErr, "session_id", args.SessionID)
		}
	}

	return nil
}

func (h *EvalErrorHandler) HandlePanic(ctx context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	if job.Attempt >= job.MaxAttempts {
		switch job.Kind {
		case "evaluate_session", "generate_educator_content":
			slog.Error("job panicked on final attempt", "kind", job.Kind, "panic", panicVal)
			return h.HandleError(ctx, job, nil)
		}
	}
	return nil
}
