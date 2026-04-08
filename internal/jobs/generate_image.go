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
	"github.com/btc/drill/internal/imagegen"
	"github.com/btc/drill/internal/storage"
)

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
	Pool        *pgxpool.Pool
	LLM         *ai.Client
	Gemini      *ai.GeminiClient
	PublicStore storage.ObjectStore
	Cfg         *config.Config
}

func (w *GenerateQuestionImageWorker) Timeout(job *river.Job[GenerateQuestionImageArgs]) time.Duration {
	return 5 * time.Minute
}

func (w *GenerateQuestionImageWorker) Work(ctx context.Context, job *river.Job[GenerateQuestionImageArgs]) error {
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

	// 3. Build meta-prompt for topic fragment generation.
	meta := imagegen.MetaPrompt(question.Title)

	// 4. Begin transaction.
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 5. Call Sonnet to generate topic-specific prompt fragment.
	// For seed questions (user_id IS NULL), use uuid.Nil.
	userID := uuid.Nil
	if question.UserID.Valid {
		userID = question.UserID.Bytes
	}

	topicFragment, err := w.LLM.CallAndLog(ctx, tx, ai.CallParams{
		Model:     w.Cfg.LLM.ImagePromptModel,
		System:    meta.System,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(meta.User))},
		MaxTokens: w.Cfg.LLM.ImagePromptMaxTokens,
		UserID:    userID,
		Role:      "image_prompt_generator",
		SessionID: uuid.Nil,
	})
	if err != nil {
		return fmt.Errorf("call image prompt LLM: %w", err)
	}

	// 6. Assemble full image prompt.
	fullPrompt := imagegen.AssemblePrompt(topicFragment)

	// 7. Generate image via Gemini.
	start := time.Now()
	imageBytes, mimeType, err := w.Gemini.GenerateImage(ctx, fullPrompt)
	geminiLatency := time.Since(start)
	if err != nil {
		return fmt.Errorf("gemini generate image: %w", err)
	}

	// 8. Upload to public storage.
	key := "questions/" + questionID.String() + "/card.png"
	_, err = w.PublicStore.Put(ctx, key, imageBytes, mimeType)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}

	// 9. Construct public URL.
	imageURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s", w.Cfg.Storage.PublicBucket, key)

	// 10. In the same transaction: update question image_url + log Gemini call.
	txq := db.New(tx)

	if err := txq.SetQuestionImageURL(ctx, db.SetQuestionImageURLParams{
		ID:       questionID,
		ImageUrl: pgtype.Text{String: imageURL, Valid: true},
	}); err != nil {
		return fmt.Errorf("set question image url: %w", err)
	}

	// Log the Gemini LLM call.
	promptJSON, err := json.Marshal(map[string]string{"prompt": fullPrompt})
	if err != nil {
		return fmt.Errorf("marshal gemini prompt: %w", err)
	}
	responseJSON, err := json.Marshal(map[string]string{
		"image_url": imageURL,
		"mime_type": mimeType,
	})
	if err != nil {
		return fmt.Errorf("marshal gemini response: %w", err)
	}

	callID, err := txq.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:     pgtype.UUID{},
		UserID:        userID,
		Role:          "image_generator",
		Model:         w.Cfg.Gemini.Model,
		InputTokens:   0,
		OutputTokens:  0,
		EstimatedCost: ai.NumericFromFloat(0.067),
		LatencyMs:     int32(geminiLatency.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("insert gemini llm_call: %w", err)
	}

	if err := txq.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: callID,
		Prompt:    promptJSON,
		Response:  responseJSON,
	}); err != nil {
		return fmt.Errorf("insert gemini llm_call_content: %w", err)
	}

	// 11. Commit.
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
