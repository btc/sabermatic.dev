# Conductor Phase Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build the interview conductor — a WebSocket-driven state machine that orchestrates real-time system design interviews with LLM streaming, STT/TTS, and rigorous integration tests.

**Architecture:** Single-goroutine conductor owns all mutable state per interview session. Messages flow through a channel from a read loop; a fan-out observer pattern distributes LLM tokens to WebSocket writer, message accumulator, and TTS accumulator. Postgres advisory locks prevent concurrent conductors per session.

**Tech Stack:** Go, coder/websocket, Anthropic Go SDK, OpenAI Go SDK (Whisper + TTS), pgx/v5, River, sqlc, testcontainers

**Spec:** `docs/superpowers/specs/2026-04-03-conductor-design.md`

**Existing patterns to follow:**
- DI: `Backend` struct holds deps, handler factories take `*backend.Backend` → `http.HandlerFunc`
- Errors: sentinel vars in `backend/errors.go`, handlers switch with `errors.Is()`
- DB: sqlc queries in `sql/queries/`, generated to `internal/db/`
- Tests: testcontainers Postgres, `newTestBackend(t)` helper, `stretchr/testify`
- Jobs: `jobs.RegisterWorkers()` returns `*river.Workers`, workers implement `river.Worker[Args]`

---

## File Map

### New files
| File | Responsibility |
|---|---|
| `sql/queries/sessions.sql` | sqlc queries for interview_sessions |
| `sql/queries/messages.sql` | sqlc queries for messages |
| `sql/queries/llm_calls.sql` | sqlc queries for llm_calls + llm_call_content |
| `internal/interview/statemachine.go` | Pure state machine, no I/O |
| `internal/interview/statemachine_test.go` | All transitions, edge cases |
| `internal/interview/observer.go` | TokenObserver interface, TokenFanOut, WSWriter, MessageAccumulator, TTSAccumulator |
| `internal/interview/observer_test.go` | Fan-out, accumulator, interrupt |
| `internal/interview/prompt.go` | PromptBuilder |
| `internal/interview/prompt_test.go` | Builder output, nil-safety |
| `internal/interview/conductor.go` | Conductor struct, Run loop, message dispatch |
| `internal/interview/conductor_test.go` | WebSocket integration tests |
| `internal/interview/wsmessage.go` | WSMessage type, WSConn interface, parseWSMessage |
| `internal/ai/client.go` | LLM client with StreamAndLog, CallAndLog |
| `internal/ai/client_test.go` | Fake Anthropic server tests |
| `internal/ai/stt.go` | Transcriber interface + OpenAI impl |
| `internal/ai/tts.go` | Synthesizer interface + OpenAI impl |
| `internal/handler/session.go` | POST/GET /api/sessions endpoints |
| `internal/handler/session_ws.go` | WebSocket upgrade, read loop |
| `internal/handler/question.go` | GET /api/questions endpoint |
| `internal/jobs/evaluate.go` | Stub EvaluateSession worker |
| `internal/jobs/cleanup.go` | CleanupAbandonedSessions periodic job |

### Modified files
| File | Change |
|---|---|
| `go.mod` | Add coder/websocket, anthropic-sdk-go, openai-go |
| `internal/backend/backend.go` | Add `llm`, `stt`, `tts` fields; `LLM()`, `STT()`, `TTS()`, `Jobs()` accessors |
| `internal/backend/errors.go` | Add session/question sentinel errors |
| `internal/backend/session.go` | **New file** — session business logic (create, list, get) |
| `internal/backend/question.go` | **New file** — question listing |
| `internal/handler/routes.go` | Register session, question, WebSocket routes |
| `internal/jobs/workers.go` | Register EvaluateSession + CleanupAbandonedSessions workers |

---

## Task 1: sqlc Queries for Sessions, Messages, LLM Calls

**Files:**
- Create: `sql/queries/sessions.sql`
- Create: `sql/queries/messages.sql`
- Create: `sql/queries/llm_calls.sql`
- Modify: `sql/queries/questions.sql`
- Regenerate: `internal/db/*.go`

- [ ] **Step 1: Write session queries**

Create `sql/queries/sessions.sql`:

```sql
-- name: CreateSession :one
INSERT INTO interview_sessions (user_id, question_id, config_duration_minutes, config_tts_enabled)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, question_id, status, config_duration_minutes, config_tts_enabled,
          config_coach_briefing, started_at, ended_at, turn_count, archived, created_at, updated_at;

-- name: GetSession :one
SELECT id, user_id, question_id, status, config_duration_minutes, config_tts_enabled,
       config_coach_briefing, started_at, ended_at, turn_count, archived, created_at, updated_at
FROM interview_sessions
WHERE id = $1;

-- name: ListSessionsByUser :many
SELECT s.id, s.user_id, s.question_id, s.status, s.config_duration_minutes,
       s.config_tts_enabled, s.started_at, s.ended_at, s.turn_count, s.archived,
       s.created_at, q.title AS question_title
FROM interview_sessions s
JOIN questions q ON q.id = s.question_id
WHERE s.user_id = $1
ORDER BY s.created_at DESC;

-- name: UpdateSessionStatus :exec
UPDATE interview_sessions
SET status = $2, ended_at = NOW(), turn_count = $3, updated_at = NOW()
WHERE id = $1;

-- name: FindAbandonedSessions :many
SELECT id FROM interview_sessions
WHERE status = 'active'
  AND started_at + (config_duration_minutes + 5) * INTERVAL '1 minute' < NOW();

-- name: MarkSessionCompleted :exec
UPDATE interview_sessions
SET status = 'completed', ended_at = NOW(), updated_at = NOW()
WHERE id = $1;
```

- [ ] **Step 2: Write message queries**

Create `sql/queries/messages.sql`:

```sql
-- name: InsertMessage :one
INSERT INTO messages (id, session_id, seq, role, content, input_method, audio_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, session_id, seq, role, content, input_method, audio_url, created_at;

-- name: GetMessagesBySession :many
SELECT id, session_id, seq, role, content, input_method, audio_url, created_at
FROM messages
WHERE session_id = $1
ORDER BY seq;

-- name: GetMessagesBySessionAfterSeq :many
SELECT id, session_id, seq, role, content, input_method, audio_url, created_at
FROM messages
WHERE session_id = $1 AND seq > $2
ORDER BY seq;

-- name: GetMaxSeqForSession :one
SELECT COALESCE(MAX(seq), 0)::int AS max_seq
FROM messages
WHERE session_id = $1;
```

- [ ] **Step 3: Write LLM call queries**

Create `sql/queries/llm_calls.sql`:

```sql
-- name: InsertLLMCall :one
INSERT INTO llm_calls (session_id, user_id, role, model, input_tokens, output_tokens, estimated_cost, latency_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: InsertLLMCallContent :exec
INSERT INTO llm_call_content (llm_call_id, prompt, response)
VALUES ($1, $2, $3);
```

- [ ] **Step 4: Add question list query with filters**

Append to `sql/queries/questions.sql`:

```sql
-- name: ListQuestionsForUser :many
SELECT id, user_id, title, prompt, difficulty, tags, hints, source, created_at
FROM questions
WHERE (source = 'seed' AND user_id IS NULL) OR user_id = $1
ORDER BY created_at;
```

- [ ] **Step 5: Regenerate sqlc**

Run: `sqlc generate`
Expected: regenerates `internal/db/` with new query functions

- [ ] **Step 6: Verify compilation**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add sql/queries/ internal/db/
git commit -m "feat(conductor): add sqlc queries for sessions, messages, llm_calls, questions"
```

---

## Task 2: State Machine

**Files:**
- Create: `internal/interview/statemachine.go`
- Create: `internal/interview/statemachine_test.go`

- [ ] **Step 1: Write failing state machine tests**

Create `internal/interview/statemachine_test.go`:

```go
package interview_test

import (
	"testing"

	"github.com/btc/drill/internal/interview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStateMachine_ValidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from interview.ConductorState
		to   interview.ConductorState
	}{
		{"speaking to waiting", interview.StateInterviewerSpeaking, interview.StateWaitingForInput},
		{"speaking to ending", interview.StateInterviewerSpeaking, interview.StateEnding},
		{"waiting to transcribing", interview.StateWaitingForInput, interview.StateTranscribing},
		{"waiting to processing", interview.StateWaitingForInput, interview.StateProcessingInput},
		{"waiting to ending", interview.StateWaitingForInput, interview.StateEnding},
		{"transcribing to processing", interview.StateTranscribing, interview.StateProcessingInput},
		{"transcribing to ending", interview.StateTranscribing, interview.StateEnding},
		{"processing to speaking", interview.StateProcessingInput, interview.StateInterviewerSpeaking},
		{"processing to ending", interview.StateProcessingInput, interview.StateEnding},
		{"ending to ended", interview.StateEnding, interview.StateEnded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := interview.NewStateMachine(tt.from)
			err := sm.Transition(tt.to)
			require.NoError(t, err)
			assert.Equal(t, tt.to, sm.State())
		})
	}
}

func TestStateMachine_InvalidTransitions(t *testing.T) {
	tests := []struct {
		name string
		from interview.ConductorState
		to   interview.ConductorState
	}{
		{"ended is terminal", interview.StateEnded, interview.StateWaitingForInput},
		{"waiting cannot go to speaking", interview.StateWaitingForInput, interview.StateInterviewerSpeaking},
		{"transcribing cannot go to waiting", interview.StateTranscribing, interview.StateWaitingForInput},
		{"speaking cannot go to processing", interview.StateInterviewerSpeaking, interview.StateProcessingInput},
		{"ending cannot go to waiting", interview.StateEnding, interview.StateWaitingForInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := interview.NewStateMachine(tt.from)
			err := sm.Transition(tt.to)
			require.ErrorIs(t, err, interview.ErrInvalidTransition)
			assert.Equal(t, tt.from, sm.State(), "state should not change on invalid transition")
		})
	}
}

func TestStateMachine_TurnCount(t *testing.T) {
	sm := interview.NewStateMachine(interview.StateInterviewerSpeaking)
	assert.Equal(t, 0, sm.TurnCount())

	require.NoError(t, sm.Transition(interview.StateWaitingForInput))
	assert.Equal(t, 1, sm.TurnCount())

	require.NoError(t, sm.Transition(interview.StateProcessingInput))
	require.NoError(t, sm.Transition(interview.StateInterviewerSpeaking))
	require.NoError(t, sm.Transition(interview.StateWaitingForInput))
	assert.Equal(t, 2, sm.TurnCount())
}

func TestStateMachine_SelfTransitionInvalid(t *testing.T) {
	sm := interview.NewStateMachine(interview.StateWaitingForInput)
	err := sm.Transition(interview.StateWaitingForInput)
	require.ErrorIs(t, err, interview.ErrInvalidTransition)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/interview/ -v`
Expected: FAIL — package does not exist

- [ ] **Step 3: Implement state machine**

Create `internal/interview/statemachine.go`:

```go
package interview

import (
	"fmt"
	"time"
)

var ErrInvalidTransition = fmt.Errorf("invalid state transition")

type ConductorState string

const (
	StateInterviewerSpeaking ConductorState = "interviewer_speaking"
	StateWaitingForInput     ConductorState = "waiting_for_input"
	StateTranscribing        ConductorState = "transcribing"
	StateProcessingInput     ConductorState = "processing_input"
	StateEnding              ConductorState = "ending"
	StateEnded               ConductorState = "ended"
)

var transitions = map[ConductorState]map[ConductorState]bool{
	StateInterviewerSpeaking: {StateWaitingForInput: true, StateEnding: true},
	StateWaitingForInput:     {StateTranscribing: true, StateProcessingInput: true, StateEnding: true},
	StateTranscribing:        {StateProcessingInput: true, StateEnding: true},
	StateProcessingInput:     {StateInterviewerSpeaking: true, StateEnding: true},
	StateEnding:              {StateEnded: true},
	StateEnded:               {},
}

type StateMachine struct {
	state     ConductorState
	turnCount int
	startedAt time.Time
}

func NewStateMachine(initial ConductorState) *StateMachine {
	return &StateMachine{state: initial, startedAt: time.Now()}
}

func (sm *StateMachine) Transition(next ConductorState) error {
	allowed := transitions[sm.state]
	if !allowed[next] {
		return fmt.Errorf("%w: %s → %s", ErrInvalidTransition, sm.state, next)
	}
	if next == StateWaitingForInput {
		sm.turnCount++
	}
	sm.state = next
	return nil
}

func (sm *StateMachine) State() ConductorState { return sm.state }
func (sm *StateMachine) TurnCount() int         { return sm.turnCount }
func (sm *StateMachine) StartedAt() time.Time   { return sm.startedAt }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/interview/ -v`
Expected: PASS — all tests green

- [ ] **Step 5: Commit**

```bash
git add internal/interview/
git commit -m "feat(conductor): add state machine with transition table"
```

---

## Task 3: Sentinel Errors + Backend Accessors

**Files:**
- Modify: `internal/backend/errors.go`
- Modify: `internal/backend/backend.go`

- [ ] **Step 1: Add sentinel errors**

Append to `internal/backend/errors.go`:

```go
var (
	ErrSessionNotFound  = fmt.Errorf("session not found")
	ErrSessionNotActive = fmt.Errorf("session is not active")
	ErrSessionNotOwned  = fmt.Errorf("session does not belong to user")
	ErrQuestionNotFound = fmt.Errorf("question not found")
	ErrInvalidDuration  = fmt.Errorf("duration must be between 1 and 180 minutes")
)
```

- [ ] **Step 2: Add AI deps and accessors to Backend**

In `internal/backend/backend.go`, add imports for the `ai` package (this will fail to compile until Task 6 creates the `ai` package — that's fine, we just add the accessor methods and placeholder fields for now). For now, add only `Jobs()` which references the existing `Jobs` interface:

Add this method after the existing `Pool()` method:

```go
// Jobs returns the River job client.
func (b *Backend) Jobs() Jobs { return b.jobs }
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/backend/errors.go internal/backend/backend.go
git commit -m "feat(conductor): add session/question errors and Jobs() accessor"
```

---

## Task 4: WSMessage Type + WSConn Interface

**Files:**
- Create: `internal/interview/wsmessage.go`
- Create: `internal/interview/wsmessage_test.go`

- [ ] **Step 1: Write failing parse tests**

Create `internal/interview/wsmessage_test.go`:

```go
package interview_test

import (
	"testing"

	"github.com/btc/drill/internal/interview"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseWSMessage_SessionInit(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"session_init","last_seq":5}`))
	require.NoError(t, err)
	assert.Equal(t, "session_init", msg.Type)
	require.NotNil(t, msg.LastSeq)
	assert.Equal(t, 5, *msg.LastSeq)
}

func TestParseWSMessage_SessionInitNullSeq(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"session_init","last_seq":null}`))
	require.NoError(t, err)
	assert.Equal(t, "session_init", msg.Type)
	assert.Nil(t, msg.LastSeq)
}

func TestParseWSMessage_EndTurnText(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"end_turn","content":"my answer","input_method":"text"}`))
	require.NoError(t, err)
	assert.Equal(t, "end_turn", msg.Type)
	assert.Equal(t, "text", msg.InputMethod)
	assert.Equal(t, "my answer", msg.Content)
}

func TestParseWSMessage_EndTurnVoice(t *testing.T) {
	// "aGVsbG8=" is base64 for "hello"
	msg, err := interview.ParseWSMessage([]byte(`{"type":"end_turn","audio":"aGVsbG8=","input_method":"voice"}`))
	require.NoError(t, err)
	assert.Equal(t, "end_turn", msg.Type)
	assert.Equal(t, "voice", msg.InputMethod)
	assert.Equal(t, []byte("hello"), msg.Audio)
}

func TestParseWSMessage_CancelTTS(t *testing.T) {
	msg, err := interview.ParseWSMessage([]byte(`{"type":"cancel_tts"}`))
	require.NoError(t, err)
	assert.Equal(t, "cancel_tts", msg.Type)
}

func TestParseWSMessage_InvalidJSON(t *testing.T) {
	_, err := interview.ParseWSMessage([]byte(`not json`))
	require.Error(t, err)
}

func TestParseWSMessage_MissingType(t *testing.T) {
	_, err := interview.ParseWSMessage([]byte(`{"content":"hello"}`))
	require.Error(t, err)
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/interview/ -v -run TestParseWSMessage`
Expected: FAIL — ParseWSMessage not defined

- [ ] **Step 3: Implement WSMessage and WSConn**

Create `internal/interview/wsmessage.go`:

```go
package interview

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"nhooyr.io/websocket"
)

// WSConn is the write interface for the WebSocket connection.
// The conductor writes through this; the read loop reads from the raw *websocket.Conn.
type WSConn interface {
	SendJSON(ctx context.Context, v any) error
	Close(code websocket.StatusCode, reason string) error
}

// Conn wraps a *websocket.Conn to implement WSConn.
type Conn struct {
	WS *websocket.Conn
}

func (c *Conn) SendJSON(ctx context.Context, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal ws message: %w", err)
	}
	return c.WS.Write(ctx, websocket.MessageText, data)
}

func (c *Conn) Close(code websocket.StatusCode, reason string) error {
	return c.WS.Close(code, reason)
}

// WSMessage represents a client-to-server WebSocket message.
type WSMessage struct {
	Type        string `json:"type"`
	LastSeq     *int   `json:"last_seq,omitempty"`
	Content     string `json:"content,omitempty"`
	Audio       []byte `json:"-"`
	AudioBase64 string `json:"audio,omitempty"`
	InputMethod string `json:"input_method,omitempty"`
}

// ParseWSMessage parses a raw JSON WebSocket message.
func ParseWSMessage(data []byte) (WSMessage, error) {
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return WSMessage{}, fmt.Errorf("unmarshal ws message: %w", err)
	}
	if msg.Type == "" {
		return WSMessage{}, fmt.Errorf("missing message type")
	}
	if msg.AudioBase64 != "" {
		audio, err := base64.StdEncoding.DecodeString(msg.AudioBase64)
		if err != nil {
			return WSMessage{}, fmt.Errorf("decode audio base64: %w", err)
		}
		msg.Audio = audio
	}
	return msg, nil
}
```

**Note:** The import path for coder/websocket is `github.com/coder/websocket` — the package name is `websocket`. When adding the dependency:

Run: `go get github.com/coder/websocket`

Update the import in `wsmessage.go` to use `github.com/coder/websocket` (not `nhooyr.io/websocket`).

- [ ] **Step 4: Run tests**

Run: `go test ./internal/interview/ -v -run TestParseWSMessage`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/interview/wsmessage.go internal/interview/wsmessage_test.go go.mod go.sum
git commit -m "feat(conductor): add WSMessage type, WSConn interface, message parsing"
```

---

## Task 5: Observer Pattern

**Files:**
- Create: `internal/interview/observer.go`
- Create: `internal/interview/observer_test.go`

- [ ] **Step 1: Write failing observer tests**

Create `internal/interview/observer_test.go`:

```go
package interview_test

import (
	"testing"

	"github.com/btc/drill/internal/interview"
	"github.com/stretchr/testify/assert"
)

// spy implements TokenObserver for testing.
type spy struct {
	tokens      []string
	doneMsg     string
	errVal      error
	interrupted bool
}

func (s *spy) OnToken(token string)        { s.tokens = append(s.tokens, token) }
func (s *spy) OnDone(fullMessage string)    { s.doneMsg = fullMessage }
func (s *spy) OnError(err error)            { s.errVal = err }
func (s *spy) Interrupt()                   { s.interrupted = true }

func TestTokenFanOut_DistributesToAll(t *testing.T) {
	a, b := &spy{}, &spy{}
	fan := interview.NewTokenFanOut(a, b)

	fan.OnToken("hello")
	fan.OnToken(" world")
	fan.OnDone("hello world")

	assert.Equal(t, []string{"hello", " world"}, a.tokens)
	assert.Equal(t, []string{"hello", " world"}, b.tokens)
	assert.Equal(t, "hello world", a.doneMsg)
	assert.Equal(t, "hello world", b.doneMsg)
}

func TestTokenFanOut_InterruptPropagates(t *testing.T) {
	a, b := &spy{}, &spy{}
	fan := interview.NewTokenFanOut(a, b)

	fan.Interrupt()

	assert.True(t, a.interrupted)
	assert.True(t, b.interrupted)
}

func TestTokenFanOut_ErrorPropagates(t *testing.T) {
	a, b := &spy{}, &spy{}
	fan := interview.NewTokenFanOut(a, b)

	testErr := assert.AnError
	fan.OnError(testErr)

	assert.Equal(t, testErr, a.errVal)
	assert.Equal(t, testErr, b.errVal)
}

func TestMessageAccumulator(t *testing.T) {
	acc := interview.NewMessageAccumulator()
	acc.OnToken("hello")
	acc.OnToken(" world")
	acc.OnDone("hello world")
	assert.Equal(t, "hello world", acc.Text())
}

func TestMessageAccumulator_EmptyStream(t *testing.T) {
	acc := interview.NewMessageAccumulator()
	acc.OnDone("")
	assert.Equal(t, "", acc.Text())
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/interview/ -v -run "TestTokenFanOut|TestMessageAccumulator"`
Expected: FAIL

- [ ] **Step 3: Implement observers**

Create `internal/interview/observer.go`:

```go
package interview

import "strings"

// TokenObserver receives streaming LLM tokens.
type TokenObserver interface {
	OnToken(token string)
	OnDone(fullMessage string)
	OnError(err error)
	Interrupt()
}

// TokenFanOut distributes to all child observers.
type TokenFanOut struct {
	observers []TokenObserver
}

func NewTokenFanOut(observers ...TokenObserver) *TokenFanOut {
	return &TokenFanOut{observers: observers}
}

func (f *TokenFanOut) OnToken(token string) {
	for _, o := range f.observers {
		o.OnToken(token)
	}
}

func (f *TokenFanOut) OnDone(fullMessage string) {
	for _, o := range f.observers {
		o.OnDone(fullMessage)
	}
}

func (f *TokenFanOut) OnError(err error) {
	for _, o := range f.observers {
		o.OnError(err)
	}
}

func (f *TokenFanOut) Interrupt() {
	for _, o := range f.observers {
		o.Interrupt()
	}
}

// MessageAccumulator builds the complete response for DB persistence.
type MessageAccumulator struct {
	buf strings.Builder
}

func NewMessageAccumulator() *MessageAccumulator {
	return &MessageAccumulator{}
}

func (a *MessageAccumulator) OnToken(token string) { a.buf.WriteString(token) }
func (a *MessageAccumulator) OnDone(string)         {}
func (a *MessageAccumulator) OnError(error)          {}
func (a *MessageAccumulator) Interrupt()             {}
func (a *MessageAccumulator) Text() string           { return a.buf.String() }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/interview/ -v -run "TestTokenFanOut|TestMessageAccumulator"`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/interview/observer.go internal/interview/observer_test.go
git commit -m "feat(conductor): add TokenObserver interface, fan-out, and MessageAccumulator"
```

---

## Task 6: STT/TTS Interfaces + OpenAI Implementations

**Files:**
- Create: `internal/ai/stt.go`
- Create: `internal/ai/tts.go`

- [ ] **Step 1: Add OpenAI dependency**

Run: `go get github.com/openai/openai-go`

Verify the actual module path that gets resolved — the import may be `github.com/openai/openai-go` or include a version suffix. Use whatever `go get` resolves.

- [ ] **Step 2: Create STT interface and OpenAI implementation**

Create `internal/ai/stt.go`:

```go
package ai

import (
	"bytes"
	"context"
	"fmt"

	"github.com/openai/openai-go"       // adjust import if version suffix differs
	"github.com/openai/openai-go/option" // adjust import if needed
)

// Transcriber converts audio bytes to text.
type Transcriber interface {
	Transcribe(ctx context.Context, audio []byte, format string) (string, error)
}

// OpenAITranscriber uses OpenAI Whisper for speech-to-text.
type OpenAITranscriber struct {
	client *openai.Client
	model  string
}

func NewOpenAITranscriber(apiKey, model string) *OpenAITranscriber {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAITranscriber{client: client, model: model}
}

func (t *OpenAITranscriber) Transcribe(ctx context.Context, audio []byte, format string) (string, error) {
	filename := fmt.Sprintf("audio.%s", format)
	resp, err := t.client.Audio.Transcriptions.New(ctx, openai.AudioTranscriptionNewParams{
		File:  openai.FileParam(bytes.NewReader(audio), filename, fmt.Sprintf("audio/%s", format)),
		Model: t.model,
	})
	if err != nil {
		return "", fmt.Errorf("transcribe: %w", err)
	}
	return resp.Text, nil
}
```

**Important:** The exact API for the OpenAI Go SDK may differ — check the SDK docs at implementation time. The `openai.FileParam` and `AudioTranscriptionNewParams` signatures should be verified against the actual SDK version that `go get` resolves. Adjust field names and constructor calls to match.

- [ ] **Step 3: Create TTS interface and OpenAI implementation**

Create `internal/ai/tts.go`:

```go
package ai

import (
	"context"
	"fmt"
	"io"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

// Synthesizer converts text to audio.
type Synthesizer interface {
	Synthesize(ctx context.Context, text string) (io.ReadCloser, error)
}

// OpenAISynthesizer uses OpenAI TTS for text-to-speech.
type OpenAISynthesizer struct {
	client *openai.Client
	model  string
	voice  string
}

func NewOpenAISynthesizer(apiKey, model, voice string) *OpenAISynthesizer {
	client := openai.NewClient(option.WithAPIKey(apiKey))
	return &OpenAISynthesizer{client: client, model: model, voice: voice}
}

func (s *OpenAISynthesizer) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	resp, err := s.client.Audio.Speech.New(ctx, openai.AudioSpeechNewParams{
		Input: text,
		Model: s.model,
		Voice: openai.AudioSpeechNewParamsVoice(s.voice),
	})
	if err != nil {
		return nil, fmt.Errorf("synthesize: %w", err)
	}
	return resp.Body, nil
}
```

**Same caveat:** Verify the exact SDK API at implementation time. The `AudioSpeechNewParams` struct and `resp.Body` pattern should be confirmed.

- [ ] **Step 4: Verify compilation**

Run: `go build ./internal/ai/...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/ai/ go.mod go.sum
git commit -m "feat(conductor): add STT/TTS interfaces with OpenAI implementations"
```

---

## Task 7: LLM Client (StreamAndLog + CallAndLog)

**Files:**
- Create: `internal/ai/client.go`
- Create: `internal/ai/client_test.go`

- [ ] **Step 1: Add Anthropic SDK dependency**

Run: `go get github.com/anthropics/anthropic-sdk-go`

Verify the resolved module path and import.

- [ ] **Step 2: Write failing LLM client tests**

Create `internal/ai/client_test.go`. The test spins up a fake HTTP server that mimics the Anthropic streaming SSE API:

```go
package ai_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/ai"
	// Adjust Anthropic imports to match resolved SDK
)

// fakeAnthropicServer returns an httptest server that streams SSE token events.
func fakeAnthropicServer(tokens []string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		// message_start
		fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_test\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-20250514\",\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}}\n\n")
		flusher.Flush()

		// content_block_start
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		flusher.Flush()

		// content_block_delta for each token
		for _, tok := range tokens {
			tokJSON, _ := json.Marshal(tok)
			fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n", tokJSON)
			flusher.Flush()
		}

		// content_block_stop
		fmt.Fprintf(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		flusher.Flush()

		// message_delta with usage
		fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":%d}}\n\n", len(tokens)*5)
		flusher.Flush()

		// message_stop
		fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
		flusher.Flush()
	}))
}

func TestStreamAndLog_YieldsTokens(t *testing.T) {
	server := fakeAnthropicServer([]string{"Hello", " world", "!"})
	defer server.Close()

	// Create ai.Client pointed at fake server
	// The exact constructor depends on how the Anthropic SDK allows overriding the base URL.
	// This test verifies the token iteration logic. DB persistence is tested separately.
	client := ai.NewTestClient(t, server.URL, nil) // nil pool = skip DB persistence

	stream, err := client.StreamAndLog(context.Background(), ai.StreamParams{
		Model:  "claude-sonnet-4-20250514",
		System: "You are helpful.",
		UserID: uuid.New(),
		Role:   "interviewer",
	})
	require.NoError(t, err)

	var tokens []string
	for {
		tok, err := stream.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		tokens = append(tokens, tok)
	}

	assert.Equal(t, []string{"Hello", " world", "!"}, tokens)
	assert.Equal(t, "Hello world!", stream.FullMessage())
	require.NoError(t, stream.Close(context.Background()))
}
```

**Note:** The exact Anthropic SDK streaming API may differ. The test structure is:
1. Fake SSE server returns canned tokens
2. `ai.Client` created with fake server URL
3. `StreamAndLog` returns a `TokenStream`
4. Iterate with `Next()` until `io.EOF`
5. Verify tokens and `FullMessage()`

The DB persistence test (verifying `llm_calls` rows) requires a real Postgres — defer to the integration test in Task 12.

- [ ] **Step 3: Implement LLM client**

Create `internal/ai/client.go`:

```go
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/btc/drill/internal/db"
)

// pricing maps model names to per-token costs in USD.
var pricing = map[string]struct{ Input, Output float64 }{
	"claude-sonnet-4-20250514": {Input: 3.0 / 1_000_000, Output: 15.0 / 1_000_000},
}

// Client wraps the Anthropic SDK with cost tracking and logging.
type Client struct {
	anthropic *anthropic.Client
	pool      *pgxpool.Pool // nil in unit tests (skip DB persistence)
}

func NewClient(apiKey string, pool *pgxpool.Pool) *Client {
	client := anthropic.NewClient(/* apiKey option */)
	return &Client{anthropic: client, pool: pool}
}

// StreamParams defines the input for a streaming LLM call.
type StreamParams struct {
	Model     string
	System    string
	Messages  []anthropic.MessageParam
	UserID    uuid.UUID
	Role      string
	SessionID uuid.UUID
}

// TokenStream iterates over streaming tokens.
type TokenStream struct {
	stream      *anthropic.MessageStream // or appropriate SDK type
	buf         strings.Builder
	params      StreamParams
	pool        *pgxpool.Pool
	startTime   time.Time
	inputTokens int
	outputTokens int
}

func (s *TokenStream) Next() (string, error) {
	// Read next event from the Anthropic stream.
	// On content_block_delta with text_delta, return the text.
	// On message_stop, return io.EOF.
	// Accumulate into buf for FullMessage().
	// Track token counts from usage events.
	panic("implement based on actual Anthropic SDK streaming API")
}

func (s *TokenStream) FullMessage() string {
	return s.buf.String()
}

// CloseWithTx persists llm_calls within the given transaction.
func (s *TokenStream) CloseWithTx(ctx context.Context, tx pgx.Tx) error {
	return s.persistLLMCall(ctx, db.New(tx))
}

// Close persists llm_calls using its own connection from the pool.
func (s *TokenStream) Close(ctx context.Context) error {
	if s.pool == nil {
		return nil // unit test mode
	}
	return s.persistLLMCall(ctx, db.New(s.pool))
}

func (s *TokenStream) persistLLMCall(ctx context.Context, q *db.Queries) error {
	cost := computeCost(s.params.Model, s.inputTokens, s.outputTokens)
	latency := time.Since(s.startTime).Milliseconds()

	callID, err := q.InsertLLMCall(ctx, db.InsertLLMCallParams{
		SessionID:    s.params.SessionID,
		UserID:       s.params.UserID,
		Role:         s.params.Role,
		Model:        s.params.Model,
		InputTokens:  int32(s.inputTokens),
		OutputTokens: int32(s.outputTokens),
		EstimatedCost: fmt.Sprintf("%.6f", cost), // numeric field
		LatencyMs:    int32(latency),
	})
	if err != nil {
		return fmt.Errorf("insert llm_call: %w", err)
	}

	promptJSON, _ := json.Marshal(s.params.Messages)
	responseJSON, _ := json.Marshal(map[string]string{"content": s.FullMessage()})
	return q.InsertLLMCallContent(ctx, db.InsertLLMCallContentParams{
		LlmCallID: callID,
		Prompt:    promptJSON,
		Response:  responseJSON,
	})
}

func computeCost(model string, inputTokens, outputTokens int) float64 {
	p, ok := pricing[model]
	if !ok {
		return 0
	}
	return float64(inputTokens)*p.Input + float64(outputTokens)*p.Output
}
```

**Critical implementation notes:**
- The exact Anthropic SDK streaming API must be checked at implementation time. The SDK may use `client.Messages.Stream(ctx, params)` returning an iterator or event stream. Adapt the `Next()` method to match.
- `NewClient` and `NewTestClient` constructors need to support overriding the base URL for testing. The Anthropic SDK likely accepts an option like `anthropic.WithBaseURL(url)`.
- The `InsertLLMCall` params types will be generated by sqlc from Task 1. Match the generated param struct field names exactly.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/ai/ -v`
Expected: PASS (at least the token iteration test)

- [ ] **Step 5: Commit**

```bash
git add internal/ai/client.go internal/ai/client_test.go go.mod go.sum
git commit -m "feat(conductor): add LLM client with StreamAndLog and CallAndLog"
```

---

## Task 8: Prompt Builder

**Files:**
- Create: `internal/interview/prompt.go`
- Create: `internal/interview/prompt_test.go`

- [ ] **Step 1: Write failing prompt tests**

Create `internal/interview/prompt_test.go`:

```go
package interview_test

import (
	"testing"
	"time"

	"github.com/btc/drill/internal/interview"
	"github.com/btc/drill/internal/db"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPromptBuilder_Basic(t *testing.T) {
	system, msgs := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{
			ID:    uuid.New(),
			Title: "URL Shortener",
			Prompt: "Design a URL shortening service.",
		}).
		WithTimeContext(5*time.Minute, 25*time.Minute).
		Build()

	assert.Contains(t, system, "URL Shortener")
	assert.Contains(t, system, "Design a URL shortening service")
	assert.Contains(t, system, "5 minutes")  // elapsed
	assert.Contains(t, system, "25 minutes") // remaining
	assert.Empty(t, msgs, "no transcript means no messages")
}

func TestPromptBuilder_WithTranscript(t *testing.T) {
	messages := []db.Message{
		{Seq: 1, Role: "interviewer", Content: "Design a URL shortener."},
		{Seq: 2, Role: "candidate", Content: "I'd start with the API design."},
	}

	_, msgs := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "URL Shortener", Prompt: "Design a URL shortening service."}).
		WithTranscript(messages).
		WithTimeContext(10*time.Minute, 20*time.Minute).
		Build()

	require.Len(t, msgs, 2)
	// First message is assistant (interviewer), second is user (candidate)
}

func TestPromptBuilder_WithCoachBriefingNil(t *testing.T) {
	// Nil briefing should not panic or add content
	system, _ := interview.NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(db.Question{Title: "Test", Prompt: "Test"}).
		WithCoachBriefing(nil).
		WithTimeContext(0, 30*time.Minute).
		Build()

	assert.NotContains(t, system, "Coach Briefing")
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/interview/ -v -run TestPromptBuilder`
Expected: FAIL

- [ ] **Step 3: Implement prompt builder**

Create `internal/interview/prompt.go`. Port the system prompt from `v0/backend/interviewer.py` (the behavioral rules are battle-tested). The builder pattern:

```go
package interview

import (
	"fmt"
	"strings"
	"time"

	"github.com/btc/drill/internal/db"
	// Anthropic message param type — adjust import to match SDK
)

type PromptBuilder struct {
	system strings.Builder
	msgs   []any // replace with actual Anthropic MessageParam type
}

func NewInterviewerPrompt() *PromptBuilder {
	return &PromptBuilder{}
}

func (b *PromptBuilder) WithSystemInstructions() *PromptBuilder {
	b.system.WriteString(`You are a senior staff engineer conducting a system design interview.

## Your Behavioral Rules

### 1. Open with deliberate vagueness.
Present the question in ONE or TWO short sentences. Do NOT add any detail, constraints, scale numbers, suggested phases, time management advice, or hints about what to cover.

### 2. Stay silent when the candidate should be driving.
Do not jump in to help. Do not fill silence. Only speak when the candidate has finished a thought or explicitly asks you something.

### 3. Answer clarifying questions collaboratively.
When the candidate asks a reasonable scoping question, help them. Product-context questions: answer directly. Open-ended use-case questions: gently redirect. Scope decisions: engage but let them decide.

### 4. Probe with WHY, not WHAT.
Challenge technology choices: "You said Kafka. Why not SQS?" Force justification.

### 5. Introduce constraints progressively.
As the candidate builds their design, introduce scale challenges, failure modes, or edge cases.

### 6. Track coverage across dimensions.
Requirements gathering, high-level design, deep dives, scalability, trade-offs.

### 7. Push past hand-waving.
If the candidate says "we'd use a cache" without specifics, probe: "What's the eviction policy? How do you handle cache invalidation?"

### 8. Stay neutral.
No "great question!" or "that's a good point!" — just respond substantively.

### 9. Keep responses short.
2-4 sentences typical. You are an interviewer, not a lecturer.

`)
	return b
}

func (b *PromptBuilder) WithQuestion(q db.Question) *PromptBuilder {
	fmt.Fprintf(&b.system, "\n## Question: %s\n\n%s\n\n", q.Title, q.Prompt)
	return b
}

func (b *PromptBuilder) WithTimeContext(elapsed, remaining time.Duration) *PromptBuilder {
	fmt.Fprintf(&b.system, "## Time Status\n\nElapsed: %d minutes. Remaining: %d minutes.\n\n",
		int(elapsed.Minutes()), int(remaining.Minutes()))

	remainingMin := int(remaining.Minutes())
	if remainingMin <= 5 {
		b.system.WriteString("**Time is nearly up.** Guide the candidate toward wrapping up. Ask for final thoughts on trade-offs or what they'd do differently.\n\n")
	} else if remainingMin <= 10 {
		b.system.WriteString("**Entering final stretch.** If major areas haven't been covered, gently steer toward them.\n\n")
	}
	return b
}

func (b *PromptBuilder) WithTranscript(msgs []db.Message) *PromptBuilder {
	for _, m := range msgs {
		role := "user"
		if m.Role == "interviewer" {
			role = "assistant"
		}
		// Build Anthropic MessageParam — adjust to actual SDK type
		_ = role
		_ = m.Content
		// b.msgs = append(b.msgs, anthropic.MessageParam{Role: role, Content: m.Content})
	}
	return b
}

func (b *PromptBuilder) WithCoachBriefing(ca *db.CoachAnalysis) *PromptBuilder {
	if ca == nil {
		return b
	}
	fmt.Fprintf(&b.system, "\n## Coach Briefing\n\n%s\n\n", ca.Narrative)
	return b
}

func (b *PromptBuilder) Build() (string, []any) {
	return b.system.String(), b.msgs
}
```

**Implementation notes:**
- The `msgs` field and `WithTranscript` need to use the actual Anthropic SDK `MessageParam` type. Replace `[]any` with the correct type from the SDK.
- The system prompt text above is abbreviated — the full version from `v0/backend/interviewer.py` has 10 behavioral rules. Port all of them.
- `db.CoachAnalysis` is a sqlc-generated type. It may need `Narrative` or another field name — check the generated types from Task 1.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/interview/ -v -run TestPromptBuilder`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/interview/prompt.go internal/interview/prompt_test.go
git commit -m "feat(conductor): add PromptBuilder with interviewer behavioral rules"
```

---

## Task 9: Backend Session + Question Logic

**Files:**
- Create: `internal/backend/session.go`
- Create: `internal/backend/question.go`

- [ ] **Step 1: Implement session business logic**

Create `internal/backend/session.go`:

```go
package backend

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
)

type CreateSessionParams struct {
	UserID          uuid.UUID
	QuestionID      uuid.UUID
	DurationMinutes int
	TTSEnabled      bool
}

func (b *Backend) CreateSession(ctx context.Context, p CreateSessionParams) (db.InterviewSession, error) {
	if p.DurationMinutes < 1 || p.DurationMinutes > 180 {
		return db.InterviewSession{}, ErrInvalidDuration
	}

	q := db.New(b.pool)

	// Verify question exists
	_, err := q.GetQuestion(ctx, p.QuestionID)
	if err != nil {
		if err == pgx.ErrNoRows {
			return db.InterviewSession{}, ErrQuestionNotFound
		}
		return db.InterviewSession{}, fmt.Errorf("get question: %w", err)
	}

	session, err := q.CreateSession(ctx, db.CreateSessionParams{
		UserID:                p.UserID,
		QuestionID:            p.QuestionID,
		ConfigDurationMinutes: int32(p.DurationMinutes),
		ConfigTtsEnabled:      p.TTSEnabled,
	})
	if err != nil {
		return db.InterviewSession{}, fmt.Errorf("create session: %w", err)
	}
	return session, nil
}

func (b *Backend) GetSession(ctx context.Context, id uuid.UUID) (db.InterviewSession, error) {
	q := db.New(b.pool)
	session, err := q.GetSession(ctx, id)
	if err != nil {
		if err == pgx.ErrNoRows {
			return db.InterviewSession{}, ErrSessionNotFound
		}
		return db.InterviewSession{}, fmt.Errorf("get session: %w", err)
	}
	return session, nil
}

func (b *Backend) GetSessionForUser(ctx context.Context, id, userID uuid.UUID) (db.InterviewSession, error) {
	session, err := b.GetSession(ctx, id)
	if err != nil {
		return db.InterviewSession{}, err
	}
	if session.UserID != userID {
		return db.InterviewSession{}, ErrSessionNotOwned
	}
	return session, nil
}

func (b *Backend) ListSessions(ctx context.Context, userID uuid.UUID) ([]db.ListSessionsByUserRow, error) {
	q := db.New(b.pool)
	return q.ListSessionsByUser(ctx, userID)
}
```

- [ ] **Step 2: Implement question business logic**

Create `internal/backend/question.go`:

```go
package backend

import (
	"context"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/db"
)

func (b *Backend) ListQuestions(ctx context.Context, userID uuid.UUID) ([]db.ListQuestionsForUserRow, error) {
	q := db.New(b.pool)
	return q.ListQuestionsForUser(ctx, userID)
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/backend/...`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/backend/session.go internal/backend/question.go
git commit -m "feat(conductor): add session/question backend business logic"
```

---

## Task 10: Session + Question HTTP Handlers

**Files:**
- Create: `internal/handler/session.go`
- Create: `internal/handler/question.go`
- Modify: `internal/handler/routes.go`

- [ ] **Step 1: Implement session handlers**

Create `internal/handler/session.go`:

```go
package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

func CreateSession(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())

		var req struct {
			QuestionID      uuid.UUID `json:"question_id"`
			DurationMinutes int       `json:"duration_minutes"`
			TTSEnabled      bool      `json:"tts_enabled"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
			return
		}

		session, err := b.CreateSession(r.Context(), backend.CreateSessionParams{
			UserID:          user.ID,
			QuestionID:      req.QuestionID,
			DurationMinutes: req.DurationMinutes,
			TTSEnabled:      req.TTSEnabled,
		})
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrInvalidDuration):
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrQuestionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusCreated, session)
	}
}

func ListSessions(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		sessions, err := b.ListSessions(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, sessions)
	}
}

func GetSession(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid session ID"})
			return
		}

		session, err := b.GetSessionForUser(r.Context(), id, user.ID)
		if err != nil {
			switch {
			case errors.Is(err, backend.ErrSessionNotFound):
				writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			case errors.Is(err, backend.ErrSessionNotOwned):
				writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
			default:
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			}
			return
		}

		writeJSON(w, http.StatusOK, session)
	}
}
```

- [ ] **Step 2: Implement question handler**

Create `internal/handler/question.go`:

```go
package handler

import (
	"net/http"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
)

func ListQuestions(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		questions, err := b.ListQuestions(r.Context(), user.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
			return
		}
		writeJSON(w, http.StatusOK, questions)
	}
}
```

- [ ] **Step 3: Register routes**

Add to `internal/handler/routes.go` inside `RegisterRoutes`, after the existing `GET /api/me` line:

```go
	// Sessions
	mux.Handle("POST /api/sessions", requireAuth(http.HandlerFunc(CreateSession(b))))
	mux.Handle("GET /api/sessions", requireAuth(http.HandlerFunc(ListSessions(b))))
	mux.Handle("GET /api/sessions/{id}", requireAuth(http.HandlerFunc(GetSession(b))))

	// Questions
	mux.Handle("GET /api/questions", requireAuth(http.HandlerFunc(ListQuestions(b))))
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/handler/session.go internal/handler/question.go internal/handler/routes.go
git commit -m "feat(conductor): add session/question HTTP handlers and routes"
```

---

## Task 11: Jobs (Stub EvaluateSession + CleanupAbandonedSessions)

**Files:**
- Create: `internal/jobs/evaluate.go`
- Create: `internal/jobs/cleanup.go`
- Modify: `internal/jobs/workers.go`

- [ ] **Step 1: Create stub EvaluateSession worker**

Create `internal/jobs/evaluate.go`:

```go
package jobs

import (
	"context"

	"github.com/google/uuid"
	"github.com/riverqueue/river"
)

type EvaluateSessionArgs struct {
	SessionID uuid.UUID `json:"session_id"`
}

func (EvaluateSessionArgs) Kind() string { return "evaluate_session" }

type EvaluateSessionWorker struct {
	river.WorkerDefaults[EvaluateSessionArgs]
}

func (w *EvaluateSessionWorker) Work(ctx context.Context, job *river.Job[EvaluateSessionArgs]) error {
	// Stub — Phase 7 replaces with actual evaluation logic.
	return nil
}
```

- [ ] **Step 2: Create CleanupAbandonedSessions worker**

Create `internal/jobs/cleanup.go`:

```go
package jobs

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
)

type CleanupAbandonedSessionsArgs struct{}

func (CleanupAbandonedSessionsArgs) Kind() string { return "cleanup_abandoned_sessions" }

type CleanupAbandonedSessionsWorker struct {
	river.WorkerDefaults[CleanupAbandonedSessionsArgs]
	Pool *pgxpool.Pool
}

func (w *CleanupAbandonedSessionsWorker) Work(ctx context.Context, job *river.Job[CleanupAbandonedSessionsArgs]) error {
	q := db.New(w.Pool)

	ids, err := q.FindAbandonedSessions(ctx)
	if err != nil {
		return err
	}

	for _, id := range ids {
		if err := q.MarkSessionCompleted(ctx, id); err != nil {
			slog.Warn("failed to mark abandoned session completed", "session_id", id, "error", err)
			continue
		}
		// TODO: enqueue EvaluateSession for each abandoned session (Phase 7)
		slog.Info("marked abandoned session completed", "session_id", id)
	}

	return nil
}
```

- [ ] **Step 3: Register workers**

Update `internal/jobs/workers.go` to register both new workers. The cleanup job also needs periodic scheduling:

```go
func RegisterWorkers(cfg *config.Config, sender email.Sender, pool *pgxpool.Pool) *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, NewSendEmailWorker(&cfg.Email, sender))
	river.AddWorker(workers, &EvaluateSessionWorker{})
	river.AddWorker(workers, &CleanupAbandonedSessionsWorker{Pool: pool})
	return workers
}
```

**Note:** `RegisterWorkers` gains a `pool` parameter. Update the call site in `backend.go` to pass `pool`. Also, the periodic job scheduling for `CleanupAbandonedSessions` (every 3 min) is configured via River's `PeriodicJobs` config in `backend.go`'s `river.NewClient` call:

```go
PeriodicJobs: []*river.PeriodicJob{
	river.NewPeriodicJob(
		river.PeriodicInterval(3*time.Minute),
		func() (river.JobArgs, *river.InsertOpts) {
			return CleanupAbandonedSessionsArgs{}, nil
		},
		nil,
	),
},
```

- [ ] **Step 4: Update backend.go call site**

In `internal/backend/backend.go`, update the `RegisterWorkers` call to pass pool:

```go
workers := jobs.RegisterWorkers(cfg, emailSender, pool)
```

- [ ] **Step 5: Verify compilation and run existing tests**

Run: `go build ./... && go test ./internal/jobs/ -v`
Expected: PASS (existing SendEmail tests still pass, new workers compile)

- [ ] **Step 6: Commit**

```bash
git add internal/jobs/ internal/backend/backend.go
git commit -m "feat(conductor): add stub EvaluateSession + CleanupAbandonedSessions jobs"
```

---

## Task 12: Wire AI Deps into Backend

**Files:**
- Modify: `internal/backend/backend.go`

- [ ] **Step 1: Add AI fields and accessors to Backend**

Add to the `Backend` struct and update `New()`:

```go
import (
	"github.com/btc/drill/internal/ai"
)

type Backend struct {
	pool *pgxpool.Pool
	jobs Jobs
	cfg  *config.Config
	llm  *ai.Client
	stt  ai.Transcriber
	tts  ai.Synthesizer
}

// In New(), after creating the pool and before creating the River client:
llmClient := ai.NewClient(cfg.LLM.APIKey, pool)
stt := ai.NewOpenAITranscriber(cfg.Speech.OpenAIAPIKey, cfg.Speech.WhisperModel)
tts := ai.NewOpenAISynthesizer(cfg.Speech.OpenAIAPIKey, cfg.Speech.TTSModel, cfg.Speech.TTSVoice)

// And in the return:
return &Backend{
	pool: pool,
	jobs: riverClient,
	cfg:  cfg,
	llm:  llmClient,
	stt:  stt,
	tts:  tts,
}, nil
```

Add accessor methods:

```go
func (b *Backend) LLM() *ai.Client    { return b.llm }
func (b *Backend) STT() ai.Transcriber { return b.stt }
func (b *Backend) TTS() ai.Synthesizer { return b.tts }
```

- [ ] **Step 2: Verify compilation**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 3: Verify existing tests still pass**

Run: `go test ./internal/backend/ -v -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/backend/backend.go
git commit -m "feat(conductor): wire AI deps (LLM, STT, TTS) into Backend"
```

---

## Task 13: WSWriter Observer

**Files:**
- Modify: `internal/interview/observer.go`
- Modify: `internal/interview/observer_test.go`

- [ ] **Step 1: Write failing WSWriter tests**

Add to `internal/interview/observer_test.go`:

```go
func TestWSWriter_OnToken(t *testing.T) {
	ws := &mockWSConn{}
	writer := interview.NewWSWriter(ws, uuid.New())
	writer.OnToken("hello")
	require.Len(t, ws.sent, 1)
	assert.Contains(t, string(ws.sent[0]), `"type":"interviewer_token"`)
	assert.Contains(t, string(ws.sent[0]), `"token":"hello"`)
}

func TestWSWriter_OnDone(t *testing.T) {
	ws := &mockWSConn{}
	msgID := uuid.New()
	writer := interview.NewWSWriter(ws, msgID)
	writer.OnDone("full message")
	require.Len(t, ws.sent, 1)
	assert.Contains(t, string(ws.sent[0]), `"type":"interviewer_done"`)
	assert.Contains(t, string(ws.sent[0]), msgID.String())
}

func TestWSWriter_IgnoresWriteErrors(t *testing.T) {
	ws := &mockWSConn{err: fmt.Errorf("closed")}
	writer := interview.NewWSWriter(ws, uuid.New())
	// Should not panic
	writer.OnToken("hello")
	writer.OnDone("hello")
}

// mockWSConn captures sent messages for testing.
type mockWSConn struct {
	sent [][]byte
	err  error
}

func (m *mockWSConn) SendJSON(ctx context.Context, v any) error {
	if m.err != nil {
		return m.err
	}
	data, _ := json.Marshal(v)
	m.sent = append(m.sent, data)
	return nil
}

func (m *mockWSConn) Close(code websocket.StatusCode, reason string) error {
	return nil
}
```

- [ ] **Step 2: Run tests to verify failure**

Run: `go test ./internal/interview/ -v -run TestWSWriter`
Expected: FAIL

- [ ] **Step 3: Implement WSWriter**

Add to `internal/interview/observer.go`:

```go
// WSWriter sends interviewer tokens to the WebSocket client.
// Write errors are logged and ignored — the LLM stream must continue
// so MessageAccumulator can persist the full response.
type WSWriter struct {
	ws        WSConn
	messageID uuid.UUID
	ctx       context.Context
}

func NewWSWriter(ws WSConn, messageID uuid.UUID) *WSWriter {
	return &WSWriter{ws: ws, messageID: messageID, ctx: context.Background()}
}

func (w *WSWriter) OnToken(token string) {
	_ = w.ws.SendJSON(w.ctx, map[string]string{
		"type":  "interviewer_token",
		"token": token,
	})
}

func (w *WSWriter) OnDone(fullMessage string) {
	_ = w.ws.SendJSON(w.ctx, map[string]string{
		"type":       "interviewer_done",
		"message_id": w.messageID.String(),
	})
}

func (w *WSWriter) OnError(err error) {
	_ = w.ws.SendJSON(w.ctx, map[string]any{
		"type":    "error",
		"message": err.Error(),
	})
}

func (w *WSWriter) Interrupt() {} // no-op
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/interview/ -v -run TestWSWriter`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/interview/observer.go internal/interview/observer_test.go
git commit -m "feat(conductor): add WSWriter observer with error-tolerant writes"
```

---

## Task 14: Conductor Core + WebSocket Handler

**Files:**
- Create: `internal/interview/conductor.go`
- Create: `internal/handler/session_ws.go`
- Modify: `internal/handler/routes.go`

This is the largest task. The conductor wires together the state machine, observers, LLM client, and message persistence.

- [ ] **Step 1: Implement conductor**

Create `internal/interview/conductor.go`. This file implements:
- `Conductor` struct with all fields from the spec (Section 6)
- `NewConductor(...)` constructor
- `Run(ctx context.Context)` main loop
- `handleEndTurn` — voice and text paths
- `handleEndSession` — transactional status + eval job enqueue
- `handleDisconnect` — LLM stream continues to completion, persist, exit
- `handleShutdown` — persist partial state, send `reconnect_please`
- `streamInterviewerResponse` — creates observer fan-out, iterates TokenStream, calls observer methods
- Timer initialization from session start time

The full implementation is ~300-400 lines. Key patterns:

```go
type Conductor struct {
	sm       *StateMachine
	msgCh    chan WSMessage
	ws       WSConn
	pool     *pgxpool.Pool
	lockConn *pgxpool.Conn
	jobs     backend.Jobs
	llm      *ai.Client
	stt      ai.Transcriber
	tts      ai.Synthesizer
	observer *TokenFanOut

	sessionID uuid.UUID
	userID    uuid.UUID
	question  db.Question
	messages  []db.Message
	sequence  int
	ttsEnabled bool
	duration   time.Duration

	reconnectPending bool
	timerWarningCh   <-chan time.Time
	timerOvertimeCh  <-chan time.Time
	reconnectTimerCh <-chan time.Time
}
```

For `Run(ctx)`:
```go
func (c *Conductor) Run(ctx context.Context) {
	defer c.lockConn.Release()
	// 1. Load session state from DB
	// 2. Initialize timers
	// 3. Send session_loaded or reconnect_state
	// 4. If new session, stream interviewer opening
	// 5. Select loop
}
```

Each handler method follows the state machine transitions and persistence patterns described in the spec.

- [ ] **Step 2: Implement WebSocket handler**

Create `internal/handler/session_ws.go`:

```go
func SessionWS(b *backend.Backend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user := auth.UserFromContext(r.Context())
		sessionID, err := uuid.Parse(r.PathValue("id"))
		// ... validate session ownership and active status ...

		ws, err := websocket.Accept(w, r, nil)
		// ... set read limit ...

		// Acquire advisory lock
		lockConn, err := b.Pool().Acquire(r.Context())
		// ... pg_try_advisory_lock ...

		// Read session_init with 10s timeout
		// Build conductor
		// Launch conductor.Run(ctx) in goroutine
		// Run readLoop (blocking)
	}
}
```

```go
func readLoop(ctx context.Context, ws *websocket.Conn, msgCh chan<- interview.WSMessage, observer func() *interview.TokenFanOut) {
	defer close(msgCh)
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		msg, err := interview.ParseWSMessage(data)
		if err != nil {
			// send error, continue
			continue
		}
		if msg.Type == "cancel_tts" {
			if obs := observer(); obs != nil {
				obs.Interrupt()
			}
			continue
		}
		msgCh <- msg
	}
}
```

- [ ] **Step 3: Register WebSocket route**

Add to `internal/handler/routes.go`:

```go
	// WebSocket (auth via query param or cookie — RequireAuth works on the initial HTTP request)
	mux.Handle("GET /api/sessions/{id}/ws", requireAuth(http.HandlerFunc(SessionWS(b))))
```

- [ ] **Step 4: Verify compilation**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/interview/conductor.go internal/handler/session_ws.go internal/handler/routes.go
git commit -m "feat(conductor): add Conductor core and WebSocket handler"
```

---

## Task 15: WebSocket Integration Tests

**Files:**
- Create: `internal/interview/conductor_test.go`

This task implements all 13 WebSocket integration test cases from the spec. Each test:
1. Starts a Postgres testcontainer
2. Creates an `httptest.Server` with the real router
3. Connects via `websocket.Dial`
4. Sends/receives messages
5. Verifies DB state

- [ ] **Step 1: Write test helpers**

Create `internal/interview/conductor_test.go` with test infrastructure:

```go
package interview_test

// testServer sets up:
// - Postgres testcontainer with migrations
// - Mock STT (returns canned text)
// - Mock TTS (returns canned audio)
// - Fake Anthropic server (returns canned streaming tokens)
// - Real Backend + router + httptest.Server
// Returns the server URL, a cleanup func, and a DB pool for assertions.

type mockTranscriber struct {
	text string
	err  error
}
func (m *mockTranscriber) Transcribe(ctx context.Context, audio []byte, format string) (string, error) {
	return m.text, m.err
}

type mockSynthesizer struct {
	audio []byte
	err   error
}
func (m *mockSynthesizer) Synthesize(ctx context.Context, text string) (io.ReadCloser, error) {
	if m.err != nil {
		return nil, m.err
	}
	return io.NopCloser(bytes.NewReader(m.audio)), nil
}
```

- [ ] **Step 2: Implement test cases 1-4 (happy path, voice, cancel TTS, reconnection)**

Each test follows the pattern:
1. Create a user and session in DB
2. Connect WebSocket
3. Send `session_init`
4. Receive and verify server messages
5. Send client messages
6. Assert DB state

Test 1 (happy path text):
```go
func TestConductor_HappyPath_Text(t *testing.T) {
	// Create user + question + session
	// Connect WS, send session_init
	// Receive session_loaded
	// Receive interviewer_token events + interviewer_done
	// Send end_turn (text)
	// Receive interviewer_token events + interviewer_done
	// Send end_session
	// Receive session_ended
	// Assert: session status = completed, 3 messages (opening + candidate + response),
	//         llm_calls rows, eval job enqueued
}
```

- [ ] **Step 3: Implement test cases 5-8 (invalid transitions, malformed, ownership, inactive)**

- [ ] **Step 4: Implement test cases 9-13 (shutdown, timer, transactional, cleanup, reconnect timer)**

- [ ] **Step 5: Run all integration tests**

Run: `go test ./internal/interview/ -v -count=1 -timeout=5m`
Expected: PASS — all 13 test cases green

- [ ] **Step 6: Commit**

```bash
git add internal/interview/conductor_test.go
git commit -m "test(conductor): add 13 WebSocket integration test cases"
```

---

## Task 16: TTSAccumulator Observer

**Files:**
- Modify: `internal/interview/observer.go`
- Modify: `internal/interview/observer_test.go`

- [ ] **Step 1: Write failing TTSAccumulator tests**

Add tests for sentence boundary detection, tts_chunk sending, interrupt cancellation:

```go
func TestTTSAccumulator_SentenceBoundaries(t *testing.T) {
	ws := &mockWSConn{}
	synth := &mockSynthesizer{audio: []byte("fake-audio")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	acc := interview.NewTTSAccumulator(ctx, ws, synth, uuid.New())
	acc.OnToken("Hello there. ")
	acc.OnToken("How are you? ")
	acc.OnDone("Hello there. How are you? ")

	// Wait for TTS goroutine to finish
	acc.Wait()

	// Should have sent tts_chunk messages for each sentence
	var chunks int
	for _, msg := range ws.sent {
		if bytes.Contains(msg, []byte(`"type":"tts_chunk"`)) {
			chunks++
		}
	}
	assert.GreaterOrEqual(t, chunks, 2, "should have TTS chunks for each sentence")
}

func TestTTSAccumulator_Interrupt(t *testing.T) {
	ws := &mockWSConn{}
	// Slow synthesizer to ensure interrupt happens during processing
	synth := &slowMockSynthesizer{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	acc := interview.NewTTSAccumulator(ctx, ws, synth, uuid.New())
	acc.OnToken("Hello there. This is a long sentence. ")
	acc.Interrupt()
	acc.OnDone("Hello there. This is a long sentence. ")
	acc.Wait()
	// Should not hang — interrupt cancels in-flight TTS
}
```

- [ ] **Step 2: Implement TTSAccumulator**

Add to `internal/interview/observer.go`:

```go
type TTSAccumulator struct {
	ws        WSConn
	synth     ai.Synthesizer
	messageID uuid.UUID
	ctx       context.Context
	cancel    context.CancelFunc
	sentCh    chan string
	done      chan struct{}
	seq       int
}

func NewTTSAccumulator(ctx context.Context, ws WSConn, synth ai.Synthesizer, messageID uuid.UUID) *TTSAccumulator {
	ctx, cancel := context.WithCancel(ctx)
	acc := &TTSAccumulator{
		ws:        ws,
		synth:     synth,
		messageID: messageID,
		ctx:       ctx,
		cancel:    cancel,
		sentCh:    make(chan string, 32),
		done:      make(chan struct{}),
	}
	go acc.ttsLoop()
	return acc
}

func (a *TTSAccumulator) OnToken(token string) {
	// Buffer tokens, detect sentence boundaries, send to sentCh
}

func (a *TTSAccumulator) OnDone(fullMessage string) {
	// Flush remaining buffer to sentCh, close sentCh
}

func (a *TTSAccumulator) OnError(err error) { a.cancel() }
func (a *TTSAccumulator) Interrupt()         { a.cancel() }
func (a *TTSAccumulator) Wait()              { <-a.done }

func (a *TTSAccumulator) ttsLoop() {
	defer close(a.done)
	for sentence := range a.sentCh {
		if a.ctx.Err() != nil {
			return
		}
		reader, err := a.synth.Synthesize(a.ctx, sentence)
		if err != nil {
			continue // TTS failure is non-fatal
		}
		// Read chunks and send tts_chunk messages
		buf := make([]byte, 4096)
		for {
			n, err := reader.Read(buf)
			if n > 0 {
				a.seq++
				_ = a.ws.SendJSON(a.ctx, map[string]any{
					"type":       "tts_chunk",
					"data":       base64.StdEncoding.EncodeToString(buf[:n]),
					"message_id": a.messageID.String(),
					"seq":        a.seq,
				})
			}
			if err != nil {
				break
			}
		}
		reader.Close()
	}
	_ = a.ws.SendJSON(a.ctx, map[string]string{
		"type":       "tts_done",
		"message_id": a.messageID.String(),
	})
}
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/interview/ -v -run TestTTSAccumulator`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/interview/observer.go internal/interview/observer_test.go
git commit -m "feat(conductor): add TTSAccumulator observer with sentence boundary detection"
```

---

## Task 17: Final Verification

- [ ] **Step 1: Run all tests**

Run: `go test ./... -count=1 -timeout=10m`
Expected: ALL PASS

- [ ] **Step 2: Run go vet and staticcheck**

Run: `go vet ./... && staticcheck ./...`
Expected: No issues

- [ ] **Step 3: Verify sqlc is in sync**

Run: `sqlc generate && git diff --exit-code internal/db/`
Expected: No diff (generated code matches)

- [ ] **Step 4: Final commit if any cleanup needed**

```bash
git add -A
git commit -m "chore(conductor): final cleanup and verification"
```
