package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/educator"
)

// GenerateEducatorContentArgs are the arguments for the educator job.
type GenerateEducatorContentArgs struct {
	SessionID uuid.UUID `json:"session_id" river:"unique"`
}

func (GenerateEducatorContentArgs) Kind() string { return "generate_educator_content" }

// GenerateEducatorContentInsertOpts returns River insert options for educator jobs.
func GenerateEducatorContentInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       QueueAI,
		MaxAttempts: 5,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	}
}

// GenerateEducatorContentWorker processes educator content generation jobs.
type GenerateEducatorContentWorker struct {
	river.WorkerDefaults[GenerateEducatorContentArgs]
	Pool *pgxpool.Pool
	LLM  *ai.Client
	Cfg  *config.LLM
}

func (w *GenerateEducatorContentWorker) Timeout(job *river.Job[GenerateEducatorContentArgs]) time.Duration {
	return 15 * time.Minute
}

func (w *GenerateEducatorContentWorker) Work(ctx context.Context, job *river.Job[GenerateEducatorContentArgs]) error {
	sessionID := job.Args.SessionID
	q := db.New(w.Pool)

	// 1. Idempotency check.
	ea, err := q.GetEducatorAnalysisBySession(ctx, sessionID)
	if err == nil {
		switch ea.Status {
		case "completed":
			slog.Info("educator analysis already completed, skipping", "session_id", sessionID)
			return nil
		case "failed":
			slog.Info("educator analysis already failed, skipping", "session_id", sessionID)
			return nil
		case "generating":
			// Previous attempt failed mid-flight. Proceed to retry.
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing educator analysis: %w", err)
	}

	// 2. Load session, verify status.
	session, err := q.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}
	if session.Status != "reviewed" {
		slog.Warn("session not reviewed, skipping educator", "session_id", sessionID, "status", session.Status)
		return nil
	}

	// 3. Build question from session JOIN data (GetSession already JOINs questions).
	question := db.Question{
		ID:         session.QuestionID,
		Title:      session.QuestionTitle,
		Prompt:     session.QuestionPrompt,
		Difficulty: session.QuestionDifficulty,
	}

	messages, err := q.GetMessagesBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get messages: %w", err)
	}
	eval, err := q.GetEvaluationBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get evaluation: %w", err)
	}

	// 4. Insert generating row if it doesn't exist yet.
	var analysisID uuid.UUID
	if ea.ID != uuid.Nil {
		analysisID = ea.ID
	} else {
		analysisID, err = q.InsertEducatorAnalysis(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("insert educator analysis: %w", err)
		}
	}

	// 5. Build prompt.
	system, promptMsgs := educator.BuildPrompt(question, messages, eval)

	// 6. Begin transaction.
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 7. Call LLM.
	output, err := w.LLM.CallJSONAndLog(ctx, tx, ai.JSONParams{
		Model:     w.Cfg.EducatorModel,
		System:    system,
		Messages:  promptMsgs,
		MaxTokens: w.Cfg.EducatorMaxTokens,
		Effort:    w.Cfg.EducatorEffort,
		Schema:    educator.OutputSchema(),
		UserID:    session.UserID,
		Role:      "educator",
		SessionID: sessionID,
	})
	if err != nil {
		err = fmt.Errorf("call educator LLM: %w", err)
		if terminalLLMError(err, job.JobRow) {
			// A refusal or a repeated max_tokens stop is not retried (see
			// terminalLLMError). River skips the error handler for cancelled
			// jobs, so mark the analysis failed here (outside tx, which
			// rolls back).
			if statusErr := q.UpdateEducatorAnalysisStatus(ctx, db.UpdateEducatorAnalysisStatusParams{
				ID:     analysisID,
				Status: "failed",
			}); statusErr != nil {
				return fmt.Errorf("set educator failed: %w (LLM error: %v)", statusErr, err)
			}
			slog.Warn("educator LLM call cannot succeed on retry, marked failed", "session_id", sessionID, "error", err)
			return river.JobCancel(err)
		}
		return err
	}

	// 8. Parse.
	result, err := educator.Parse(output)
	if err != nil {
		return fmt.Errorf("parse educator: %w", err)
	}

	// 9. Update with content.
	txq := db.New(tx)
	if err := txq.UpdateEducatorAnalysisContent(ctx, db.UpdateEducatorAnalysisContentParams{
		ID:           analysisID,
		ModelAnswer:  pgtype.Text{String: result.ModelAnswer, Valid: true},
		GapDeepDives: pgtype.Text{String: result.GapDeepDives, Valid: true},
	}); err != nil {
		return fmt.Errorf("update educator analysis: %w", err)
	}

	// 10. Commit.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("educator analysis complete", "session_id", sessionID, "analysis_id", analysisID)
	return nil
}
