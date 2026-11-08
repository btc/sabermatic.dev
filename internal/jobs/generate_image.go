package jobs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"google.golang.org/genai"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/imagegen"
	"github.com/btc/drill/internal/storage"
)

var imagegenTracer = drilotel.Tracer("imagegen")

// geminiImageCostUSD is the estimated cost per Gemini native image generation
// call: gemini-3.1-flash-image Vertex AI Standard pricing for one 1K image,
// $0.067 (global endpoint), per https://cloud.google.com/vertex-ai/generative-ai/pricing
// as read 2026-11-07. It excludes input tokens and thinking tokens (billed
// at the text-output rate), which the response's UsageMetadata would give.
const geminiImageCostUSD = 0.067

// geminiUnavailableSnooze is how long an image job waits before checking
// again when the Gemini client cannot be created (missing or broken ADC).
const geminiUnavailableSnooze = 15 * time.Minute

// GenerateQuestionImageArgs are the arguments for the image generation job.
type GenerateQuestionImageArgs struct {
	QuestionID uuid.UUID `json:"question_id" river:"unique"`
}

func (GenerateQuestionImageArgs) Kind() string { return "generate_question_image" }

// GenerateQuestionImageInsertOpts returns River insert options for image generation jobs.
func GenerateQuestionImageInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       QueueGemini,
		MaxAttempts: 3,
		// Completed is excluded from the unique states so that nulling
		// image_url and letting the sweep re-queue is enough to regenerate.
		//
		// Cancelled and discarded are included so that the per-minute sweep
		// does not re-queue a question whose job ended in failure (a refused
		// image prompt, or exhausted retries); each re-queued run would make
		// another paid call that is expected to fail the same way. River
		// prunes cancelled rows after 24h and discarded rows after 7 days by
		// default, after which the sweep retries the question once more.
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
			ByState: []rivertype.JobState{
				rivertype.JobStateAvailable,
				rivertype.JobStateCancelled,
				rivertype.JobStateDiscarded,
				rivertype.JobStatePending,
				rivertype.JobStateRetryable,
				rivertype.JobStateRunning,
				rivertype.JobStateScheduled,
			},
		},
	}
}

// GenerateQuestionImageWorker generates a cubist illustration for a question.
type GenerateQuestionImageWorker struct {
	river.WorkerDefaults[GenerateQuestionImageArgs]
	Pool   *pgxpool.Pool
	LLM    *ai.Client
	Gemini *ai.GeminiClient
	Store  storage.Store
	Cfg    *config.Config
}

func (w *GenerateQuestionImageWorker) Timeout(job *river.Job[GenerateQuestionImageArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *GenerateQuestionImageWorker) Work(ctx context.Context, job *river.Job[GenerateQuestionImageArgs]) (err error) {
	ctx, span := imagegenTracer.Start(ctx, "GenerateQuestionImageWorker.Work")
	defer func() { drilotel.End(span, err) }()

	// 0. Make sure Gemini can be reached before any billed Anthropic call:
	//    without usable Google credentials the Sonnet output would be thrown
	//    away. A credential problem does not fix itself between retries, so
	//    snooze instead of failing; a snoozed job costs nothing, keeps its
	//    attempts, and its scheduled state stops the sweep from inserting a
	//    duplicate.
	if err := w.Gemini.Ready(ctx); err != nil {
		if errors.Is(err, ai.ErrGeminiUnavailable) {
			slog.Error("gemini unavailable, snoozing image job",
				"question_id", job.Args.QuestionID, "error", err)
			return river.JobSnooze(geminiUnavailableSnooze)
		}
		return fmt.Errorf("gemini ready: %w", err)
	}

	questionID := job.Args.QuestionID
	q := db.New(w.Pool)

	// 1. Load question.
	question, err := q.GetQuestion(ctx, questionID)
	if err != nil {
		return fmt.Errorf("get question: %w", err)
	}

	// 2. Idempotent: skip if image already set.
	if question.ImageUrl.Valid {
		slog.Info("question already has image, skipping",
			"question_id", questionID)
		return nil
	}

	// 3. Call Sonnet to generate topic-specific prompt fragment.
	//    Uses Call (not CallAndLog) — no transaction needed. We log everything
	//    together in the final transaction below.
	meta := imagegen.MetaPrompt(question.Title)
	sonnetResult, err := w.LLM.Call(ctx, ai.CallParams{
		Model:     w.Cfg.LLM.ImagePromptModel,
		System:    meta.System,
		Messages:  []anthropic.BetaMessageParam{anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(meta.User))},
		MaxTokens: w.Cfg.LLM.ImagePromptMaxTokens,
		Effort:    w.Cfg.LLM.ImagePromptEffort,
	})
	if err != nil {
		err = fmt.Errorf("call image prompt LLM: %w", err)
		if sonnetResult.Model != "" {
			// Refusals and max_tokens stops are billed; Call returns their
			// usage. Log through the pool, since no transaction is open.
			if logErr := insertImagePromptCall(ctx, q, question.UserID, meta, sonnetResult); logErr != nil {
				slog.Warn("log failed image prompt LLM call", "question_id", questionID, "error", logErr)
			}
		}
		if terminalLLMError(err, job.JobRow) {
			// Not retried (see terminalLLMError). The cancelled job also
			// keeps the sweep from re-queueing the question; see
			// GenerateQuestionImageInsertOpts.
			slog.Warn("image prompt LLM call cannot succeed on retry, cancelling job", "question_id", questionID, "error", err)
			return river.JobCancel(err)
		}
		return err
	}

	// 4. Assemble full image prompt.
	fullPrompt := imagegen.AssemblePrompt(sonnetResult.Text)

	// 5. Generate image via Gemini.
	geminiStart := time.Now()
	imageBytes, mimeType, err := w.Gemini.GenerateImage(ctx, fullPrompt)
	geminiLatency := time.Since(geminiStart)
	if err != nil {
		var apiErr *genai.APIError
		if errors.As(err, &apiErr) && apiErr.Code == 429 {
			slog.Info("gemini rate limit", "details", apiErr.Details)
			return river.JobSnooze(60 * time.Second)
		}
		return fmt.Errorf("gemini generate image: %w", err)
	}

	// 6. Upload to public bucket (content-addressed so regenerations don't overwrite).
	hash := sha256.Sum256(imageBytes)
	key := path.Join(
		"questions",
		questionID.String(),
		hex.EncodeToString(hash[:8])+".png",
	)
	imageURL, err := w.Store.Public().Put(ctx, key, imageBytes, mimeType)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}

	// 7. Single transaction: update image URL + log both LLM calls.
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	txq := db.New(tx)

	if err := txq.SetQuestionImageURL(ctx, db.SetQuestionImageURLParams{
		ID:       questionID,
		ImageUrl: pgtype.Text{String: imageURL, Valid: true},
	}); err != nil {
		return fmt.Errorf("set question image url: %w", err)
	}

	// Log Sonnet call.
	if err := insertImagePromptCall(ctx, txq, question.UserID, meta, sonnetResult); err != nil {
		return err
	}

	// Log Gemini call.
	geminiPromptJSON, err := json.Marshal(map[string]string{"prompt": fullPrompt})
	if err != nil {
		return fmt.Errorf("marshal gemini prompt: %w", err)
	}
	geminiRespJSON, err := json.Marshal(map[string]string{"image_url": imageURL, "mime_type": mimeType})
	if err != nil {
		return fmt.Errorf("marshal gemini response: %w", err)
	}

	geminiCallID, err := txq.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:     pgtype.UUID{},
		UserID:        question.UserID,
		Role:          "image_generator",
		Model:         w.Cfg.Gemini.Model,
		InputTokens:   0,
		OutputTokens:  0,
		EstimatedCost: ai.NumericFromFloat(geminiImageCostUSD),
		LatencyMs:     int32(geminiLatency.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("insert gemini llm_call: %w", err)
	}
	if err := txq.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: geminiCallID,
		Prompt:    geminiPromptJSON,
		Response:  geminiRespJSON,
	}); err != nil {
		return fmt.Errorf("insert gemini llm_call_content: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("question image generated",
		"question_id", questionID,
		"image_url", imageURL,
		"gemini_latency_ms", geminiLatency.Milliseconds(),
	)
	return nil
}

// insertImagePromptCall logs the image-prompt LLM call to llm_calls and
// llm_call_content. result's token counts and Cost cover every billed attempt
// (see ai.CallResult).
func insertImagePromptCall(ctx context.Context, q *db.Queries, userID pgtype.UUID, meta imagegen.MetaPromptParts, result ai.CallResult) error {
	promptJSON, err := json.Marshal(map[string]string{"system": meta.System, "user": meta.User})
	if err != nil {
		return fmt.Errorf("marshal sonnet prompt: %w", err)
	}
	respJSON, err := json.Marshal(map[string]string{"text": result.Text})
	if err != nil {
		return fmt.Errorf("marshal sonnet response: %w", err)
	}
	callID, err := q.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:     pgtype.UUID{},
		UserID:        userID,
		Role:          "image_prompt_generator",
		Model:         result.Model,
		InputTokens:   int32(result.InputTokens),
		OutputTokens:  int32(result.OutputTokens),
		EstimatedCost: ai.NumericFromFloat(result.Cost),
		LatencyMs:     int32(result.Latency.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("insert sonnet llm_call: %w", err)
	}
	if err := q.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: callID,
		Prompt:    promptJSON,
		Response:  respJSON,
	}); err != nil {
		return fmt.Errorf("insert sonnet llm_call_content: %w", err)
	}
	return nil
}
