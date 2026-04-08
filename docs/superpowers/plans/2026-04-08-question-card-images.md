# Question Card Images Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add AI-generated cubist editorial illustrations to question cards, replacing the text-only list with an Airbnb-style borderless image grid.

**Architecture:** Two River workers: Job A generates an image for a single question (Claude Sonnet for prompt → Nano Banana 2 for image → GCS upload → DB update, all-or-nothing). Job B sweeps for questions missing images and enqueues Job A. Frontend rewrites QuestionCard to a borderless image grid with a hero card for coach recommendations.

**Tech Stack:** Go, River, Claude Sonnet (Anthropic SDK), Nano Banana 2 (`google.golang.org/genai`), GCS, React/Tailwind, ConnectRPC/protobuf.

**Spec:** `docs/superpowers/specs/2026-04-08-question-card-images-design.md`

---

### Task 1: SQL Migration + sqlc Queries

**Files:**
- Create: `sql/migrations/010_question_images.up.sql`
- Create: `sql/migrations/010_question_images.down.sql`
- Modify: `sql/queries/questions.sql`
- Regenerate: `internal/db/*.sql.go`, `internal/db/models.go`

- [ ] **Step 1: Create migration files**

```sql
-- sql/migrations/010_question_images.up.sql
ALTER TABLE questions ADD COLUMN image_url TEXT;
```

```sql
-- sql/migrations/010_question_images.down.sql
ALTER TABLE questions DROP COLUMN IF EXISTS image_url;
```

- [ ] **Step 2: Add new sqlc queries and update existing ones**

Add to `sql/queries/questions.sql`:

```sql
-- name: ListQuestionsWithoutImages :many
SELECT id FROM questions
WHERE image_url IS NULL
LIMIT $1;

-- name: SetQuestionImageURL :exec
UPDATE questions SET image_url = $2, updated_at = NOW()
WHERE id = $1;
```

Update all existing SELECT queries in `sql/queries/questions.sql` to include `image_url` in their column lists:

- `ListSeedQuestions`: add `image_url` after `source`
- `GetQuestion`: add `image_url` after `updated_at`
- `ListQuestionsForUser`: add `image_url` after `source`
- `GetQuestionsForUser`: already uses `SELECT *` (gets it automatically)

- [ ] **Step 3: Run migration and regenerate sqlc**

```bash
# Apply migration to local DB
psql "$DATABASE_URL" -f sql/migrations/010_question_images.up.sql

# Regenerate sqlc
sqlc generate
```

Verify `internal/db/models.go` now has `ImageUrl pgtype.Text` in the `Question` struct, and `internal/db/questions.sql.go` has the new query functions.

- [ ] **Step 4: Commit**

```bash
git add sql/migrations/010_question_images.up.sql sql/migrations/010_question_images.down.sql sql/queries/questions.sql internal/db/
git commit -m "feat: add image_url column and sqlc queries for question images"
```

---

### Task 2: Proto Update + buf generate

**Files:**
- Modify: `pb/drill/v1/question.proto`
- Regenerate: `internal/pb/drill/v1/question.pb.go`, `web/src/pb/drill/v1/question_pb.*`

- [ ] **Step 1: Add image_url to proto**

In `pb/drill/v1/question.proto`, add field 10 to the `Question` message:

```protobuf
message Question {
  string id = 1;
  optional string user_id = 2;
  string title = 3;
  string prompt = 4;
  Difficulty difficulty = 5;
  repeated string tags = 6;
  optional string hints = 7;
  QuestionSource source = 8;
  google.protobuf.Timestamp create_time = 9;
  optional string image_url = 10;
}
```

- [ ] **Step 2: Generate proto code**

```bash
buf generate
```

Verify generated files in `internal/pb/drill/v1/` and `web/src/pb/drill/v1/`.

- [ ] **Step 3: Update DB-to-proto mapping**

In `internal/rpc/question/server.go`, update `questionToProto` to map `image_url`:

```go
func questionToProto(row db.ListQuestionsForUserRow) *drillv1.Question {
	tags := row.Tags
	if tags == nil {
		tags = []string{}
	}

	q := &drillv1.Question{
		Id:         row.ID.String(),
		Title:      row.Title,
		Prompt:     row.Prompt,
		Difficulty: difficultyToProto(row.Difficulty),
		Tags:       tags,
		Source:     sourceToProto(row.Source),
		CreateTime: timestamppb.New(row.CreatedAt),
	}

	if row.UserID.Valid {
		uid := uuid.UUID(row.UserID.Bytes).String()
		q.UserId = &uid
	}

	if row.Hints.Valid {
		q.Hints = &row.Hints.String
	}

	if row.ImageUrl.Valid {
		q.ImageUrl = &row.ImageUrl.String
	}

	return q
}
```

- [ ] **Step 4: Run existing tests to verify nothing is broken**

```bash
go test ./internal/rpc/question/... -v
```

- [ ] **Step 5: Commit**

```bash
git add pb/ internal/pb/ web/src/pb/ internal/rpc/question/server.go
git commit -m "feat: add image_url to Question proto and DB-to-proto mapping"
```

---

### Task 3: Config — Gemini + Public Storage

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add Gemini and public storage config**

Add new config sections to `internal/config/config.go`:

```go
type Gemini struct {
	Model    string `env:"GEMINI_MODEL,default=gemini-3.1-flash-image-preview"`
	Location string `env:"GEMINI_LOCATION,default=us-central1"`
}
```

Add `PublicBucket` to the existing `Storage` struct:

```go
type Storage struct {
	Backend      string `env:"STORAGE_BACKEND,default=local"`
	Bucket       string `env:"STORAGE_BUCKET"`
	PublicBucket string `env:"PUBLIC_STORAGE_BUCKET"`
	LocalDir     string `env:"STORAGE_LOCAL_DIR,default=data/audio"`
}
```

Add `Gemini` to the `Config` struct:

```go
type Config struct {
	Server   Server
	Database Database
	Log      Log
	LLM      LLM
	Speech   Speech
	Email    Email
	River    River
	Auth     Auth
	OAuth    OAuth
	Otel     Otel
	Storage  Storage
	Stripe   Stripe
	Gemini   Gemini
}
```

Add `ImagePromptModel` and `ImagePromptMaxTokens` to the existing `LLM` struct:

```go
type LLM struct {
	APIKey               string `env:"ANTHROPIC_API_KEY,required"`
	InterviewerModel     string `env:"INTERVIEWER_MODEL,default=claude-sonnet-4-20250514"`
	EvaluatorModel       string `env:"EVALUATOR_MODEL,default=claude-opus-4-20250514"`
	EvaluatorMaxTokens   int64  `env:"EVALUATOR_MAX_TOKENS,default=4096"`
	EducatorModel        string `env:"EDUCATOR_MODEL,default=claude-opus-4-20250514"`
	EducatorMaxTokens    int64  `env:"EDUCATOR_MAX_TOKENS,default=8192"`
	CoachModel           string `env:"COACH_MODEL,default=claude-sonnet-4-20250514"`
	CoachMaxTokens       int64  `env:"COACH_MAX_TOKENS,default=4096"`
	ImagePromptModel     string `env:"IMAGE_PROMPT_MODEL,default=claude-sonnet-4-20250514"`
	ImagePromptMaxTokens int64  `env:"IMAGE_PROMPT_MAX_TOKENS,default=1024"`
}
```

- [ ] **Step 2: Run existing tests**

```bash
go test ./internal/config/... -v
```

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add Gemini and image prompt config"
```

---

### Task 4: Gemini Image Generation Client

**Files:**
- Create: `internal/ai/gemini.go`
- Create: `internal/ai/gemini_test.go`

- [ ] **Step 1: Install dependency**

```bash
go get google.golang.org/genai
```

- [ ] **Step 2: Write test for GeminiClient**

Create `internal/ai/gemini_test.go`:

```go
package ai_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
)

func TestGeminiClient_GenerateImage_EmptyPrompt(t *testing.T) {
	// GeminiClient should return an error for empty prompts.
	client := &ai.GeminiClient{} // nil underlying client
	_, _, err := client.GenerateImage(context.Background(), "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "empty prompt")
}
```

- [ ] **Step 3: Run test to verify it fails**

```bash
go test ./internal/ai/ -run TestGeminiClient -v
```

Expected: FAIL (GeminiClient not defined)

- [ ] **Step 4: Implement GeminiClient**

Create `internal/ai/gemini.go`:

```go
package ai

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/genai"

	"github.com/btc/drill/internal/drilotel"
)

// GeminiClient wraps the Google Gen AI SDK for image generation.
type GeminiClient struct {
	client *genai.Client
	model  string
}

// NewGeminiClient creates a GeminiClient using Vertex AI with ADC.
func NewGeminiClient(ctx context.Context, project, location, model string) (*GeminiClient, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  project,
		Location: location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("create gemini client: %w", err)
	}
	return &GeminiClient{client: client, model: model}, nil
}

var geminiTracer = drilotel.Tracer("gemini")

// GenerateImage generates an image from a text prompt.
// Returns image bytes and MIME type.
func (g *GeminiClient) GenerateImage(ctx context.Context, prompt string) (_ []byte, _ string, err error) {
	ctx, span := geminiTracer.Start(ctx, "GeminiClient.GenerateImage")
	defer func() { drilotel.End(span, err) }()

	if prompt == "" {
		return nil, "", errors.New("empty prompt")
	}
	if g.client == nil {
		return nil, "", errors.New("gemini client not initialized")
	}

	result, err := g.client.Models.GenerateContent(ctx, g.model,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			ResponseModalities: []string{"IMAGE"},
		},
	)
	if err != nil {
		return nil, "", fmt.Errorf("gemini generate: %w", err)
	}

	if len(result.Candidates) == 0 || result.Candidates[0].Content == nil {
		return nil, "", errors.New("gemini: no candidates returned")
	}

	for _, part := range result.Candidates[0].Content.Parts {
		if part.InlineData != nil && len(part.InlineData.Data) > 0 {
			return part.InlineData.Data, part.InlineData.MIMEType, nil
		}
	}

	return nil, "", errors.New("gemini: no image data in response")
}

// Close closes the underlying client.
func (g *GeminiClient) Close() error {
	// genai.Client doesn't have a Close method; this is a no-op placeholder
	// for interface compliance if needed later.
	return nil
}
```

- [ ] **Step 5: Run test**

```bash
go test ./internal/ai/ -run TestGeminiClient -v
```

Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/ai/gemini.go internal/ai/gemini_test.go go.mod go.sum
git commit -m "feat: add Gemini image generation client"
```

---

### Task 5: Image Prompt Generator

**Files:**
- Create: `internal/imagegen/prompt.go`
- Create: `internal/imagegen/prompt_test.go`

- [ ] **Step 1: Write test**

Create `internal/imagegen/prompt_test.go`:

```go
package imagegen

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAssemblePrompt(t *testing.T) {
	fragment := "an abstract figure as a calm gatekeeper"
	got := AssemblePrompt(fragment)

	assert.Contains(t, got, "Cubist editorial illustration of")
	assert.Contains(t, got, fragment)
	assert.Contains(t, got, "Edge-to-edge composition")
	assert.Contains(t, got, "No text")
}

func TestMetaPrompt(t *testing.T) {
	got := MetaPrompt("Rate Limiter")

	// Must contain system instructions
	assert.Contains(t, got.System, "cubist editorial illustration series")
	// Must contain the three few-shot examples
	assert.Contains(t, got.User, "Rate Limiter")
	assert.Contains(t, got.User, "News Feed")
	assert.Contains(t, got.User, "Chat System")
	// Must end with the target question
	assert.Contains(t, got.User, "Now generate a prompt fragment for")
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
go test ./internal/imagegen/ -v
```

Expected: FAIL (package doesn't exist)

- [ ] **Step 3: Implement**

Create `internal/imagegen/prompt.go`:

```go
package imagegen

import "fmt"

const promptPrefix = "Cubist editorial illustration of "

const promptSuffix = ". Bold dark outlines on flat color planes. Warm palette: cream, amber, " +
	"terracotta, burnt orange with accents of cerulean blue and sage green. " +
	"Painterly brushwork. Playful and warm, Apple corporate illustration energy. " +
	"Edge-to-edge composition filling the entire canvas, no border, no margin, no frame. " +
	"No text. Horizontal 4:3 aspect ratio."

// AssemblePrompt builds the full image generation prompt from a topic fragment.
func AssemblePrompt(topicFragment string) string {
	return promptPrefix + topicFragment + promptSuffix
}

// MetaPromptParts holds the system and user portions of the meta-prompt
// sent to Claude Sonnet for topic fragment generation.
type MetaPromptParts struct {
	System string
	User   string
}

const metaSystem = `You generate image prompt fragments for a cubist editorial illustration series. Each prompt describes a metaphorical scene with abstract figures representing a system design concept. Study the examples carefully — match their style, specificity, and structure. Output ONLY the prompt fragment, nothing else.`

const metaExamples = `Examples:

Title: "Rate Limiter"
→ an abstract figure as a calm gatekeeper, body assembled from geometric planes seen from multiple simultaneous perspectives in the style of Picasso. The figure gently directs a stream of colorful geometric shapes through a narrow passage with one hand raised.

Title: "News Feed"
→ abstract figures sharing and reading overlapping pages and glowing screens, bodies assembled from geometric planes seen from multiple simultaneous perspectives in the style of Picasso. Figures lean together, content flowing between them.

Title: "Chat System"
→ abstract figures in animated conversation, bodies assembled from geometric planes seen from multiple simultaneous perspectives in the style of Picasso. Speech bubbles and message fragments float between the figures as angular, overlapping shapes.`

// MetaPrompt returns the system and user prompt parts for generating
// a topic fragment from a question title via Claude Sonnet.
func MetaPrompt(questionTitle string) MetaPromptParts {
	return MetaPromptParts{
		System: metaSystem,
		User: fmt.Sprintf("%s\n\nNow generate a prompt fragment for:\nTitle: %q", metaExamples, questionTitle),
	}
}
```

- [ ] **Step 4: Run test**

```bash
go test ./internal/imagegen/ -v
```

Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/imagegen/
git commit -m "feat: add image prompt template and meta-prompt for Claude Sonnet"
```

---

### Task 6: Job A — GenerateQuestionImageWorker

**Files:**
- Create: `internal/jobs/generate_image.go`
- Create: `internal/jobs/generate_image_test.go`

- [ ] **Step 1: Define job args and insert opts**

Create `internal/jobs/generate_image.go`:

```go
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

// GenerateQuestionImageInsertOpts returns River insert options.
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
		return fmt.Errorf("get question %s: %w", questionID, err)
	}

	// 2. Idempotent: skip if image already exists.
	if question.ImageUrl.Valid {
		slog.Info("question already has image, skipping",
			"question_id", questionID,
			"image_url", question.ImageUrl.String)
		return nil
	}

	// 3. Generate topic fragment via Claude Sonnet.
	meta := imagegen.MetaPrompt(question.Title)
	tx, err := w.Pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	topicFragment, err := w.LLM.CallAndLog(ctx, tx, ai.CallParams{
		Model:     w.Cfg.LLM.ImagePromptModel,
		System:    meta.System,
		Messages:  []anthropic.MessageParam{anthropic.NewUserMessage(anthropic.NewTextBlock(meta.User))},
		MaxTokens: w.Cfg.LLM.ImagePromptMaxTokens,
		UserID:    question.UserID.Bytes,
		Role:      "image_prompt_generator",
		SessionID: uuid.Nil,
	})
	if err != nil {
		return fmt.Errorf("generate image prompt: %w", err)
	}

	// 4. Assemble full image prompt.
	fullPrompt := imagegen.AssemblePrompt(topicFragment)

	// 5. Generate image via Nano Banana 2.
	geminiStart := time.Now()
	imageBytes, mimeType, err := w.Gemini.GenerateImage(ctx, fullPrompt)
	geminiLatency := time.Since(geminiStart)
	if err != nil {
		return fmt.Errorf("generate image: %w", err)
	}

	// 6. Upload to public GCS bucket.
	key := fmt.Sprintf("questions/%s/card.png", questionID)
	if mimeType == "" {
		mimeType = "image/png"
	}
	_, err = w.PublicStore.Put(ctx, key, imageBytes, mimeType)
	if err != nil {
		return fmt.Errorf("upload image: %w", err)
	}

	// 7. Construct public HTTPS URL.
	imageURL := fmt.Sprintf("https://storage.googleapis.com/%s/%s",
		w.Cfg.Storage.PublicBucket, key)

	// 8. In transaction: update question + log Gemini call.
	txq := db.New(tx)
	if err := txq.SetQuestionImageURL(ctx, db.SetQuestionImageURLParams{
		ID:       questionID,
		ImageUrl: pgtype.Text{String: imageURL, Valid: true},
	}); err != nil {
		return fmt.Errorf("set image url: %w", err)
	}

	// Log the Gemini call (not auto-logged since it's not via ai.Client).
	// numericFromFloat is in ai/client.go — export it or duplicate the helper.
	geminiCallID, err := txq.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:     pgtype.UUID{},
		UserID:        question.UserID,
		Role:          "image_generator",
		Model:         w.Cfg.Gemini.Model,
		InputTokens:   0, // Gemini image gen doesn't report token counts
		OutputTokens:  0,
		EstimatedCost: ai.NumericFromFloat(0.067),
		LatencyMs:     int32(geminiLatency.Milliseconds()),
	})
	if err != nil {
		return fmt.Errorf("log gemini call: %w", err)
	}
	if err := txq.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: geminiCallID,
		Prompt:    pgtype.Text{String: fullPrompt, Valid: true},
		Response:  pgtype.Text{String: imageURL, Valid: true},
	}); err != nil {
		return fmt.Errorf("log gemini call content: %w", err)
	}

	// 9. Commit.
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}

	slog.Info("question image generated",
		"question_id", questionID,
		"image_url", imageURL)
	return nil
}
```

Note: `numericFromFloat` is an unexported helper in `ai/client.go`. Either export it as `ai.NumericFromFloat` or duplicate the logic inline. The function converts a `float64` to `pgtype.Numeric` with 10 decimal places of precision.

- [ ] **Step 2: Write test**

Create `internal/jobs/generate_image_test.go`. This test verifies the idempotency check (skip when image_url is already set) and the overall flow using mocks:

```go
package jobs_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

func TestGenerateQuestionImageArgs_Kind(t *testing.T) {
	args := jobs.GenerateQuestionImageArgs{QuestionID: uuid.New()}
	require.Equal(t, "generate_question_image", args.Kind())
}

func TestSweepMissingImagesArgs_Kind(t *testing.T) {
	args := jobs.SweepMissingImagesArgs{}
	require.Equal(t, "sweep_missing_images", args.Kind())
}
```

- [ ] **Step 3: Run test**

```bash
go test ./internal/jobs/ -run TestGenerateQuestionImage -v
go test ./internal/jobs/ -run TestSweepMissing -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/jobs/generate_image.go internal/jobs/generate_image_test.go
git commit -m "feat: add GenerateQuestionImageWorker"
```

---

### Task 7: Job B — SweepMissingImagesWorker

**Files:**
- Create: `internal/jobs/sweep_images.go`

- [ ] **Step 1: Implement sweep worker**

Create `internal/jobs/sweep_images.go`:

```go
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
)

// SweepMissingImagesArgs are the arguments for the periodic sweep job.
type SweepMissingImagesArgs struct{}

func (SweepMissingImagesArgs) Kind() string { return "sweep_missing_images" }

// SweepMissingImagesWorker finds questions without images and enqueues
// GenerateQuestionImage jobs for each.
type SweepMissingImagesWorker struct {
	river.WorkerDefaults[SweepMissingImagesArgs]
	Pool *pgxpool.Pool
	Jobs *river.Client[pgx.Tx]
}

func (w *SweepMissingImagesWorker) Timeout(job *river.Job[SweepMissingImagesArgs]) time.Duration {
	return 1 * time.Minute
}

func (w *SweepMissingImagesWorker) Work(ctx context.Context, job *river.Job[SweepMissingImagesArgs]) error {
	q := db.New(w.Pool)

	ids, err := q.ListQuestionsWithoutImages(ctx, 50)
	if err != nil {
		return fmt.Errorf("list questions without images: %w", err)
	}

	if len(ids) == 0 {
		return nil
	}

	enqueued := 0
	for _, id := range ids {
		_, err := w.Jobs.Insert(ctx, GenerateQuestionImageArgs{
			QuestionID: id,
		}, GenerateQuestionImageInsertOpts())
		if err != nil {
			slog.Error("sweep: failed to enqueue image generation",
				"question_id", id, "error", err)
			continue
		}
		enqueued++
	}

	slog.Info("sweep: enqueued image generation jobs",
		"found", len(ids), "enqueued", enqueued)
	return nil
}
```

Add the `pgx` import: `"github.com/jackc/pgx/v5"`.

- [ ] **Step 2: Commit**

```bash
git add internal/jobs/sweep_images.go
git commit -m "feat: add SweepMissingImagesWorker"
```

---

### Task 8: Worker Registration + DI Wiring

**Files:**
- Modify: `internal/jobs/workers.go`
- Modify: `internal/backend/backend.go`

- [ ] **Step 1: Update RegisterWorkers**

In `internal/jobs/workers.go`, add the new workers to `WorkerRefs` and `RegisterWorkers`:

Add to `WorkerRefs`:

```go
type WorkerRefs struct {
	Evaluate          *EvaluateSessionWorker
	Cleanup           *CleanupAbandonedSessionsWorker
	CleanupGenerating *CleanupStaleGeneratingWorker
	Educator          *GenerateEducatorContentWorker
	Coach             *RunCoachAnalysisWorker
	ImageGen          *GenerateQuestionImageWorker
	SweepImages       *SweepMissingImagesWorker
}
```

Update `RegisterWorkers` signature to accept the new dependencies:

```go
func RegisterWorkers(cfg *config.Config, sender email.Sender, pool *pgxpool.Pool, llm *ai.Client, gemini *ai.GeminiClient, publicStore storage.ObjectStore) (*river.Workers, WorkerRefs) {
```

Add worker registration inside the function:

```go
	imgGen := &GenerateQuestionImageWorker{Pool: pool, LLM: llm, Gemini: gemini, PublicStore: publicStore, Cfg: cfg}
	river.AddWorker(workers, imgGen)
	sweep := &SweepMissingImagesWorker{Pool: pool}
	river.AddWorker(workers, sweep)
```

Return them in `WorkerRefs`:

```go
	return workers, WorkerRefs{
		Evaluate: eval, Cleanup: cleanup, CleanupGenerating: cleanupGenerating,
		Educator: edu, Coach: coachWorker, ImageGen: imgGen, SweepImages: sweep,
	}
```

- [ ] **Step 2: Update Backend.New to create Gemini client and public store**

In `internal/backend/backend.go`, in the `New` function:

After the existing object storage initialization (around line 46-62), add public store creation:

```go
	// Public object storage for question images.
	var publicStore storage.ObjectStore
	switch cfg.Storage.Backend {
	case "gcs":
		if cfg.Storage.PublicBucket != "" {
			publicStore, err = storage.NewGCS(context.Background(), cfg.Storage.PublicBucket)
			if err != nil {
				return nil, fmt.Errorf("public gcs storage: %w", err)
			}
			slog.Info("public storage: gcs", "bucket", cfg.Storage.PublicBucket)
		}
	case "local":
		publicStore, err = storage.NewLocal(cfg.Storage.LocalDir + "/public")
		if err != nil {
			return nil, fmt.Errorf("public local storage: %w", err)
		}
		slog.Info("public storage: local")
	}
```

After the AI clients initialization (around line 97-99), add Gemini client:

```go
	// Gemini client for image generation (optional — nil if not configured).
	var geminiClient *ai.GeminiClient
	if cfg.Otel.GCPProjectID != "" && cfg.Gemini.Model != "" {
		geminiClient, err = ai.NewGeminiClient(
			context.Background(),
			cfg.Otel.GCPProjectID,
			cfg.Gemini.Location,
			cfg.Gemini.Model,
		)
		if err != nil {
			slog.Warn("gemini client creation failed, image generation disabled", "error", err)
		} else {
			slog.Info("gemini client initialized", "model", cfg.Gemini.Model)
		}
	}
```

Update the `RegisterWorkers` call to pass new dependencies:

```go
	workers, workerRefs := jobs.RegisterWorkers(cfg, emailSender, pool, llmClient, geminiClient, publicStore)
```

Wire up the sweep worker's Jobs field after river client creation:

```go
	workerRefs.Evaluate.Jobs = riverClient
	workerRefs.Cleanup.Jobs = riverClient
	workerRefs.SweepImages.Jobs = riverClient
```

Add the sweep periodic job to the `PeriodicJobs` slice:

```go
			river.NewPeriodicJob(
				river.PeriodicInterval(5*time.Minute),
				func() (river.JobArgs, *river.InsertOpts) {
					return jobs.SweepMissingImagesArgs{}, nil
				},
				nil,
			),
```

Update `Close` to close public store if it exists (add after `b.store.Close()`):

Check if `publicStore` is stored on Backend. If the Backend struct needs it for Close, add a `publicStore` field. Otherwise, if it's only used by the worker (which holds its own reference), no Backend field is needed.

- [ ] **Step 3: Run build**

```bash
go build ./...
```

Fix any compilation errors.

- [ ] **Step 4: Run existing tests**

```bash
go test ./internal/backend/... -v -count=1
go test ./internal/jobs/... -v -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/jobs/workers.go internal/backend/backend.go
git commit -m "feat: wire up image generation workers and dependencies"
```

---

### Task 9: Transactional Enqueue on Question Creation

**Files:**
- Modify: `internal/jobs/coach.go` (coach-generated questions)

The coach worker already creates questions via `txq.InsertQuestion`. Add a transactional enqueue of Job A right after the insert.

- [ ] **Step 1: Add enqueue in coach worker**

In `internal/jobs/coach.go`, after the `InsertQuestion` call (around line 138), add:

```go
		suggestedQuestionID = pgtype.UUID{Bytes: qID, Valid: true}

		// Enqueue image generation for the new question.
		_, err = w.Jobs.InsertTx(ctx, tx, GenerateQuestionImageArgs{
			QuestionID: qID,
		}, GenerateQuestionImageInsertOpts())
		if err != nil {
			return fmt.Errorf("enqueue image generation for coach question: %w", err)
		}
```

`RunCoachAnalysisWorker` does not currently have a `Jobs` field. Add it:

```go
type RunCoachAnalysisWorker struct {
	river.WorkerDefaults[RunCoachAnalysisArgs]
	Pool *pgxpool.Pool
	LLM  *ai.Client
	Cfg  *config.LLM
	Jobs *river.Client[pgx.Tx]
}
```

And wire it in `backend.go`:

```go
	workerRefs.Coach.Jobs = riverClient
```

Note: The REST endpoint for user-created questions (`POST /api/questions`) also needs transactional enqueue. If the endpoint exists, add the same pattern there. If it doesn't exist yet, Job B (sweep) handles these within 5 minutes. The REST → ConnectRPC migration of CreateQuestion is out of scope but should include transactional enqueue when done.

- [ ] **Step 2: Run tests**

```bash
go test ./internal/jobs/... -v -count=1
```

- [ ] **Step 3: Commit**

```bash
git add internal/jobs/coach.go internal/backend/backend.go
git commit -m "feat: transactionally enqueue image generation for coach-generated questions"
```

---

### Task 10: Frontend — Borderless Grid + QuestionCard + HeroCard

**Files:**
- Modify: `web/src/pages/home.tsx`

- [ ] **Step 1: Rewrite QuestionCard to borderless image card**

Replace the `QuestionCard` component in `web/src/pages/home.tsx`:

```tsx
function QuestionCard({
  question,
  startDisabled,
}: {
  question: ProtoQuestion;
  startDisabled: boolean;
}) {
  const href = startDisabled ? undefined : `/sessions/new?question=${question.id}`;

  const image = question.imageUrl ? (
    <img
      src={question.imageUrl}
      alt={question.title}
      className="w-full aspect-[4/3] object-cover rounded-[14px] transition-transform duration-250 ease-out group-hover:scale-[1.02]"
    />
  ) : (
    <div
      className="w-full aspect-[4/3] rounded-[14px]"
      style={{ background: "linear-gradient(135deg, hsl(32 40% 85%), hsl(24 30% 75%))" }}
    />
  );

  const content = (
    <>
      {image}
      <div className="pt-2.5 px-0.5 space-y-1">
        <span className="text-sm font-semibold">{question.title}</span>
        <div className="flex items-center gap-1.5 flex-wrap">
          <Badge variant={difficultyVariant(question.difficulty)}>
            {difficultyLabel(question.difficulty)}
          </Badge>
          {question.source === QuestionSource.CUSTOM && (
            <Badge variant="outline">Custom</Badge>
          )}
          {question.source === QuestionSource.COACH_GENERATED && (
            <Badge variant="outline" className="text-amber-600 dark:text-amber-400 border-amber-400/50">
              Coach
            </Badge>
          )}
          {question.tags.map((tag) => (
            <span key={tag} className="text-xs text-muted-foreground">{tag}</span>
          ))}
        </div>
      </div>
    </>
  );

  if (startDisabled) {
    return (
      <div className="group opacity-60 cursor-not-allowed">
        {content}
      </div>
    );
  }

  return (
    <Link to={href!} className="group no-underline text-inherit">
      {content}
    </Link>
  );
}
```

- [ ] **Step 2: Add HeroQuestionCard component**

Add a new `HeroQuestionCard` component above `QuestionCard`:

```tsx
function HeroQuestionCard({
  question,
  startDisabled,
}: {
  question: ProtoQuestion;
  startDisabled: boolean;
}) {
  const href = startDisabled ? undefined : `/sessions/new?question=${question.id}`;

  const image = question.imageUrl ? (
    <img
      src={question.imageUrl}
      alt={question.title}
      className="w-[55%] aspect-[4/3] object-cover rounded-l-[14px] flex-shrink-0"
    />
  ) : (
    <div
      className="w-[55%] aspect-[4/3] rounded-l-[14px] flex-shrink-0"
      style={{ background: "linear-gradient(135deg, hsl(32 40% 85%), hsl(24 30% 75%))" }}
    />
  );

  const content = (
    <div className="flex rounded-[14px] border border-border bg-card overflow-hidden transition-shadow hover:shadow-lg">
      {image}
      <div className="flex flex-col justify-center px-7 py-6 flex-1">
        <span className="text-xs font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400 mb-2">
          Recommended by Coach
        </span>
        <span className="text-xl font-bold mb-2">{question.title}</span>
        <div className="flex items-center gap-1.5 flex-wrap">
          <Badge variant={difficultyVariant(question.difficulty)}>
            {difficultyLabel(question.difficulty)}
          </Badge>
          {question.tags.map((tag) => (
            <span key={tag} className="text-xs text-muted-foreground">{tag}</span>
          ))}
        </div>
      </div>
    </div>
  );

  if (startDisabled) {
    return <div className="opacity-60 cursor-not-allowed col-span-full">{content}</div>;
  }

  return (
    <Link to={href!} className="no-underline text-inherit col-span-full">
      {content}
    </Link>
  );
}
```

- [ ] **Step 3: Update the Home component grid layout**

In the `Home` component, replace the question list section (the `<div className="space-y-2">` block) with the new grid:

```tsx
      {/* Question grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-8">
        {suggestedId && sorted.find((q) => q.id === suggestedId) && (
          <HeroQuestionCard
            question={sorted.find((q) => q.id === suggestedId)!}
            startDisabled={atConcurrentLimit}
          />
        )}
        {sorted
          .filter((q) => q.id !== suggestedId)
          .map((q) => (
            <QuestionCard
              key={q.id}
              question={q}
              startDisabled={atConcurrentLimit}
            />
          ))}
        {sorted.length === 0 && (
          <p className="text-sm text-muted-foreground py-8 text-center col-span-full">
            No questions match your filters.
          </p>
        )}
      </div>
```

Remove the `suggestedId` prop from `QuestionCard` since the hero handles recommendation display separately.

- [ ] **Step 4: Update loading skeleton**

Update the loading skeleton in the `if (!user)` block to match the grid layout:

```tsx
    return (
      <div className="space-y-6">
        <Skeleton className="h-5 w-64" />
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-8">
          <Skeleton className="aspect-[4/3] rounded-[14px]" />
          <Skeleton className="aspect-[4/3] rounded-[14px]" />
          <Skeleton className="aspect-[4/3] rounded-[14px]" />
        </div>
      </div>
    );
```

- [ ] **Step 5: Verify in browser**

```bash
cd web && npm run dev
```

Open http://localhost:3000, verify:
- Grid layout renders (3 columns on desktop)
- Gradient placeholders show for questions without images
- Hover animation works on cards
- Responsive: check 2-column and 1-column at narrower widths
- If coach recommendation exists, hero card renders full-width above the grid

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/home.tsx
git commit -m "feat: rewrite home page to borderless image grid with hero card"
```

---

### Task 11: Final Integration Verification

- [ ] **Step 1: Run full test suite**

```bash
go test ./... -count=1
```

Fix any failures.

- [ ] **Step 2: Build backend**

```bash
go build ./cmd/drill/
```

- [ ] **Step 3: Build frontend**

```bash
cd web && npm run build
```

- [ ] **Step 4: Verify in browser**

Run the full app locally. Check:
- Home page loads with new grid layout
- Questions without images show gradient placeholders
- The sweep worker log line appears after 5 minutes (or trigger manually)
- If Gemini is configured, images generate successfully

- [ ] **Step 5: Commit any fixes**

```bash
git add -u
git commit -m "fix: integration fixes for question card images"
```
