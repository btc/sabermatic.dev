package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/config"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/imagegen"
	"github.com/btc/drill/internal/storage"
)

var imagegenTracer = drilotel.Tracer("imagegen")

// geminiImageCostUSD is the estimated cost per Gemini native image generation call.
const geminiImageCostUSD = 0.067

// GenerateQuestionImageArgs are the arguments for the image generation job.
type GenerateQuestionImageArgs struct {
	QuestionID uuid.UUID `json:"question_id" river:"unique"`
}

func (GenerateQuestionImageArgs) Kind() string { return "generate_question_image" }

// GenerateQuestionImageInsertOpts returns River insert options for image generation jobs.
func GenerateQuestionImageInsertOpts() *river.InsertOpts {
	return &river.InsertOpts{
		Queue:       QueueAI,
		MaxAttempts: 3,
		UniqueOpts: river.UniqueOpts{
			ByArgs: true,
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

	if w.Gemini == nil {
		return fmt.Errorf("image generation not configured (Gemini client is nil)")
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
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(meta.User))},
		MaxTokens: w.Cfg.LLM.ImagePromptMaxTokens,
	})
	if err != nil {
		return fmt.Errorf("call image prompt LLM: %w", err)
	}

	// 4. Assemble full image prompt.
	fullPrompt := imagegen.AssemblePrompt(sonnetResult.Text)

	// 5. Generate image via Gemini.
	geminiStart := time.Now()
	imageBytes, mimeType, err := w.Gemini.GenerateImage(ctx, fullPrompt)
	geminiLatency := time.Since(geminiStart)
	if err != nil {
		return fmt.Errorf("gemini generate image: %w", err)
	}

	// 6. Upload to public bucket.
	key := "questions/" + questionID.String() + "/card.png"
	_, err = w.Store.Public().Put(ctx, key, imageBytes, mimeType)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}

	imageURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", w.Cfg.Storage.PublicBucket, key)

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
	sonnetPromptJSON, err := json.Marshal(map[string]string{"system": meta.System, "user": meta.User})
	if err != nil {
		return fmt.Errorf("marshal sonnet prompt: %w", err)
	}
	sonnetRespJSON, err := json.Marshal(map[string]string{"text": sonnetResult.Text})
	if err != nil {
		return fmt.Errorf("marshal sonnet response: %w", err)
	}

	sonnetCost := ai.EstimateCost(w.Cfg.LLM.ImagePromptModel, sonnetResult.InputTokens, sonnetResult.OutputTokens)
	sonnetCallID, err := txq.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:     pgtype.UUID{},
		UserID:        question.UserID,
		Role:          "image_prompt_generator",
		Model:         w.Cfg.LLM.ImagePromptModel,
		InputTokens:   int32(sonnetResult.InputTokens),
		OutputTokens:  int32(sonnetResult.OutputTokens),
		EstimatedCost: ai.NumericFromFloat(sonnetCost),
		LatencyMs:     int32(sonnetResult.Latency.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("insert sonnet llm_call: %w", err)
	}
	if err := txq.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: sonnetCallID,
		Prompt:    sonnetPromptJSON,
		Response:  sonnetRespJSON,
	}); err != nil {
		return fmt.Errorf("insert sonnet llm_call_content: %w", err)
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
