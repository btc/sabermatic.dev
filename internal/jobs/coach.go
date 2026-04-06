package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/coach"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
)

// RunCoachAnalysisArgs are the arguments for the coach analysis job.
type RunCoachAnalysisArgs struct {
	UserID uuid.UUID `json:"user_id" river:"unique"`
}

func (RunCoachAnalysisArgs) Kind() string { return "run_coach_analysis" }

// RunCoachAnalysisInsertOpts returns River insert options for coach jobs.
func RunCoachAnalysisInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       QueueAI,
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
		},
	}
}

// RunCoachAnalysisWorker processes coach analysis jobs.
type RunCoachAnalysisWorker struct {
	river.WorkerDefaults[RunCoachAnalysisArgs]
	Pool *pgxpool.Pool
	LLM  *ai.Client
	Cfg  *config.LLM
}

func (w *RunCoachAnalysisWorker) Timeout(job *river.Job[RunCoachAnalysisArgs]) time.Duration {
	return 15 * time.Minute
}

func (w *RunCoachAnalysisWorker) Work(ctx context.Context, job *river.Job[RunCoachAnalysisArgs]) error {
	userID := job.Args.UserID
	q := db.New(w.Pool)

	// 1. Load reviewed, non-archived sessions.
	sessions, err := q.GetReviewedSessionsForUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("get reviewed sessions: %w", err)
	}
	if len(sessions) == 0 {
		slog.Info("no reviewed sessions for user, skipping coach", "user_id", userID)
		return nil
	}

	// 2. Collect session IDs for evaluation batch query.
	sessionIDs := make([]uuid.UUID, len(sessions))
	for i := range sessions {
		s := &sessions[i]
		sessionIDs[i] = s.ID
	}

	// 3. Load evaluations.
	evaluations, err := q.GetEvaluationsBySessionIDs(ctx, sessionIDs)
	if err != nil {
		return fmt.Errorf("get evaluations: %w", err)
	}

	// 4. Load questions for topic context.
	questions, err := q.GetQuestionsForUser(ctx, pgtype.UUID{Bytes: userID, Valid: true})
	if err != nil {
		return fmt.Errorf("get questions: %w", err)
	}

	// 5. Build prompt.
	system, promptMsgs := coach.BuildPrompt(sessions, evaluations, questions)

	// 6. Begin transaction.
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 7. Call LLM.
	toolSchema := coach.ToolSchema()
	toolInput, err := w.LLM.CallToolAndLog(ctx, tx, &ai.CallToolParams{
		Model:      w.Cfg.CoachModel,
		System:     system,
		Messages:   promptMsgs,
		MaxTokens:  w.Cfg.CoachMaxTokens,
		UserID:     userID,
		Role:       "coach",
		SessionID:  uuid.Nil,
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolSchema}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("submit_analysis"),
	})
	if err != nil {
		return fmt.Errorf("call coach LLM: %w", err)
	}

	// 8. Parse.
	result, err := coach.Parse(toolInput)
	if err != nil {
		return fmt.Errorf("parse coach: %w", err)
	}

	txq := db.New(tx)

	// 9. Insert generated question if present.
	var suggestedQuestionID pgtype.UUID
	if result.GeneratedQuestion != nil {
		gq := result.GeneratedQuestion
		// Truncate narrative for coach_rationale (max ~500 chars).
		rationale := result.Narrative
		if len(rationale) > 500 {
			rationale = rationale[:500] + "..."
		}
		qID, err := txq.InsertQuestion(ctx, db.InsertQuestionParams{
			UserID:         pgtype.UUID{Bytes: userID, Valid: true},
			Title:          gq.Title,
			Prompt:         gq.Prompt,
			Difficulty:     gq.Difficulty,
			Tags:           gq.Tags,
			Source:         "coach_generated",
			CoachRationale: pgtype.Text{String: rationale, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("insert coach-generated question: %w", err)
		}
		suggestedQuestionID = pgtype.UUID{Bytes: qID, Valid: true}
	}

	// 10. Insert coach analysis.
	_, err = txq.InsertCoachAnalysis(ctx, db.InsertCoachAnalysisParams{
		UserID:              userID,
		Narrative:           result.Narrative,
		WeakestDimension:    pgtype.Text{String: result.WeakestDimension, Valid: result.WeakestDimension != ""},
		ImprovingDimensions: result.ImprovingDimensions,
		TopicGaps:           result.TopicGaps,
		SuggestedQuestionID: suggestedQuestionID,
		SessionsAnalyzed:    sessionIDs,
	})
	if err != nil {
		return fmt.Errorf("insert coach analysis: %w", err)
	}

	// 11. Commit.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("coach analysis complete", "user_id", userID, "sessions_analyzed", len(sessions))
	return nil
}
