package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log/slog"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	pgx "github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/branding"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/email"
	"github.com/btc/drill/internal/evaluation"
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
	Pool    *pgxpool.Pool
	LLM     *ai.Client
	Cfg     *config.LLM
	BaseURL string
	Jobs    *river.Client[pgx.Tx] // set after river.NewClient returns
}

func (w *EvaluateSessionWorker) Timeout(job *river.Job[EvaluateSessionArgs]) time.Duration {
	return 10 * time.Minute
}

func (w *EvaluateSessionWorker) Work(ctx context.Context, job *river.Job[EvaluateSessionArgs]) error {
	sessionID := job.Args.SessionID
	q := db.New(w.Pool)

	// 1. Idempotency: if evaluation already exists for this session, return nil.
	_, err := q.GetEvaluationBySession(ctx, sessionID)
	if err == nil {
		slog.Info("evaluation already exists, skipping", "session_id", sessionID)
		return nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing evaluation: %w", err)
	}

	// 2. Load session data.
	row, err := q.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get session: %w", err)
	}

	messages, err := q.GetMessagesBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get messages: %w", err)
	}

	// 3. If zero messages, mark as failed and return.
	if len(messages) == 0 {
		if err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
			ID:     sessionID,
			Status: "evaluation_failed",
		}); err != nil {
			return fmt.Errorf("set evaluation_failed (no messages): %w", err)
		}
		slog.Warn("no messages in session, marked evaluation_failed", "session_id", sessionID)
		return nil
	}

	// 4. Build seq -> message_id map.
	seqMap := make(map[int32]uuid.UUID, len(messages))
	for _, m := range messages {
		seqMap[m.Seq] = m.ID
	}

	// 5. Set status to "evaluating".
	if err := q.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     sessionID,
		Status: "evaluating",
	}); err != nil {
		return fmt.Errorf("set evaluating status: %w", err)
	}

	// 6. Build prompt.
	system, userMsgs := evaluation.BuildPrompt(db.Question{
		Title:      row.QuestionTitle,
		Prompt:     row.QuestionPrompt,
		Difficulty: row.QuestionDifficulty,
		Hints:      row.QuestionHints,
	}, messages)

	// 7. Begin transaction.
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 8. Call LLM.
	toolSchema := evaluation.ToolSchema()
	toolInput, err := w.LLM.CallToolAndLog(ctx, tx, ai.CallToolParams{
		Model:      w.Cfg.EvaluatorModel,
		System:     system,
		Messages:   userMsgs,
		MaxTokens:  w.Cfg.EvaluatorMaxTokens,
		UserID:     row.UserID,
		Role:       "evaluator",
		SessionID:  sessionID,
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolSchema}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("submit_evaluation"),
	})
	if err != nil {
		return fmt.Errorf("call evaluator LLM: %w", err)
	}

	// 9. Parse.
	result, err := evaluation.Parse(toolInput)
	if err != nil {
		return fmt.Errorf("parse evaluation: %w", err)
	}

	// 10. Validate.
	if err := evaluation.Validate(result, seqMap); err != nil {
		return fmt.Errorf("validate evaluation: %w", err)
	}

	// 11. Persist evaluation, annotations, and update status.
	txq := db.New(tx)

	strengthsJSON, err := json.Marshal(result.Strengths)
	if err != nil {
		return fmt.Errorf("marshal strengths: %w", err)
	}
	gapsJSON, err := json.Marshal(result.Gaps)
	if err != nil {
		return fmt.Errorf("marshal gaps: %w", err)
	}

	evalID, err := txq.InsertEvaluation(ctx, db.InsertEvaluationParams{
		SessionID:          sessionID,
		ScoreRequirements:  result.ScoreRequirements,
		ScoreArchitecture:  result.ScoreArchitecture,
		ScoreDeepDive:      result.ScoreDeepDive,
		ScoreScalability:   result.ScoreScalability,
		ScoreCommunication: result.ScoreCommunication,
		ScoreOverall:       result.ScoreOverall,
		Strengths:          strengthsJSON,
		Gaps:               gapsJSON,
		Advice:             result.Advice,
	})
	if err != nil {
		return fmt.Errorf("insert evaluation: %w", err)
	}

	for _, a := range result.Annotations {
		msgID, ok := seqMap[a.MessageSeq]
		if !ok {
			// Should not happen after Validate, but be defensive.
			continue
		}
		if err := txq.InsertAnnotation(ctx, db.InsertAnnotationParams{
			EvaluationID:   evalID,
			MessageID:      msgID,
			AnnotationType: a.Type,
			Content:        a.Content,
		}); err != nil {
			return fmt.Errorf("insert annotation (seq %d): %w", a.MessageSeq, err)
		}
	}

	if err := txq.UpdateSessionStatusOnly(ctx, db.UpdateSessionStatusOnlyParams{
		ID:     sessionID,
		Status: "reviewed",
	}); err != nil {
		return fmt.Errorf("set reviewed status: %w", err)
	}

	// 12. Enqueue email with pre-rendered HTML.
	user, err := txq.GetUserByID(ctx, row.UserID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}

	htmlBody := renderEvaluationEmail(w.BaseURL, row.QuestionTitle, result)
	_, err = w.Jobs.InsertTx(ctx, tx, SendEmailArgs{
		To:      user.Email,
		Subject: fmt.Sprintf("Evaluation: %s", row.QuestionTitle),
		Text:    fmt.Sprintf("Your evaluation for %q is ready. View it in the app.", row.QuestionTitle),
		HTML:    htmlBody,
	}, &river.InsertOpts{
		Queue:       QueueNotifications,
		MaxAttempts: 3,
	})
	if err != nil {
		return fmt.Errorf("enqueue send_email: %w", err)
	}

	// 13. Commit.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("evaluation complete",
		"session_id", sessionID,
		"evaluation_id", evalID,
		"overall_score", result.ScoreOverall,
	)
	return nil
}

// renderEvaluationEmail builds an HTML email summarizing the evaluation results,
// wrapped in the shared email template.
func renderEvaluationEmail(baseURL, questionTitle string, result *evaluation.EvaluationResult) string {
	var strengthItems, gapItems string
	for _, s := range result.Strengths {
		strengthItems += fmt.Sprintf("<li style=\"margin-bottom:4px;\">%s</li>", html.EscapeString(s))
	}
	for _, g := range result.Gaps {
		gapItems += fmt.Sprintf("<li style=\"margin-bottom:4px;\">%s</li>", html.EscapeString(g))
	}

	sessionsURL := html.EscapeString(baseURL + "/sessions")

	innerHTML := fmt.Sprintf(
		`<h2 style="margin:0 0 16px 0;font-size:18px;color:#333;">Evaluation: %s</h2>

  <h3 style="margin:20px 0 8px 0;font-size:15px;color:#555;">Scores</h3>
  <table style="border-collapse:collapse;width:100%%;margin-bottom:20px;">
    <tr style="background:#fef3c7;"><td style="padding:8px 12px;border:1px solid #e5e7eb;">Requirements</td><td style="padding:8px 12px;border:1px solid #e5e7eb;text-align:center;">%d/5</td></tr>
    <tr><td style="padding:8px 12px;border:1px solid #e5e7eb;">Architecture</td><td style="padding:8px 12px;border:1px solid #e5e7eb;text-align:center;">%d/5</td></tr>
    <tr style="background:#fef3c7;"><td style="padding:8px 12px;border:1px solid #e5e7eb;">Deep Dive</td><td style="padding:8px 12px;border:1px solid #e5e7eb;text-align:center;">%d/5</td></tr>
    <tr><td style="padding:8px 12px;border:1px solid #e5e7eb;">Scalability</td><td style="padding:8px 12px;border:1px solid #e5e7eb;text-align:center;">%d/5</td></tr>
    <tr style="background:#fef3c7;"><td style="padding:8px 12px;border:1px solid #e5e7eb;">Communication</td><td style="padding:8px 12px;border:1px solid #e5e7eb;text-align:center;">%d/5</td></tr>
    <tr style="background:#d9f99d;font-weight:bold;"><td style="padding:8px 12px;border:1px solid #e5e7eb;">Overall</td><td style="padding:8px 12px;border:1px solid #e5e7eb;text-align:center;">%d/5</td></tr>
  </table>

  <h3 style="margin:20px 0 8px 0;font-size:15px;color:#555;">Strengths</h3>
  <ul style="padding-left:20px;">%s</ul>

  <h3 style="margin:20px 0 8px 0;font-size:15px;color:#555;">Areas for Growth</h3>
  <ul style="padding-left:20px;">%s</ul>

  <h3 style="margin:20px 0 8px 0;font-size:15px;color:#555;">Advice</h3>
  <p style="line-height:1.6;">%s</p>

  <p style="margin:24px 0 0 0;"><a href="%s" style="display:inline-block;padding:12px 24px;background:#b45309;color:#fff;text-decoration:none;border-radius:6px;font-weight:500;">View full evaluation</a></p>`,
		html.EscapeString(questionTitle),
		result.ScoreRequirements,
		result.ScoreArchitecture,
		result.ScoreDeepDive,
		result.ScoreScalability,
		result.ScoreCommunication,
		result.ScoreOverall,
		strengthItems,
		gapItems,
		html.EscapeString(result.Advice),
		sessionsURL,
	)

	rendered, err := email.RenderEmail(template.HTML(innerHTML), fmt.Sprintf("You received this email because you use %s.", branding.AppName), "")
	if err != nil {
		// Fall back to inner HTML if template rendering fails.
		slog.Error("render evaluation email template", "error", err)
		return innerHTML
	}
	return rendered
}
