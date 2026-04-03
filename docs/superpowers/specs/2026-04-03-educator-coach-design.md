# Educator + Coach — Design Spec

**Date**: 2026-04-03
**Status**: Approved
**Phase**: 8 (per build order in system design spec)
**Depends on**: Phase 6 (Conductor, merged), Phase 7 (Evaluation, in progress)
**Branch strategy**: Branch from eval HEAD (`fbf6a46`), rebase as eval progresses

---

## 1. Scope

Backend only. Two new AI roles (educator, coach) implemented as River job workers with supporting domain packages, backend methods, HTTP handlers, and SQL queries. No frontend in this phase.

**What this phase builds:**
- `internal/educator/` — prompt builder, tool schema, response parser
- `internal/coach/` — prompt builder, tool schema, response parser
- `internal/jobs/educator.go` — `GenerateEducatorContent` River worker
- `internal/jobs/coach.go` — `RunCoachAnalysis` River worker
- `internal/backend/educator.go` — service methods
- `internal/backend/coach.go` — service methods
- `internal/handler/educator.go` — HTTP handlers
- `internal/handler/coach.go` — HTTP handlers
- `sql/queries/educator_analyses.sql` — DB queries
- `sql/queries/coach_analyses.sql` — DB queries
- `WithCoachBriefing()` on the interviewer PromptBuilder
- Conductor query update for coach briefing data

**What this phase does NOT build:**
- Frontend (Learn page, Home coach card)
- Billing/entitlement checks (Phase 9)
- Educator email notification (v2)

---

## 2. Model Selection

| Role | Model | Rationale |
|------|-------|-----------|
| Educator | Opus | Deep technical content requires highest capability |
| Coach | Sonnet | Strategic analysis, sufficient for pattern recognition and coaching |

Both use `CallToolAndLog` (blocking, forced tool_choice). Model names configured via `config.LLM.EducatorModel` and `config.LLM.CoachModel`.

**New config fields** added to `config.LLM`:
- `EducatorMaxTokens int64` — `env:"EDUCATOR_MAX_TOKENS,default=8192"` (educator produces long-form content; v0 used 8000)
- `CoachMaxTokens int64` — `env:"COACH_MAX_TOKENS,default=4096"`

---

## 3. SQL Queries

### `sql/queries/educator_analyses.sql`

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

### `sql/queries/coach_analyses.sql`

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

The educator worker also uses `GetEvaluationBySession` from eval's query file to load evaluation data for prompt construction.

### Additional queries needed by coach worker

Added to `sql/queries/evaluations.sql`:
```sql
-- name: GetEvaluationsBySessionIDs :many
SELECT id, session_id, score_requirements, score_architecture,
       score_deep_dive, score_scalability, score_communication,
       score_overall, strengths, gaps, advice, created_at
FROM evaluations WHERE session_id = ANY($1::uuid[]);
```

Added to `sql/queries/questions.sql`:
```sql
-- name: GetQuestionsForUser :many
SELECT * FROM questions
WHERE user_id IS NULL OR user_id = $1
ORDER BY created_at;
```

### Session queries for coach (added to `sql/queries/sessions.sql`)

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

`GetReviewedSessionsForUser` returns full rows for the coach worker (needed for `BuildPrompt`). `GetReviewedSessionIDsForUser` returns only IDs for the backend debounce comparison in `RequestCoachAnalysis`.

### Coach briefing query (added to `sql/queries/sessions.sql`)

```sql
-- name: GetSessionWithCoachBriefing :one
SELECT s.*,
       ca.narrative           AS coach_narrative,
       ca.weakest_dimension   AS coach_weakest_dimension,
       ca.improving_dimensions AS coach_improving_dimensions,
       ca.topic_gaps          AS coach_topic_gaps
FROM interview_sessions s
LEFT JOIN LATERAL (
    SELECT * FROM coach_analyses
    WHERE user_id = s.user_id
    ORDER BY created_at DESC LIMIT 1
) ca ON s.config_coach_briefing = true
WHERE s.id = $1;
```

This query is an optional optimization for future adoption. The initial implementation uses a simpler approach: the conductor calls `GetLatestCoachAnalysis` conditionally when `config_coach_briefing` is true (see Section 8). The LATERAL join query is preserved here as a potential consolidation for when sqlc support improves.

---

## 4. Domain Packages

### `internal/educator/`

**`prompt.go`**:
- `BuildPrompt(question db.Question, messages []db.Message, eval db.Evaluation) (string, []anthropic.MessageParam)`
- System prompt ported from v0: teaching-focused instructions for model answer + gap deep-dives
- User message contains formatted transcript + evaluation summary (scores, strengths, gaps, advice)
- Internal helpers: `buildTranscript()`, `buildEvaluationSummary()` (strengths/gaps unmarshaled from JSONB)

**`parse.go`**:
- `ToolSchema() anthropic.ToolParam` — `submit_education` tool, two required string fields: `model_answer`, `gap_deep_dives`
- `EducatorResult` struct: `ModelAnswer string`, `GapDeepDives string`
- `Parse(raw json.RawMessage) (*EducatorResult, error)` — unmarshal, validate both fields non-empty

### `internal/coach/`

**`prompt.go`**:
- `BuildPrompt(sessions []db.InterviewSession, evaluations []db.Evaluation, questions []db.Question) (string, []anthropic.MessageParam)`
- System prompt ported from v0: dimension analysis, topic coverage, thinking patterns, scenario generation, progressive difficulty, metacognitive coaching
- User message is the history summary built from sessions + evaluations + questions
- Internal helper: `buildHistorySummary()` — per-session score breakdowns, topic coverage (attempted vs. available tags), score averages

**`parse.go`**:
- `ToolSchema() anthropic.ToolParam` — `submit_analysis` tool. Fields map 1:1 to DB columns:
  - `narrative` (required string)
  - `weakest_dimension` (string)
  - `improving_dimensions` (string array)
  - `topic_gaps` (string array)
  - `generated_question` (optional object: title, prompt, difficulty, tags)
- `CoachResult` struct: fields match DB columns + `GeneratedQuestion *GeneratedQuestion`
- `GeneratedQuestion` struct: `Title`, `Prompt`, `Difficulty`, `Tags []string`
- `Parse(raw json.RawMessage) (*CoachResult, error)` — unmarshal, validate narrative non-empty, validate difficulty is `medium` or `hard` if question present

---

## 5. River Workers

### `internal/jobs/educator.go` — `GenerateEducatorContent`

| Property | Value |
|----------|-------|
| Kind | `generate_educator_content` |
| Queue | `ai` |
| Timeout | 15 min |
| Max attempts | 5 |
| Uniqueness | by `session_id` |

**Work() flow:**
1. Idempotency check: load `educator_analyses` row for this session
   - If exists with status `completed` → skip (already done)
   - If exists with status `failed` → previous attempt exhausted retries, skip
   - If exists with status `generating` → previous attempt failed, proceed to step 5
   - If no row → proceed to step 2
2. Load session, verify status is `reviewed`
3. Load question, messages, evaluation
4. Insert `educator_analyses` row with status `generating` (skip if row existed from step 1)
5. Unmarshal evaluation strengths/gaps from JSONB, build prompt via `educator.BuildPrompt()`
6. Begin transaction
7. `CallToolAndLog()` with Opus, `submit_education` tool. Params: `Model: w.Cfg.EducatorModel`, `MaxTokens: w.Cfg.EducatorMaxTokens`, `UserID: session.UserID`, `Role: "educator"`, `SessionID: sessionID`
8. Parse via `educator.Parse()`
9. Update `educator_analyses` with content + status `completed`
10. Commit

On final failure (River `MaxAttempts` exhausted), update `educator_analyses` status to `failed`. Implement via River's `ErrorHandler` interface or by checking `job.Attempt == job.MaxAttempts` at the top of `Work()` before returning an error.

**Schema migration**: The `educator_analyses.status` CHECK constraint needs updating from `('generating', 'completed')` to `('generating', 'completed', 'failed')`. Add as a new migration (002 or later).

### `internal/jobs/coach.go` — `RunCoachAnalysis`

| Property | Value |
|----------|-------|
| Kind | `run_coach_analysis` |
| Queue | `ai` |
| Timeout | 15 min |
| Max attempts | 3 |
| Uniqueness | by `user_id` |

**Work() flow:**
1. Load all reviewed, non-archived sessions for user
2. If zero sessions, return nil
3. Load evaluations for those sessions
4. Load questions for topic/tag context
5. Build prompt via `coach.BuildPrompt()`
6. Begin transaction
7. `CallToolAndLog()` with Sonnet, `submit_analysis` tool. Params: `Model: w.Cfg.CoachModel`, `MaxTokens: w.Cfg.CoachMaxTokens`, `UserID: job.Args.UserID`, `Role: "coach"`, `SessionID: uuid.Nil` (coach is user-level, not session-level; `llm_calls.session_id` will be NULL)
8. Parse via `coach.Parse()`
9. If `GeneratedQuestion` present: insert into `questions` (source `coach_generated`, user_id set, coach_rationale from narrative excerpt), capture UUID
10. Insert `coach_analyses` row with all columns including `suggested_question_id` and `sessions_analyzed`
11. Commit

No email notification for either worker.

Both workers are registered in `internal/jobs/workers.go` via `RegisterWorkers()`, following the eval pattern. `WorkerRefs` is extended with `Educator` and `Coach` fields for post-creation wiring of the `Jobs` field (needed if either worker needs to enqueue follow-up jobs in the future).

---

## 6. Backend Methods

### `internal/backend/educator.go`

**Response type:**
```go
type EducatorResponse struct {
    Status       string `json:"status"`
    ModelAnswer  string `json:"model_answer,omitempty"`
    GapDeepDives string `json:"gap_deep_dives,omitempty"`
}
```

**`GetEducatorAnalysis(ctx, sessionID, userID) (*EducatorResponse, error)`**:
1. Verify ownership via `GetSessionForUser()`
2. If session status is not `reviewed` → return `ErrEvaluationNotReady`
3. Load `educator_analyses` row by session ID
4. No row → status `"not_requested"`
5. Status `generating` → status `"generating"`
6. Status `failed` → status `"failed"`
7. Status `completed` → return full content
8. Free-tier preview (deferred to Phase 9): truncate to first ~2000 chars of combined markdown. Preview length calibrated to roughly two screen folds so that scrolling acts as a signal of interest before the upgrade prompt.

**`RequestEducatorAnalysis(ctx, sessionID, userID) error`**:
1. Verify ownership; if status is not `reviewed` → return `ErrEvaluationNotReady`
2. Load `educator_analyses` row if it exists:
   - Status `completed` → return `ErrAlreadyExists`
   - Status `generating` → return nil (idempotent, job already in flight)
   - Status `failed` → update status to `generating`, then enqueue (allows retry after failure)
3. Enqueue `GenerateEducatorContent` job

### `internal/backend/coach.go`

**Response type:**
```go
type CoachResponse struct {
    Narrative           string    `json:"narrative"`
    WeakestDimension    string    `json:"weakest_dimension,omitempty"`
    ImprovingDimensions []string  `json:"improving_dimensions,omitempty"`
    TopicGaps           []string  `json:"topic_gaps,omitempty"`
    SuggestedQuestionID *string   `json:"suggested_question_id,omitempty"`
    CreatedAt           time.Time `json:"created_at"`
}
```

**`GetLatestCoachAnalysis(ctx, userID) (*CoachResponse, error)`**:
1. Load latest `coach_analyses` row for user
2. No row → return nil

**`RequestCoachAnalysis(ctx, userID, force bool) error`**:
1. If not force: compare `sessions_analyzed` against current reviewed session IDs. If equal → `ErrNoNewSessions`
2. Enqueue `RunCoachAnalysis` job

**New sentinel errors** in `errors.go`: `ErrAlreadyExists`, `ErrNoNewSessions`.

---

## 7. HTTP Handlers

### `internal/handler/educator.go`

**`GET /api/sessions/{id}/educator`** → `GetEducatorAnalysis(b)`:
- Error mapping: `ErrSessionNotFound` → 404, `ErrSessionNotOwned` → 403, `ErrEvaluationNotReady` → 409
- 200 with `EducatorResponse`

**`POST /api/sessions/{id}/educator`** → `RequestEducatorAnalysis(b)`:
- Error mapping: `ErrAlreadyExists` → 200 (idempotent), `ErrSessionNotFound` → 404, `ErrSessionNotOwned` → 403, `ErrEvaluationNotReady` → 409
- 202 with `{"status": "generating"}`

### `internal/handler/coach.go`

**`GET /api/coach/latest`** → `GetCoachAnalysis(b)`:
- No row → 404
- 200 with `CoachResponse`

**`POST /api/coach/analyze`** → `RequestCoachAnalysis(b)`:
- Parses `?force=true` query param
- `ErrNoNewSessions` → 200 with `{"status": "up_to_date"}`
- 202 with `{"status": "analyzing"}`

All four endpoints behind auth middleware. Routes registered in `internal/handler/routes.go`.

---

## 8. WithCoachBriefing()

Addition to `internal/interview/prompt.go`:

```go
func (b *PromptBuilder) WithCoachBriefing(ca *db.CoachAnalysis) *PromptBuilder
```

Takes a pointer to the coach analysis row (or nil). Nil-safe: if `ca` is nil or `ca.Narrative` is empty, the method is a no-op. This matches the system design spec's nil-safe convention for all `With` methods.

Appends to the system prompt:

```
## Coach Briefing

This candidate's weakest area is {weakest_dimension}.
They are improving in: {improving_dimensions joined}.
Topic gaps to probe if relevant: {topic_gaps joined}.

Coach's assessment: {narrative, truncated to ~500 chars}
```

The conductor loads the latest coach analysis separately when `config_coach_briefing` is true, and passes the `*db.CoachAnalysis` (or nil) to `WithCoachBriefing()`. The `GetSessionWithCoachBriefing` LATERAL join query from Section 3 is an optimization that can be adopted later; the initial implementation uses a simple `GetLatestCoachAnalysis` call conditioned on `config_coach_briefing`.

---

## 9. Testing Strategy

### Unit tests (no DB, no LLM)

**`internal/educator/prompt_test.go`**:
- Transcript and evaluation summary formatting
- System prompt includes key instructional sections

**`internal/educator/parse_test.go`**:
- Valid tool input → correct `EducatorResult`
- Missing fields → error
- Empty strings → error

**`internal/coach/prompt_test.go`**:
- History summary with multiple sessions, scores, topics
- Zero sessions → baseline message
- Topic coverage computation (attempted vs. available tags)

**`internal/coach/parse_test.go`**:
- With generated question → correct result
- Without generated question (null) → nil field
- Invalid difficulty → error
- Empty narrative → error

### Integration tests (real DB, mocked LLM)

**`internal/jobs/educator_test.go`**:
- Seeds reviewed session with evaluation, messages, question
- Mocks `CallToolAndLog` with canned tool JSON
- Verifies `educator_analyses` row: status `completed`, content populated
- Idempotency: second run is a no-op
- Failure: verifies status transitions to `failed` on final attempt

**`internal/jobs/coach_test.go`**:
- Seeds user with 2 reviewed sessions + evaluations
- Mocks LLM, verifies `coach_analyses` row with correct `sessions_analyzed`
- With generated question: verifies `questions` row (source `coach_generated`, FK set)
- Debounce: no new sessions → no-op

**Handler tests**: error mapping and response shapes, following eval handler test pattern.

---

## 10. Functional Requirements Traceability

| FR | Requirement | Design Decision |
|----|-------------|-----------------|
| FR-047 | Request analysis after evaluation | `POST /api/sessions/:id/educator` gates on status `reviewed` |
| FR-048 | Model answer + gap deep-dives | Two markdown TEXT fields on `educator_analyses` |
| FR-049 | Fresh, personalized per session | No caching; prompt includes full transcript + evaluation |
| FR-050 | Rendered as formatted markdown | Backend returns raw markdown; frontend renders (Phase 9+) |
| FR-051 | Free tier preview | First ~2000 chars of combined markdown (~2 screen folds); deferred to Phase 9 |
| FR-052 | Retry on failure | River max attempts 5, exponential backoff; manual retry re-enqueues |
| FR-053 | Raw LLM response stored | `CallToolAndLog` persists to `llm_call_content` |
| FR-054 | Analyze complete history | Coach prompt built from all reviewed, non-archived sessions |
| FR-055 | Narrative + gap analysis | Structured columns: narrative, weakest_dimension, improving_dimensions, topic_gaps |
| FR-056 | Coach-generated questions | Inserted into `questions` with source `coach_generated`, FK on `coach_analyses` |
| FR-057 | Coach briefs interviewer | `WithCoachBriefing(ca *db.CoachAnalysis)` on PromptBuilder, conductor loads latest coach analysis when `config_coach_briefing` is true |
| FR-058 | Debounced | `sessions_analyzed` compared to current reviewed sessions; `?force=true` overrides |
| FR-059 | One concurrent analysis per user | River unique job constraint on `user_id` |
| FR-060 | Raw LLM response stored | `CallToolAndLog` persists to `llm_call_content` |
