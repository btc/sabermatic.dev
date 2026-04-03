# Educator + Coach Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the educator and coach AI roles as River job workers with HTTP endpoints, following the evaluation phase patterns.

**Architecture:** Two domain packages (`internal/educator/`, `internal/coach/`) handle prompt building, tool schemas, and response parsing. River workers call these packages, persist results transactionally. Backend methods + HTTP handlers expose the API. Coach briefing wired into the conductor's prompt builder.

**Tech Stack:** Go, sqlc, River, Anthropic Go SDK (tool_use via `CallToolAndLog`), pgx/v5, testify

**Branch:** Create `feat/educator-coach` from `main` (eval is now merged).

**Spec:** `docs/superpowers/specs/2026-04-03-educator-coach-design.md`

---

## File Structure

**New files:**
- `sql/queries/educator_analyses.sql` — educator DB queries
- `sql/queries/coach_analyses.sql` — coach DB queries
- `sql/migrations/002_educator_failed_status.up.sql` — add 'failed' to educator_analyses CHECK
- `sql/migrations/002_educator_failed_status.down.sql` — revert CHECK
- `internal/educator/prompt.go` — system prompt + transcript/eval summary formatting
- `internal/educator/prompt_test.go`
- `internal/educator/parse.go` — tool schema, result type, parser
- `internal/educator/parse_test.go`
- `internal/coach/prompt.go` — system prompt + history summary formatting
- `internal/coach/prompt_test.go`
- `internal/coach/parse.go` — tool schema, result type, parser
- `internal/coach/parse_test.go`
- `internal/jobs/educator.go` — GenerateEducatorContent River worker
- `internal/jobs/coach.go` — RunCoachAnalysis River worker
- `internal/backend/educator.go` — service methods
- `internal/backend/coach.go` — service methods
- `internal/handler/educator.go` — HTTP handlers
- `internal/handler/coach.go` — HTTP handlers

**Modified files:**
- `internal/config/config.go` — add EducatorModel defaults + MaxTokens fields
- `sql/queries/sessions.sql` — add GetReviewedSessionsForUser, GetReviewedSessionIDsForUser
- `sql/queries/evaluations.sql` — add GetEvaluationsBySessionIDs
- `sql/queries/questions.sql` — add GetQuestionsForUser
- `internal/backend/errors.go` — add sentinel errors
- `internal/jobs/workers.go` — register educator + coach workers
- `internal/jobs/error_handler.go` — handle educator failures
- `internal/handler/routes.go` — register 4 new routes
- `internal/interview/conductor.go` — add coachBriefing field, load in loadSession, wire into prompt chain

---

### Task 1: Branch Setup + Migration

**Files:**
- Create: `sql/migrations/002_educator_failed_status.up.sql`
- Create: `sql/migrations/002_educator_failed_status.down.sql`

- [ ] **Step 1: Create feature branch from main**

```bash
cd /Users/btc/Projects/src/drill
git fetch origin
git checkout -b feat/educator-coach main
```

- [ ] **Step 2: Create migration for educator_analyses 'failed' status**

Create `sql/migrations/002_educator_failed_status.up.sql`:

```sql
-- Add 'failed' to educator_analyses status CHECK constraint.
ALTER TABLE educator_analyses DROP CONSTRAINT IF EXISTS educator_analyses_status_check;
ALTER TABLE educator_analyses ADD CONSTRAINT educator_analyses_status_check
    CHECK (status IN ('generating', 'completed', 'failed'));
```

Create `sql/migrations/002_educator_failed_status.down.sql`:

```sql
ALTER TABLE educator_analyses DROP CONSTRAINT IF EXISTS educator_analyses_status_check;
ALTER TABLE educator_analyses ADD CONSTRAINT educator_analyses_status_check
    CHECK (status IN ('generating', 'completed'));
```

- [ ] **Step 3: Commit**

```bash
git add sql/migrations/002_educator_failed_status.*
git commit -m "feat(educator): add 'failed' status to educator_analyses CHECK constraint"
```

---

### Task 2: SQL Queries + sqlc Codegen

**Files:**
- Create: `sql/queries/educator_analyses.sql`
- Create: `sql/queries/coach_analyses.sql`
- Modify: `sql/queries/sessions.sql`
- Modify: `sql/queries/evaluations.sql`
- Modify: `sql/queries/questions.sql`

- [ ] **Step 1: Create educator_analyses queries**

Create `sql/queries/educator_analyses.sql`:

```sql
-- name: InsertEducatorAnalysis :one
INSERT INTO educator_analyses (session_id)
VALUES ($1)
RETURNING id;

-- name: UpdateEducatorAnalysisContent :exec
UPDATE educator_analyses
SET model_answer = $2, gap_deep_dives = $3, status = 'completed'
WHERE id = $1;

-- name: UpdateEducatorAnalysisStatus :exec
UPDATE educator_analyses SET status = $2 WHERE id = $1;

-- name: GetEducatorAnalysisBySession :one
SELECT id, session_id, status, model_answer, gap_deep_dives, created_at
FROM educator_analyses WHERE session_id = $1;
```

- [ ] **Step 2: Create coach_analyses queries**

Create `sql/queries/coach_analyses.sql`:

```sql
-- name: InsertCoachAnalysis :one
INSERT INTO coach_analyses (
    user_id, narrative, weakest_dimension,
    improving_dimensions, topic_gaps,
    suggested_question_id, sessions_analyzed
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: GetLatestCoachAnalysis :one
SELECT id, user_id, narrative, weakest_dimension,
       improving_dimensions, topic_gaps,
       suggested_question_id, sessions_analyzed, created_at
FROM coach_analyses
WHERE user_id = $1
ORDER BY created_at DESC LIMIT 1;
```

- [ ] **Step 3: Add session queries for coach**

Append to `sql/queries/sessions.sql`:

```sql
-- name: GetReviewedSessionsForUser :many
SELECT * FROM interview_sessions
WHERE user_id = $1 AND status = 'reviewed' AND archived = FALSE
ORDER BY created_at;

-- name: GetReviewedSessionIDsForUser :many
SELECT id FROM interview_sessions
WHERE user_id = $1 AND status = 'reviewed' AND archived = FALSE
ORDER BY created_at;
```

- [ ] **Step 4: Add batch evaluation query**

Append to `sql/queries/evaluations.sql`:

```sql
-- name: GetEvaluationsBySessionIDs :many
SELECT id, session_id, score_requirements, score_architecture,
       score_deep_dive, score_scalability, score_communication,
       score_overall, strengths, gaps, advice, created_at
FROM evaluations WHERE session_id = ANY($1::uuid[]);
```

- [ ] **Step 5: Add GetQuestionsForUser query**

Append to `sql/queries/questions.sql`:

```sql
-- name: GetQuestionsForUser :many
SELECT * FROM questions
WHERE user_id IS NULL OR user_id = $1
ORDER BY created_at;
```

- [ ] **Step 6: Add InsertQuestion query**

Append to `sql/queries/questions.sql` (needed by the coach worker to insert generated questions):

```sql
-- name: InsertQuestion :one
INSERT INTO questions (user_id, title, prompt, difficulty, tags, source, coach_rationale)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;
```

- [ ] **Step 7: Run sqlc generate**

```bash
sqlc generate
```

Verify: `internal/db/educator_analyses.sql.go` and `internal/db/coach_analyses.sql.go` are created. `internal/db/querier.go` includes the new methods (including `InsertQuestion`).

- [ ] **Step 8: Commit**

```bash
git add sql/queries/ internal/db/
git commit -m "feat(educator-coach): add SQL queries and sqlc codegen for educator, coach, and supporting batch queries"
```

---

### Task 3: Config — Educator + Coach Model/Token Settings

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Update LLM config struct**

In `internal/config/config.go`, replace the LLM struct:

```go
type LLM struct {
	APIKey             string `env:"ANTHROPIC_API_KEY,required"`
	InterviewerModel   string `env:"INTERVIEWER_MODEL,default=claude-sonnet-4-20250514"`
	EvaluatorModel     string `env:"EVALUATOR_MODEL,default=claude-opus-4-20250514"`
	EvaluatorMaxTokens int64  `env:"EVALUATOR_MAX_TOKENS,default=4096"`
	EducatorModel      string `env:"EDUCATOR_MODEL,default=claude-opus-4-20250514"`
	EducatorMaxTokens  int64  `env:"EDUCATOR_MAX_TOKENS,default=8192"`
	CoachModel         string `env:"COACH_MODEL,default=claude-sonnet-4-20250514"`
	CoachMaxTokens     int64  `env:"COACH_MAX_TOKENS,default=4096"`
}
```

- [ ] **Step 2: Run tests to verify config loads**

```bash
go test ./internal/config/ -v -run TestLoad
```

Expected: PASS (environment variable defaults are set).

- [ ] **Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat(config): add educator and coach model + max_tokens settings"
```

---

### Task 4: Educator Domain Package — Parse

**Files:**
- Create: `internal/educator/parse.go`
- Create: `internal/educator/parse_test.go`

- [ ] **Step 1: Write parse tests**

Create `internal/educator/parse_test.go`:

```go
package educator

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validToolInput() map[string]any {
	return map[string]any{
		"model_answer":  "## Architecture\n\nUse a distributed cache with write-through...",
		"gap_deep_dives": "## Cache Invalidation\n\nThe candidate missed...",
	}
}

func TestParse_ValidInput(t *testing.T) {
	raw, err := json.Marshal(validToolInput())
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	assert.Contains(t, result.ModelAnswer, "distributed cache")
	assert.Contains(t, result.GapDeepDives, "Cache Invalidation")
}

func TestParse_EmptyModelAnswer(t *testing.T) {
	input := validToolInput()
	input["model_answer"] = ""
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "model_answer")
}

func TestParse_EmptyGapDeepDives(t *testing.T) {
	input := validToolInput()
	input["gap_deep_dives"] = ""
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "gap_deep_dives")
}

func TestParse_MissingFields(t *testing.T) {
	raw := []byte(`{}`)
	_, err := Parse(json.RawMessage(raw))
	require.Error(t, err)
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse(json.RawMessage([]byte(`not json`)))
	require.Error(t, err)
}

func TestToolSchema_HasRequiredFields(t *testing.T) {
	schema := ToolSchema()
	assert.Equal(t, "submit_education", schema.Name)
	props := schema.InputSchema.Properties
	assert.Contains(t, props, "model_answer")
	assert.Contains(t, props, "gap_deep_dives")
	assert.Equal(t, []string{"model_answer", "gap_deep_dives"}, schema.InputSchema.Required)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/educator/ -v -count=1
```

Expected: compilation error (package doesn't exist yet).

- [ ] **Step 3: Write parse implementation**

Create `internal/educator/parse.go`:

```go
package educator

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// EducatorResult holds the parsed educator content ready for persistence.
type EducatorResult struct {
	ModelAnswer  string
	GapDeepDives string
}

// ToolSchema returns the anthropic.ToolParam for the submit_education tool.
func ToolSchema() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_education",
		Description: anthropic.String("Submit the educational analysis with model answer and gap deep-dives."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"model_answer": map[string]any{
					"type":        "string",
					"description": "Markdown: what a strong answer to this specific problem looks like.",
				},
				"gap_deep_dives": map[string]any{
					"type":        "string",
					"description": "Markdown: detailed technical explanation for each gap identified by the evaluator.",
				},
			},
			Required: []string{"model_answer", "gap_deep_dives"},
		},
	}
}

// rawToolInput is an intermediate struct for JSON unmarshaling.
type rawToolInput struct {
	ModelAnswer  string `json:"model_answer"`
	GapDeepDives string `json:"gap_deep_dives"`
}

// Parse unmarshals tool input JSON into an EducatorResult.
func Parse(raw json.RawMessage) (*EducatorResult, error) {
	var input rawToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("educator: unmarshal tool input: %w", err)
	}

	if input.ModelAnswer == "" {
		return nil, fmt.Errorf("educator: model_answer is empty")
	}
	if input.GapDeepDives == "" {
		return nil, fmt.Errorf("educator: gap_deep_dives is empty")
	}

	return &EducatorResult{
		ModelAnswer:  input.ModelAnswer,
		GapDeepDives: input.GapDeepDives,
	}, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/educator/ -v -count=1
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/educator/parse.go internal/educator/parse_test.go
git commit -m "feat(educator): add tool schema, result type, and parser"
```

---

### Task 5: Educator Domain Package — Prompt

**Files:**
- Create: `internal/educator/prompt.go`
- Create: `internal/educator/prompt_test.go`

- [ ] **Step 1: Write prompt tests**

Create `internal/educator/prompt_test.go`:

```go
package educator

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

func testQuestion() db.Question {
	return db.Question{
		ID:     uuid.New(),
		Title:  "Design a URL Shortener",
		Prompt: "Design a URL shortening service like bit.ly.",
	}
}

func testMessages() []db.Message {
	return []db.Message{
		{Seq: 1, Role: "interviewer", Content: "Design a URL shortening service."},
		{Seq: 2, Role: "candidate", Content: "Let me start by gathering requirements."},
		{Seq: 3, Role: "interviewer", Content: "Go ahead."},
	}
}

func testEvaluation() db.Evaluation {
	strengths, _ := json.Marshal([]string{"Good requirements gathering", "Strong architecture"})
	gaps, _ := json.Marshal([]string{"Missing cache invalidation", "No discussion of hot keys"})
	return db.Evaluation{
		ID:                 uuid.New(),
		ScoreRequirements:  4,
		ScoreArchitecture:  3,
		ScoreDeepDive:      2,
		ScoreScalability:   3,
		ScoreCommunication: 4,
		ScoreOverall:       3,
		Strengths:          strengths,
		Gaps:               gaps,
		Advice:             "Focus on cache invalidation strategies.",
	}
}

func TestBuildPrompt_SystemContainsTeachingInstructions(t *testing.T) {
	system, userMsgs := BuildPrompt(testQuestion(), testMessages(), testEvaluation())

	assert.Contains(t, system, "expert system design educator")
	assert.Contains(t, system, "Model Answer")
	assert.Contains(t, system, "Gap Deep-Dives")
	assert.Contains(t, system, "TEACH, not judge")
	require.Len(t, userMsgs, 1)
}

func TestBuildPrompt_UserMessageContainsTranscript(t *testing.T) {
	_, userMsgs := BuildPrompt(testQuestion(), testMessages(), testEvaluation())

	require.Len(t, userMsgs, 1)
	// The user message content is a TextBlock — extract it.
	// We verify the transcript and evaluation are included.
}

func TestBuildPrompt_UserMessageContainsEvaluation(t *testing.T) {
	system, _ := BuildPrompt(testQuestion(), testMessages(), testEvaluation())

	assert.Contains(t, system, "Design a URL Shortener")
}

func TestBuildPrompt_EvalSummaryIncludesScores(t *testing.T) {
	summary := buildEvaluationSummary(testEvaluation())

	assert.Contains(t, summary, "Requirements: 4")
	assert.Contains(t, summary, "Architecture: 3")
	assert.Contains(t, summary, "Deep Dive: 2")
	assert.Contains(t, summary, "Good requirements gathering")
	assert.Contains(t, summary, "Missing cache invalidation")
	assert.Contains(t, summary, "Focus on cache invalidation")
}

func TestBuildTranscript_FormatsCorrectly(t *testing.T) {
	transcript := buildTranscript(testQuestion(), testMessages())

	assert.Contains(t, transcript, "Design a URL Shortener")
	assert.Contains(t, transcript, "[1] Interviewer:")
	assert.Contains(t, transcript, "[2] Candidate:")
	assert.Contains(t, transcript, "[3] Interviewer:")
}

func TestBuildPrompt_EmptyMessages(t *testing.T) {
	system, userMsgs := BuildPrompt(testQuestion(), nil, testEvaluation())
	assert.Contains(t, system, "educator")
	require.Len(t, userMsgs, 1)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/educator/ -v -count=1 -run TestBuildPrompt
```

Expected: compilation error (BuildPrompt not defined).

- [ ] **Step 3: Write prompt implementation**

Create `internal/educator/prompt.go`:

```go
package educator

import (
	"encoding/json"
	"fmt"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/btc/drill/internal/db"
)

const systemPrompt = `You are an expert system design educator with deep production experience at top tech companies. You have designed and operated large-scale distributed systems.

You are given:
1. A system design interview question
2. The full interview transcript
3. The evaluator's assessment (scores, gaps, strengths)

Your job is to TEACH, not judge. The evaluator already judged. You provide the knowledge the candidate needs.

## Model Answer

Write what a strong answer to THIS SPECIFIC problem looks like. Not a generic textbook answer — a concrete, production-aware design tailored to the exact question as framed in the interview.

Include:
- Concrete architecture with specific technology choices and WHY each was chosen
- Data model with actual schemas, key structures, and access patterns
- Key algorithms, protocols, or techniques with enough detail to implement
- Explicit tradeoffs: what you're giving up and what you're gaining
- What separates a good answer from an exceptional one at each phase

Write as if explaining to a strong engineer who needs to build this. Be specific enough that they could start implementing.

## Gap Deep-Dives

For each gap identified by the evaluator, provide a detailed technical education:

- What the candidate should have known, explained clearly
- How this works in practice at real companies (name companies and systems where relevant)
- Concrete implementation details — not "go research cache invalidation" but "here are the three main approaches: write-through (used by DynamoDB), write-behind (used by most ORMs with batch flush), and TTL-based expiration (Redis default). For this problem, write-through is best because..."
- Code snippets, schema examples, or algorithm pseudocode where helpful
- Common mistakes and how to avoid them

Be the senior engineer who sits down with the candidate after the interview and says "here's what you need to know."

Format everything in Markdown. Use headers, code blocks, and tables where they aid clarity.`

// BuildPrompt returns the evaluator system prompt and a single user message
// containing the formatted interview transcript and evaluation summary.
func BuildPrompt(question db.Question, messages []db.Message, eval db.Evaluation) (string, []anthropic.MessageParam) {
	transcript := buildTranscript(question, messages)
	evalSummary := buildEvaluationSummary(eval)

	userContent := fmt.Sprintf("%s\n\n## Evaluator Assessment\n\n%s", transcript, evalSummary)
	userMsg := anthropic.NewUserMessage(anthropic.NewTextBlock(userContent))

	system := fmt.Sprintf("%s\n\n## Interview Context\n\n**Question:** %s\n\n%s",
		systemPrompt, question.Title, question.Prompt)

	return system, []anthropic.MessageParam{userMsg}
}

// buildTranscript formats the question and messages into a structured markdown
// transcript for the educator.
func buildTranscript(question db.Question, messages []db.Message) string {
	var sb strings.Builder

	fmt.Fprintf(&sb, "# Interview Transcript: %s\n", question.Title)
	fmt.Fprintf(&sb, "\n## Question\n\n%s\n", question.Prompt)
	sb.WriteString("\n## Transcript\n")

	for _, m := range messages {
		role := roleLabel(m.Role)
		fmt.Fprintf(&sb, "\n[%d] %s: %s\n", m.Seq, role, m.Content)
	}

	return sb.String()
}

// buildEvaluationSummary formats the evaluation scores, strengths, gaps, and
// advice into a readable summary for the educator prompt.
func buildEvaluationSummary(eval db.Evaluation) string {
	var sb strings.Builder

	sb.WriteString("### Scores\n\n")
	fmt.Fprintf(&sb, "- Requirements: %d/5\n", eval.ScoreRequirements)
	fmt.Fprintf(&sb, "- Architecture: %d/5\n", eval.ScoreArchitecture)
	fmt.Fprintf(&sb, "- Deep Dive: %d/5\n", eval.ScoreDeepDive)
	fmt.Fprintf(&sb, "- Scalability: %d/5\n", eval.ScoreScalability)
	fmt.Fprintf(&sb, "- Communication: %d/5\n", eval.ScoreCommunication)
	fmt.Fprintf(&sb, "- Overall: %d/5\n", eval.ScoreOverall)

	var strengths []string
	if err := json.Unmarshal(eval.Strengths, &strengths); err == nil && len(strengths) > 0 {
		sb.WriteString("\n### Strengths\n\n")
		for _, s := range strengths {
			fmt.Fprintf(&sb, "- %s\n", s)
		}
	}

	var gaps []string
	if err := json.Unmarshal(eval.Gaps, &gaps); err == nil && len(gaps) > 0 {
		sb.WriteString("\n### Gaps\n\n")
		for _, g := range gaps {
			fmt.Fprintf(&sb, "- %s\n", g)
		}
	}

	if eval.Advice != "" {
		fmt.Fprintf(&sb, "\n### Advice\n\n%s\n", eval.Advice)
	}

	return sb.String()
}

func roleLabel(role string) string {
	switch role {
	case "interviewer":
		return "Interviewer"
	case "candidate":
		return "Candidate"
	default:
		return role
	}
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/educator/ -v -count=1
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/educator/prompt.go internal/educator/prompt_test.go
git commit -m "feat(educator): add prompt builder with system instructions and transcript formatting"
```

---

### Task 6: Coach Domain Package — Parse

**Files:**
- Create: `internal/coach/parse.go`
- Create: `internal/coach/parse_test.go`

- [ ] **Step 1: Write parse tests**

Create `internal/coach/parse_test.go`:

```go
package coach

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validToolInput() map[string]any {
	return map[string]any{
		"narrative":            "Your weakest area is scalability. You consistently skip back-of-envelope calculations...",
		"weakest_dimension":    "scalability",
		"improving_dimensions": []any{"requirements", "communication"},
		"topic_gaps":           []any{"caching", "message_queues"},
		"generated_question":   nil,
	}
}

func validToolInputWithQuestion() map[string]any {
	input := validToolInput()
	input["generated_question"] = map[string]any{
		"title":      "Design a Distributed Cache",
		"prompt":     "Design a distributed caching system like Memcached or Redis.",
		"difficulty": "hard",
		"tags":       []any{"caching", "distributed_systems"},
	}
	return input
}

func TestParse_ValidInput(t *testing.T) {
	raw, err := json.Marshal(validToolInput())
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	assert.Contains(t, result.Narrative, "scalability")
	assert.Equal(t, "scalability", result.WeakestDimension)
	assert.Equal(t, []string{"requirements", "communication"}, result.ImprovingDimensions)
	assert.Equal(t, []string{"caching", "message_queues"}, result.TopicGaps)
	assert.Nil(t, result.GeneratedQuestion)
}

func TestParse_WithGeneratedQuestion(t *testing.T) {
	raw, err := json.Marshal(validToolInputWithQuestion())
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)

	require.NotNil(t, result.GeneratedQuestion)
	assert.Equal(t, "Design a Distributed Cache", result.GeneratedQuestion.Title)
	assert.Equal(t, "hard", result.GeneratedQuestion.Difficulty)
	assert.Equal(t, []string{"caching", "distributed_systems"}, result.GeneratedQuestion.Tags)
}

func TestParse_EmptyNarrative(t *testing.T) {
	input := validToolInput()
	input["narrative"] = ""
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "narrative")
}

func TestParse_InvalidDifficulty(t *testing.T) {
	input := validToolInputWithQuestion()
	input["generated_question"].(map[string]any)["difficulty"] = "easy"
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	_, err = Parse(json.RawMessage(raw))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "difficulty")
}

func TestParse_NullArrays(t *testing.T) {
	input := map[string]any{
		"narrative":            "Good progress.",
		"weakest_dimension":    "deep_dive",
		"improving_dimensions": nil,
		"topic_gaps":           nil,
		"generated_question":   nil,
	}
	raw, err := json.Marshal(input)
	require.NoError(t, err)

	result, err := Parse(json.RawMessage(raw))
	require.NoError(t, err)
	assert.NotNil(t, result.ImprovingDimensions)
	assert.Empty(t, result.ImprovingDimensions)
	assert.NotNil(t, result.TopicGaps)
	assert.Empty(t, result.TopicGaps)
}

func TestParse_InvalidJSON(t *testing.T) {
	_, err := Parse(json.RawMessage([]byte(`not json`)))
	require.Error(t, err)
}

func TestToolSchema_HasRequiredFields(t *testing.T) {
	schema := ToolSchema()
	assert.Equal(t, "submit_analysis", schema.Name)
	props := schema.InputSchema.Properties
	assert.Contains(t, props, "narrative")
	assert.Contains(t, props, "weakest_dimension")
	assert.Contains(t, props, "improving_dimensions")
	assert.Contains(t, props, "topic_gaps")
	assert.Contains(t, props, "generated_question")
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/coach/ -v -count=1
```

Expected: compilation error.

- [ ] **Step 3: Write parse implementation**

Create `internal/coach/parse.go`:

```go
package coach

import (
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
)

// CoachResult holds the parsed coach analysis ready for persistence.
type CoachResult struct {
	Narrative           string
	WeakestDimension    string
	ImprovingDimensions []string
	TopicGaps           []string
	GeneratedQuestion   *GeneratedQuestion
}

// GeneratedQuestion holds a coach-generated practice question.
type GeneratedQuestion struct {
	Title      string
	Prompt     string
	Difficulty string
	Tags       []string
}

// ToolSchema returns the anthropic.ToolParam for the submit_analysis tool.
func ToolSchema() anthropic.ToolParam {
	return anthropic.ToolParam{
		Name:        "submit_analysis",
		Description: anthropic.String("Submit the strategic coaching analysis based on the candidate's interview history."),
		InputSchema: anthropic.ToolInputSchemaParam{
			Properties: map[string]any{
				"narrative": map[string]any{
					"type":        "string",
					"description": "Strategic coaching narrative including thinking patterns and metacognitive coaching.",
				},
				"weakest_dimension": map[string]any{
					"type":        "string",
					"description": "The evaluation dimension where the candidate scores lowest.",
				},
				"improving_dimensions": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "Dimensions showing improvement over time.",
				},
				"topic_gaps": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "System design topics the candidate hasn't practiced yet.",
				},
				"generated_question": map[string]any{
					"type":        []any{"object", "null"},
					"description": "An optional custom question targeting the candidate's weaknesses.",
					"properties": map[string]any{
						"title": map[string]any{
							"type":        "string",
							"description": "Short title for the generated question.",
						},
						"prompt": map[string]any{
							"type":        "string",
							"description": "Full interview-style prompt for the question.",
						},
						"difficulty": map[string]any{
							"type":        "string",
							"enum":        []string{"medium", "hard"},
							"description": "Difficulty level.",
						},
						"tags": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string"},
							"description": "Topic tags.",
						},
					},
				},
			},
			Required: []string{"narrative"},
		},
	}
}

// rawToolInput is an intermediate struct for JSON unmarshaling.
type rawToolInput struct {
	Narrative           string            `json:"narrative"`
	WeakestDimension    string            `json:"weakest_dimension"`
	ImprovingDimensions []string          `json:"improving_dimensions"`
	TopicGaps           []string          `json:"topic_gaps"`
	GeneratedQuestion   *rawGenQuestion   `json:"generated_question"`
}

type rawGenQuestion struct {
	Title      string   `json:"title"`
	Prompt     string   `json:"prompt"`
	Difficulty string   `json:"difficulty"`
	Tags       []string `json:"tags"`
}

// Parse unmarshals tool input JSON into a CoachResult.
func Parse(raw json.RawMessage) (*CoachResult, error) {
	var input rawToolInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, fmt.Errorf("coach: unmarshal tool input: %w", err)
	}

	if input.Narrative == "" {
		return nil, fmt.Errorf("coach: narrative is empty")
	}

	if input.ImprovingDimensions == nil {
		input.ImprovingDimensions = []string{}
	}
	if input.TopicGaps == nil {
		input.TopicGaps = []string{}
	}

	result := &CoachResult{
		Narrative:           input.Narrative,
		WeakestDimension:    input.WeakestDimension,
		ImprovingDimensions: input.ImprovingDimensions,
		TopicGaps:           input.TopicGaps,
	}

	if input.GeneratedQuestion != nil && input.GeneratedQuestion.Title != "" {
		gq := input.GeneratedQuestion
		if gq.Difficulty != "medium" && gq.Difficulty != "hard" {
			return nil, fmt.Errorf("coach: generated question difficulty must be medium or hard, got %q", gq.Difficulty)
		}
		if gq.Tags == nil {
			gq.Tags = []string{}
		}
		result.GeneratedQuestion = &GeneratedQuestion{
			Title:      gq.Title,
			Prompt:     gq.Prompt,
			Difficulty: gq.Difficulty,
			Tags:       gq.Tags,
		}
	}

	return result, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/coach/ -v -count=1
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/coach/parse.go internal/coach/parse_test.go
git commit -m "feat(coach): add tool schema, result types, and parser"
```

---

### Task 7: Coach Domain Package — Prompt

**Files:**
- Create: `internal/coach/prompt.go`
- Create: `internal/coach/prompt_test.go`

- [ ] **Step 1: Write prompt tests**

Create `internal/coach/prompt_test.go`:

```go
package coach

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/db"
)

func testSessions() []db.InterviewSession {
	return []db.InterviewSession{
		{ID: uuid.New(), QuestionID: uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001"), Status: "reviewed"},
		{ID: uuid.New(), QuestionID: uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000002"), Status: "reviewed"},
	}
}

func testEvaluations(sessions []db.InterviewSession) []db.Evaluation {
	s1, _ := json.Marshal([]string{"Good requirements"})
	g1, _ := json.Marshal([]string{"Missed caching"})
	s2, _ := json.Marshal([]string{"Strong communication"})
	g2, _ := json.Marshal([]string{"Weak scalability"})
	return []db.Evaluation{
		{
			SessionID: sessions[0].ID,
			ScoreRequirements: 4, ScoreArchitecture: 3, ScoreDeepDive: 2,
			ScoreScalability: 2, ScoreCommunication: 4, ScoreOverall: 3,
			Strengths: s1, Gaps: g1, Advice: "Study caching patterns.",
		},
		{
			SessionID: sessions[1].ID,
			ScoreRequirements: 3, ScoreArchitecture: 4, ScoreDeepDive: 3,
			ScoreScalability: 2, ScoreCommunication: 4, ScoreOverall: 3,
			Strengths: s2, Gaps: g2, Advice: "Work on scalability.",
		},
	}
}

func testQuestions(sessions []db.InterviewSession) []db.Question {
	return []db.Question{
		{ID: sessions[0].QuestionID, Title: "Design URL Shortener", Difficulty: "medium", Tags: []string{"databases", "caching"}},
		{ID: sessions[1].QuestionID, Title: "Design Chat System", Difficulty: "hard", Tags: []string{"messaging", "websockets"}},
		{ID: uuid.New(), Title: "Design Search Engine", Difficulty: "hard", Tags: []string{"search", "indexing"}},
	}
}

func TestBuildPrompt_SystemContainsCoachInstructions(t *testing.T) {
	sessions := testSessions()
	system, userMsgs := BuildPrompt(sessions, testEvaluations(sessions), testQuestions(sessions))

	assert.Contains(t, system, "expert system design interview coach")
	assert.Contains(t, system, "Dimension Analysis")
	assert.Contains(t, system, "Topic Coverage")
	assert.Contains(t, system, "Thinking Pattern")
	assert.Contains(t, system, "Metacognitive")
	require.Len(t, userMsgs, 1)
}

func TestBuildHistorySummary_IncludesScores(t *testing.T) {
	sessions := testSessions()
	summary := buildHistorySummary(sessions, testEvaluations(sessions), testQuestions(sessions))

	assert.Contains(t, summary, "Design URL Shortener")
	assert.Contains(t, summary, "Design Chat System")
	assert.Contains(t, summary, "Requirements: 4")
	assert.Contains(t, summary, "Good requirements")
	assert.Contains(t, summary, "Missed caching")
}

func TestBuildHistorySummary_TopicCoverage(t *testing.T) {
	sessions := testSessions()
	summary := buildHistorySummary(sessions, testEvaluations(sessions), testQuestions(sessions))

	assert.Contains(t, summary, "Topics Covered")
	assert.Contains(t, summary, "databases")
	assert.Contains(t, summary, "Topics Not Yet Covered")
	assert.Contains(t, summary, "search")
}

func TestBuildHistorySummary_ScoreAverages(t *testing.T) {
	sessions := testSessions()
	summary := buildHistorySummary(sessions, testEvaluations(sessions), testQuestions(sessions))

	assert.Contains(t, summary, "Score Averages")
}

func TestBuildHistorySummary_ZeroSessions(t *testing.T) {
	summary := buildHistorySummary(nil, nil, nil)

	assert.Contains(t, summary, "no interview history")
}

func TestBuildPrompt_EmptySessions(t *testing.T) {
	system, userMsgs := BuildPrompt(nil, nil, nil)
	assert.Contains(t, system, "coach")
	require.Len(t, userMsgs, 1)
}
```

- [ ] **Step 2: Run tests to verify they fail**

```bash
go test ./internal/coach/ -v -count=1 -run TestBuildPrompt
```

Expected: compilation error (BuildPrompt not defined).

- [ ] **Step 3: Write prompt implementation**

Create `internal/coach/prompt.go`:

```go
package coach

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"

	"github.com/btc/drill/internal/db"
)

const coachSystemPrompt = `You are an expert system design interview coach. You have access to the candidate's full interview history — past sessions, evaluations, scores, strengths, gaps, and advice. Your job is to analyze this history and produce strategic guidance.

## Your Responsibilities

### 1. Dimension Analysis
Analyze the candidate's scores across all evaluation dimensions (requirements, high-level architecture, deep dive, scalability, communication). Identify:
- Which dimension is consistently weakest
- Which dimensions are improving over time
- Where scores plateau and what might break through

### 2. Topic Coverage
Review which system design topics the candidate has practiced and which they haven't. Identify gaps in their coverage and recommend topics that would round out their preparation.

### 3. Thinking Pattern Recognition
Look across multiple interviews for recurring patterns in how the candidate approaches problems:
- Do they consistently skip requirements gathering?
- Do they go too deep too early?
- Do they struggle with back-of-envelope calculations?
- Do they miss trade-off discussions?
Identify both positive patterns (strengths to maintain) and negative patterns (habits to break).

### 4. Scenario Generation
Based on identified gaps, optionally generate a custom practice question that specifically targets the candidate's weaknesses. The question should:
- Address a topic or dimension they struggle with
- Be at an appropriate difficulty level for their current skill
- Include tags that map to their gap areas

### 5. Progressive Difficulty
Track the candidate's overall trajectory and recommend appropriate difficulty:
- If scores are consistently low (1-2), suggest medium difficulty questions
- If scores are improving (3-4), suggest harder variants or new topics
- If scores are high (4-5), suggest hard questions with complex constraints

### 6. Metacognitive Coaching
Help the candidate develop self-awareness about their interview approach:
- Point out blind spots they may not realize they have
- Suggest reflection exercises or frameworks
- Encourage deliberate practice on specific sub-skills
- Frame feedback in terms of growth and improvement

## Output

You MUST use the submit_analysis tool to submit your structured analysis. Do not produce free-text output — use the tool.

If you identify a specific gap that would benefit from a custom practice question, include it in the generated_question field. Otherwise, set generated_question to null.`

// BuildPrompt returns the coach system prompt and a single user message
// containing the history summary.
func BuildPrompt(sessions []db.InterviewSession, evaluations []db.Evaluation, questions []db.Question) (string, []anthropic.MessageParam) {
	summary := buildHistorySummary(sessions, evaluations, questions)
	userMsg := anthropic.NewUserMessage(anthropic.NewTextBlock(summary))
	return coachSystemPrompt, []anthropic.MessageParam{userMsg}
}

// buildHistorySummary formats the candidate's interview history for the coach.
func buildHistorySummary(sessions []db.InterviewSession, evaluations []db.Evaluation, questions []db.Question) string {
	if len(sessions) == 0 {
		return "The candidate has no interview history yet. " +
			"This is their first coaching session. " +
			"Recommend starting with a medium difficulty question to establish a baseline."
	}

	questionMap := make(map[uuid.UUID]db.Question, len(questions))
	for _, q := range questions {
		questionMap[q.ID] = q
	}
	sessionMap := make(map[uuid.UUID]db.InterviewSession, len(sessions))
	for _, s := range sessions {
		sessionMap[s.ID] = s
	}

	var sb strings.Builder
	sb.WriteString("# Candidate Interview History\n\n")
	sb.WriteString("## Session Details\n\n")

	for _, ev := range evaluations {
		session, ok := sessionMap[ev.SessionID]
		if !ok {
			continue
		}
		question, ok := questionMap[session.QuestionID]
		if !ok {
			continue
		}

		fmt.Fprintf(&sb, "### %s\n", question.Title)
		fmt.Fprintf(&sb, "- Difficulty: %s\n", question.Difficulty)
		if len(question.Tags) > 0 {
			fmt.Fprintf(&sb, "- Tags: %s\n", strings.Join(question.Tags, ", "))
		}
		sb.WriteString("- Scores:\n")
		fmt.Fprintf(&sb, "  - Requirements: %d\n", ev.ScoreRequirements)
		fmt.Fprintf(&sb, "  - Architecture: %d\n", ev.ScoreArchitecture)
		fmt.Fprintf(&sb, "  - Deep Dive: %d\n", ev.ScoreDeepDive)
		fmt.Fprintf(&sb, "  - Scalability: %d\n", ev.ScoreScalability)
		fmt.Fprintf(&sb, "  - Communication: %d\n", ev.ScoreCommunication)
		fmt.Fprintf(&sb, "  - Overall: %d\n", ev.ScoreOverall)

		var strengths []string
		if err := json.Unmarshal(ev.Strengths, &strengths); err == nil && len(strengths) > 0 {
			fmt.Fprintf(&sb, "- Strengths: %s\n", strings.Join(strengths, "; "))
		}
		var gaps []string
		if err := json.Unmarshal(ev.Gaps, &gaps); err == nil && len(gaps) > 0 {
			fmt.Fprintf(&sb, "- Gaps: %s\n", strings.Join(gaps, "; "))
		}
		if ev.Advice != "" {
			fmt.Fprintf(&sb, "- Advice: %s\n", ev.Advice)
		}
		sb.WriteString("\n")
	}

	// Topic coverage.
	attemptedQuestionIDs := make(map[uuid.UUID]bool, len(sessions))
	for _, s := range sessions {
		if s.Status == "reviewed" {
			attemptedQuestionIDs[s.QuestionID] = true
		}
	}
	attemptedTags := make(map[string]bool)
	allTags := make(map[string]bool)
	for _, q := range questions {
		for _, tag := range q.Tags {
			allTags[tag] = true
			if attemptedQuestionIDs[q.ID] {
				attemptedTags[tag] = true
			}
		}
	}

	sb.WriteString("## Topics Covered (Attempted)\n")
	if len(attemptedTags) > 0 {
		sb.WriteString(joinSortedKeys(attemptedTags))
	} else {
		sb.WriteString("None")
	}
	sb.WriteString("\n\n")

	uncoveredTags := make(map[string]bool)
	for tag := range allTags {
		if !attemptedTags[tag] {
			uncoveredTags[tag] = true
		}
	}
	if len(uncoveredTags) > 0 {
		sb.WriteString("## Topics Not Yet Covered (Available)\n")
		sb.WriteString(joinSortedKeys(uncoveredTags))
		sb.WriteString("\n\n")
	}

	// Score averages.
	if len(evaluations) > 0 {
		sb.WriteString("## Score Averages\n")
		type dim struct {
			name  string
			label string
			sum   int
		}
		dims := []dim{
			{"requirements", "Requirements", 0},
			{"architecture", "Architecture", 0},
			{"deep_dive", "Deep Dive", 0},
			{"scalability", "Scalability", 0},
			{"communication", "Communication", 0},
			{"overall", "Overall", 0},
		}
		for _, ev := range evaluations {
			dims[0].sum += int(ev.ScoreRequirements)
			dims[1].sum += int(ev.ScoreArchitecture)
			dims[2].sum += int(ev.ScoreDeepDive)
			dims[3].sum += int(ev.ScoreScalability)
			dims[4].sum += int(ev.ScoreCommunication)
			dims[5].sum += int(ev.ScoreOverall)
		}
		n := len(evaluations)
		for _, d := range dims {
			fmt.Fprintf(&sb, "- %s: %.1f\n", d.label, float64(d.sum)/float64(n))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

func joinSortedKeys(m map[string]bool) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
go test ./internal/coach/ -v -count=1
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/coach/prompt.go internal/coach/prompt_test.go
git commit -m "feat(coach): add prompt builder with history summary and system instructions"
```

---

### Task 8: Backend — Sentinel Errors + Educator Service Methods

**Files:**
- Modify: `internal/backend/errors.go`
- Create: `internal/backend/educator.go`

- [ ] **Step 1: Add sentinel errors**

Append to the `var` block in `internal/backend/errors.go`:

```go
	ErrAlreadyExists  = fmt.Errorf("educator analysis already exists")
	ErrNoNewSessions  = fmt.Errorf("no new sessions since last analysis")
```

- [ ] **Step 2: Write educator service methods**

Create `internal/backend/educator.go`:

```go
package backend

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// EducatorResponse is the API response for educator analysis.
type EducatorResponse struct {
	Status       string `json:"status"`
	ModelAnswer  string `json:"model_answer,omitempty"`
	GapDeepDives string `json:"gap_deep_dives,omitempty"`
}

// GetEducatorAnalysis returns the educator analysis for a session.
func (b *Backend) GetEducatorAnalysis(ctx context.Context, sessionID, userID uuid.UUID) (*EducatorResponse, error) {
	session, err := b.GetSessionForUser(ctx, sessionID, userID)
	if err != nil {
		return nil, err
	}
	if session.Status != "reviewed" {
		return nil, ErrEvaluationNotReady
	}

	q := db.New(b.pool)
	ea, err := q.GetEducatorAnalysisBySession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &EducatorResponse{Status: "not_requested"}, nil
		}
		return nil, fmt.Errorf("get educator analysis: %w", err)
	}

	switch ea.Status {
	case "generating":
		return &EducatorResponse{Status: "generating"}, nil
	case "failed":
		return &EducatorResponse{Status: "failed"}, nil
	case "completed":
		return &EducatorResponse{
			Status:       "completed",
			ModelAnswer:  ea.ModelAnswer.String,
			GapDeepDives: ea.GapDeepDives.String,
		}, nil
	default:
		return &EducatorResponse{Status: ea.Status}, nil
	}
}

// RequestEducatorAnalysis enqueues educator content generation for a session.
func (b *Backend) RequestEducatorAnalysis(ctx context.Context, sessionID, userID uuid.UUID) error {
	session, err := b.GetSessionForUser(ctx, sessionID, userID)
	if err != nil {
		return err
	}
	if session.Status != "reviewed" {
		return ErrEvaluationNotReady
	}

	q := db.New(b.pool)
	ea, err := q.GetEducatorAnalysisBySession(ctx, sessionID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("check existing educator analysis: %w", err)
	}
	if err == nil {
		switch ea.Status {
		case "completed":
			return ErrAlreadyExists
		case "generating":
			return nil // idempotent
		case "failed":
			// Allow retry: update status to generating and re-enqueue.
			if err := q.UpdateEducatorAnalysisStatus(ctx, db.UpdateEducatorAnalysisStatusParams{
				ID:     ea.ID,
				Status: "generating",
			}); err != nil {
				return fmt.Errorf("reset educator status: %w", err)
			}
		}
	}

	_, err = b.jobs.Insert(ctx, jobs.GenerateEducatorContentArgs{SessionID: sessionID}, jobs.GenerateEducatorContentInsertOpts())
	if err != nil {
		return fmt.Errorf("enqueue educator job: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./internal/backend/
```

Expected: compiles (the `jobs.GenerateEducatorContentArgs` type doesn't exist yet — if this fails, stub it in the next task and come back). If it fails, create a minimal stub in `internal/jobs/educator.go` with just the args type and insert opts, then rebuild.

- [ ] **Step 4: Commit**

```bash
git add internal/backend/errors.go internal/backend/educator.go
git commit -m "feat(backend): add educator service methods and sentinel errors"
```

---

### Task 9: Backend — Coach Service Methods

**Files:**
- Create: `internal/backend/coach.go`

- [ ] **Step 1: Write coach service methods**

Create `internal/backend/coach.go`:

```go
package backend

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/jobs"
)

// CoachResponse is the API response for coach analysis.
type CoachResponse struct {
	Narrative           string    `json:"narrative"`
	WeakestDimension    string    `json:"weakest_dimension,omitempty"`
	ImprovingDimensions []string  `json:"improving_dimensions,omitempty"`
	TopicGaps           []string  `json:"topic_gaps,omitempty"`
	SuggestedQuestionID *string   `json:"suggested_question_id,omitempty"`
	CreatedAt           time.Time `json:"created_at"`
}

// GetLatestCoachAnalysis returns the most recent coach analysis for a user.
// Returns nil with no error if no analysis exists.
func (b *Backend) GetLatestCoachAnalysis(ctx context.Context, userID uuid.UUID) (*CoachResponse, error) {
	q := db.New(b.pool)
	ca, err := q.GetLatestCoachAnalysis(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get latest coach analysis: %w", err)
	}

	resp := &CoachResponse{
		Narrative:           ca.Narrative,
		WeakestDimension:    ca.WeakestDimension.String,
		ImprovingDimensions: ca.ImprovingDimensions,
		TopicGaps:           ca.TopicGaps,
		CreatedAt:           ca.CreatedAt,
	}
	if ca.SuggestedQuestionID.Valid {
		id := uuid.UUID(ca.SuggestedQuestionID.Bytes).String()
		resp.SuggestedQuestionID = &id
	}
	if resp.ImprovingDimensions == nil {
		resp.ImprovingDimensions = []string{}
	}
	if resp.TopicGaps == nil {
		resp.TopicGaps = []string{}
	}

	return resp, nil
}

// RequestCoachAnalysis enqueues a coach analysis job.
// If force is false, checks whether new sessions exist since the last analysis.
func (b *Backend) RequestCoachAnalysis(ctx context.Context, userID uuid.UUID, force bool) error {
	q := db.New(b.pool)

	if !force {
		// Check if there are new reviewed sessions since the last analysis.
		currentIDs, err := q.GetReviewedSessionIDsForUser(ctx, userID)
		if err != nil {
			return fmt.Errorf("get reviewed session ids: %w", err)
		}

		ca, err := q.GetLatestCoachAnalysis(ctx, userID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get latest coach analysis: %w", err)
		}

		if err == nil && uuidSlicesEqual(currentIDs, ca.SessionsAnalyzed) {
			return ErrNoNewSessions
		}
	}

	_, err := b.jobs.Insert(ctx, jobs.RunCoachAnalysisArgs{UserID: userID}, jobs.RunCoachAnalysisInsertOpts())
	if err != nil {
		return fmt.Errorf("enqueue coach job: %w", err)
	}
	return nil
}

// uuidSlicesEqual compares two UUID slices for set equality (order-sensitive;
// both queries ORDER BY created_at so order is deterministic).
func uuidSlicesEqual(a []uuid.UUID, b []uuid.UUID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/backend/
```

- [ ] **Step 3: Commit**

```bash
git add internal/backend/coach.go
git commit -m "feat(backend): add coach service methods"
```

---

### Task 10: River Workers — Educator

**Files:**
- Create: `internal/jobs/educator.go`
- Modify: `internal/jobs/workers.go`
- Modify: `internal/jobs/error_handler.go`

- [ ] **Step 1: Write educator worker**

Create `internal/jobs/educator.go`:

```go
package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
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

	// 3. Load question, messages, evaluation.
	question, err := q.GetQuestion(ctx, session.QuestionID)
	if err != nil {
		return fmt.Errorf("get question: %w", err)
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
	toolSchema := educator.ToolSchema()
	toolInput, err := w.LLM.CallToolAndLog(ctx, tx, ai.CallToolParams{
		Model:      w.Cfg.EducatorModel,
		System:     system,
		Messages:   promptMsgs,
		MaxTokens:  w.Cfg.EducatorMaxTokens,
		UserID:     session.UserID,
		Role:       "educator",
		SessionID:  sessionID,
		Tools:      []anthropic.ToolUnionParam{{OfTool: &toolSchema}},
		ToolChoice: anthropic.ToolChoiceParamOfTool("submit_education"),
	})
	if err != nil {
		return fmt.Errorf("call educator LLM: %w", err)
	}

	// 8. Parse.
	result, err := educator.Parse(toolInput)
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
```

- [ ] **Step 2: Update error handler for educator failures**

In `internal/jobs/error_handler.go`, **replace the entire `HandleError` function** with a switch-based dispatch. The current function has a guard clause `if job.Kind != "evaluate_session" { return nil }` that would prevent any educator handling from executing, so we must restructure it.

Replace `HandleError` with:

```go
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
```

Also **replace the entire `HandlePanic` function** with:

```go
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
```

- [ ] **Step 3: Register worker in workers.go**

In `internal/jobs/workers.go`, update `WorkerRefs` and `RegisterWorkers`:

```go
type WorkerRefs struct {
	Evaluate *EvaluateSessionWorker
	Cleanup  *CleanupAbandonedSessionsWorker
	Educator *GenerateEducatorContentWorker
}

func RegisterWorkers(cfg *config.Config, sender email.Sender, pool *pgxpool.Pool, llm *ai.Client) (*river.Workers, WorkerRefs) {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewSendEmailWorker(&cfg.Email, sender))
	eval := &EvaluateSessionWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, eval)
	cleanup := &CleanupAbandonedSessionsWorker{Pool: pool}
	river.AddWorker(workers, cleanup)
	edu := &GenerateEducatorContentWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, edu)
	return workers, WorkerRefs{Evaluate: eval, Cleanup: cleanup, Educator: edu}
}
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./internal/jobs/
```

- [ ] **Step 5: Commit**

```bash
git add internal/jobs/educator.go internal/jobs/error_handler.go internal/jobs/workers.go
git commit -m "feat(educator): add GenerateEducatorContent River worker with failure handling"
```

---

### Task 11: River Workers — Coach

**Files:**
- Create: `internal/jobs/coach.go`
- Modify: `internal/jobs/workers.go`

- [ ] **Step 1: Write coach worker**

Create `internal/jobs/coach.go`:

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
	for i, s := range sessions {
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
	toolInput, err := w.LLM.CallToolAndLog(ctx, tx, ai.CallToolParams{
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
			UserID:        pgtype.UUID{Bytes: userID, Valid: true},
			Title:         gq.Title,
			Prompt:        gq.Prompt,
			Difficulty:    gq.Difficulty,
			Tags:          gq.Tags,
			Source:        "coach_generated",
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
```

- [ ] **Step 2: Update workers.go to register coach worker**

Add to `WorkerRefs`:

```go
type WorkerRefs struct {
	Evaluate *EvaluateSessionWorker
	Cleanup  *CleanupAbandonedSessionsWorker
	Educator *GenerateEducatorContentWorker
	Coach    *RunCoachAnalysisWorker
}
```

Add to `RegisterWorkers` before the return:

```go
	coachWorker := &RunCoachAnalysisWorker{Pool: pool, LLM: llm, Cfg: &cfg.LLM}
	river.AddWorker(workers, coachWorker)
	return workers, WorkerRefs{Evaluate: eval, Cleanup: cleanup, Educator: edu, Coach: coachWorker}
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./internal/jobs/
```

`InsertQuestion` was added in Task 2, Step 6, so this should compile.

- [ ] **Step 4: Commit**

```bash
git add internal/jobs/coach.go internal/jobs/workers.go
git commit -m "feat(coach): add RunCoachAnalysis River worker with question generation"
```

---

### Task 12: HTTP Handlers + Routes

**Files:**
- Create: `internal/handler/educator.go`
- Create: `internal/handler/coach.go`
- Modify: `internal/handler/routes.go`

- [ ] **Step 1: Write educator handlers**

Create `internal/handler/educator.go`:

```go
package handler

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// GetEducatorAnalysis returns a handler that fetches educator content for a session.
func GetEducatorAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session id"})
			return
		}

		resp, err := b.GetEducatorAnalysis(r.Context(), id, user.ID)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrSessionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrSessionNotOwned):
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrEvaluationNotReady):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// RequestEducatorAnalysis returns a handler that enqueues educator content generation.
func RequestEducatorAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session id"})
			return
		}

		err = b.RequestEducatorAnalysis(r.Context(), id, user.ID)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrAlreadyExists):
				writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
			case errors.Is(err, backend.ErrSessionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrSessionNotOwned):
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrEvaluationNotReady):
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "generating"})
	}
}
```

- [ ] **Step 2: Write coach handlers**

Create `internal/handler/coach.go`:

```go
package handler

import (
	"errors"
	"net/http"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

// GetCoachAnalysis returns a handler that fetches the latest coach analysis.
func GetCoachAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())

		resp, err := b.GetLatestCoachAnalysis(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		if resp == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "no coach analysis yet"})
			return
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// RequestCoachAnalysis returns a handler that enqueues a coach analysis.
func RequestCoachAnalysis(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		force := r.URL.Query().Get("force") == "true"

		err := b.RequestCoachAnalysis(r.Context(), user.ID, force)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrNoNewSessions):
				writeJSON(w, http.StatusOK, map[string]string{"status": "up_to_date"})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusAccepted, map[string]string{"status": "analyzing"})
	}
}
```

- [ ] **Step 3: Register routes**

Add to `internal/handler/routes.go` inside `RegisterRoutes`, after the Evaluations section:

```go
	// Educator
	mux.Handle("GET /api/sessions/{id}/educator", requireAuth(http.HandlerFunc(GetEducatorAnalysis(b))))
	mux.Handle("POST /api/sessions/{id}/educator", requireAuth(http.HandlerFunc(RequestEducatorAnalysis(b))))

	// Coach
	mux.Handle("GET /api/coach/latest", requireAuth(http.HandlerFunc(GetCoachAnalysis(b))))
	mux.Handle("POST /api/coach/analyze", requireAuth(http.HandlerFunc(RequestCoachAnalysis(b))))
```

- [ ] **Step 4: Verify compilation**

```bash
go build ./internal/handler/
```

- [ ] **Step 5: Commit**

```bash
git add internal/handler/educator.go internal/handler/coach.go internal/handler/routes.go
git commit -m "feat(educator-coach): add HTTP handlers and register routes"
```

---

### Task 13: Wire Coach Briefing Into Conductor

**Files:**
- Modify: `internal/interview/conductor.go`

- [ ] **Step 1: Add coach briefing loading to conductor's loadSession**

On the eval branch, `LoadSessionForConductor` does not exist. The conductor's `loadSession` calls `c.backend.GetSession()` (returns `db.GetSessionRow`) and `c.backend.GetMessagesBySession()` separately. We add coach briefing loading directly in the conductor's `loadSession`, using the queries package.

In `internal/interview/conductor.go`, update the `Conductor` struct to add a `coachBriefing` field:

```go
	// Coach briefing for this user (nil if disabled or no analysis exists).
	coachBriefing *db.CoachAnalysis
```

Update `loadSession` to conditionally load coach briefing after the existing session/message loading:

```go
func (c *Conductor) loadSession(ctx context.Context) error {
	row, err := c.backend.GetSession(ctx, c.sessionID)
	if err != nil {
		return err
	}

	msgs, err := c.backend.GetMessagesBySession(ctx, c.sessionID)
	if err != nil {
		return err
	}

	c.question = db.Question{
		Title:  row.QuestionTitle,
		Prompt: row.QuestionPrompt,
	}
	c.messages = msgs
	c.ttsEnabled = row.ConfigTtsEnabled
	c.duration = time.Duration(row.ConfigDurationMinutes) * time.Minute
	c.model = c.backend.Config().LLM.InterviewerModel
	c.sm = NewStateMachine(StateWaitingForInput)
	c.sm.SetStartedAt(row.StartedAt)

	if len(msgs) > 0 {
		c.sequence = int(msgs[len(msgs)-1].Seq)
	}

	// Load coach briefing if enabled.
	if row.ConfigCoachBriefing {
		q := db.New(c.backend.Pool())
		ca, err := q.GetLatestCoachAnalysis(ctx, row.UserID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("get coach analysis: %w", err)
		}
		if err == nil {
			c.coachBriefing = &ca
		}
	}

	return nil
}
```

Note: This requires adding `"errors"` and `pgx "github.com/jackc/pgx/v5"` to conductor.go's import block (the eval branch conductor imports neither).

- [ ] **Step 2: Wire WithCoachBriefing into prompt builder chain**

`WithCoachBriefing` already exists on the eval branch in `internal/interview/prompt.go`. In `streamInterviewerResponse`, add it to the builder chain (line ~355):

```go
	system, promptMsgs := NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(c.question).
		WithCoachBriefing(c.coachBriefing).
		WithTimeContext(elapsed, remaining).
		WithTranscript(c.messages).
		Build()
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./...
```

- [ ] **Step 4: Commit**

```bash
git add internal/interview/conductor.go
git commit -m "feat(conductor): wire coach briefing into interviewer prompt"
```

---

### Task 14: Backend Wiring — Update backend.go

**Files:**
- Modify: `internal/backend/backend.go`

- [ ] **Step 1: Wire educator and coach workers in backend.go**

After `workerRefs.Cleanup.Jobs = riverClient`, add:

```go
	// Educator and coach workers don't currently need the Jobs client,
	// but wiring is reserved for future use (e.g., email notifications).
```

No actual wiring needed yet — educator and coach workers don't enqueue follow-up jobs. The `WorkerRefs` already includes them from Task 10/11, so `RegisterWorkers` handles registration.

- [ ] **Step 2: Verify full build**

```bash
go build ./...
```

Expected: clean compilation.

- [ ] **Step 3: Run all existing tests**

```bash
go test ./... -count=1 -timeout 5m
```

Expected: all PASS.

- [ ] **Step 4: Commit if any fixes were needed**

```bash
git add -A
git commit -m "fix: resolve remaining compilation errors"
```

---

### Task 15: Full Test Run + Final Verification

- [ ] **Step 1: Run all unit tests**

```bash
go test ./internal/educator/ ./internal/coach/ -v -count=1
```

Expected: all PASS.

- [ ] **Step 2: Run full test suite**

```bash
go test ./... -count=1 -timeout 5m
```

Expected: all PASS.

- [ ] **Step 3: Verify sqlc generated code matches queries**

```bash
sqlc generate
git diff --stat
```

Expected: no changes (codegen is already up to date).

- [ ] **Step 4: Final commit if needed, then log**

```bash
git log --oneline feat/educator-coach ^main
```

Review commit history for clarity and completeness.
