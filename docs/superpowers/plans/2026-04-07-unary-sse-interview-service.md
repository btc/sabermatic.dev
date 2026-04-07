# Unary SSE Interview Service Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the WebSocket conductor with stateless ConnectRPC RPCs for interview sessions.

**Architecture:** Four ConnectRPC RPCs (`SubmitTurn`, `GetSessionState`, `EndSession`, `CancelSession`) replace the WebSocket conductor event loop. Turn execution moves into a `Backend.ExecuteTurn` method. Concurrency control via a `generating` status column replaces advisory locks. TTS and token streaming share a single server-streaming response.

**Tech Stack:** Go, ConnectRPC, sqlc, pgx, buf, React/TypeScript, TanStack Query

**Spec:** `docs/superpowers/specs/2026-04-07-unary-sse-interview-service-design.md`

---

## File Structure

### New files

| File | Purpose |
|------|---------|
| `sql/migrations/009_generating_status.up.sql` | Add `generating_since` column, update status CHECK |
| `sql/migrations/009_generating_status.down.sql` | Revert migration |
| `sql/queries/interview.sql` | New queries for turn execution (acquire lock, load context, persist, complete, cancel, cleanup) |
| `pb/drill/v1/interview.proto` | Proto for `InterviewService` (SubmitTurn, GetSessionState, EndSession, CancelSession) |
| `internal/rpc/interview/server.go` | ConnectRPC handler — thin shim calling `Backend.ExecuteTurn` |
| `internal/rpc/interview/server_test.go` | Integration tests for the interview RPC handlers |
| `internal/rpc/interview/sink.go` | `connectTurnSink` adapter: `TurnEventSink` → `connect.ServerStream` |
| `internal/backend/turn.go` | `Backend.ExecuteTurn` — turn pipeline (transcribe, LLM stream, TTS, persist) |
| `internal/backend/turn_test.go` | Unit tests for ExecuteTurn |
| `internal/backend/turn_sink.go` | `TurnEventSink` interface definition |
| `internal/jobs/cleanup_generating.go` | River periodic job for stale `generating` sessions |
| `web/src/hooks/use-interview.ts` | New React hook using ConnectRPC generated client |

### Modified files

| File | Change |
|------|--------|
| `pb/drill/v1/session.proto` | Add `SESSION_STATUS_GENERATING = 8` to `SessionStatus` enum |
| `internal/rpc/register.go` | Register `InterviewService` |
| `internal/backend/session.go` | Update `CompleteSession`/`CancelSession` to derive turn count from DB |
| `internal/backend/backend.go` | Register cleanup job; add exported methods for STT/TTS/LLM that `ExecuteTurn` needs |
| `web/src/pages/interview.tsx` | Replace `useInterview` WS hook with new ConnectRPC hook |

### Deleted files

| File | Reason |
|------|--------|
| `internal/handler/session_ws.go` | WebSocket upgrade handler |
| `internal/handler/session_ws_test.go` | WebSocket handler tests (2,458 lines) |
| `internal/interview/conductor.go` | Event loop, read loop, reconnection |
| `internal/interview/conductor_unit_test.go` | Conductor unit tests |
| `internal/interview/state_machine.go` | Conductor state machine |
| `internal/interview/state_machine_test.go` | State machine tests |
| `internal/interview/transport/ws_client.go` | WebSocket JSON send layer |
| `internal/interview/transport/ws_client_test.go` | WS client tests |
| `internal/interview/ws_message.go` | WebSocket message parsing |
| `internal/interview/ws_message_test.go` | WS message tests |
| `internal/backend/session_lock.go` | Advisory lock |
| `internal/backend/session_lock_test.go` | Advisory lock tests |
| `web/src/ws/` | Entire directory (connection.ts, hooks.ts, protocol.ts, tests) |

---

## Task 1: Database Migration — Add `generating_since` Column

**Files:**
- Create: `sql/migrations/009_generating_status.up.sql`
- Create: `sql/migrations/009_generating_status.down.sql`

- [ ] **Step 1: Write up migration**

```sql
-- sql/migrations/009_generating_status.up.sql
ALTER TABLE interview_sessions ADD COLUMN generating_since TIMESTAMPTZ;

-- Update the status CHECK to include 'generating'.
ALTER TABLE interview_sessions DROP CONSTRAINT IF EXISTS interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
  CHECK (status IN ('active', 'completed', 'evaluating', 'reviewed', 'evaluation_failed', 'failed', 'cancelled', 'generating'));
```

- [ ] **Step 2: Write down migration**

```sql
-- sql/migrations/009_generating_status.down.sql
-- Reset any generating sessions to active before removing the status value.
UPDATE interview_sessions SET status = 'active', generating_since = NULL WHERE status = 'generating';

ALTER TABLE interview_sessions DROP CONSTRAINT IF EXISTS interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
  CHECK (status IN ('active', 'completed', 'evaluating', 'reviewed', 'evaluation_failed', 'failed', 'cancelled'));

ALTER TABLE interview_sessions DROP COLUMN generating_since;
```

- [ ] **Step 3: Run migration locally**

Run: `go run ./cmd/migrate up`
Expected: Migration 009 applied successfully.

- [ ] **Step 4: Commit**

```
git add sql/migrations/009_*
git commit -m "migrate: add generating_since column and generating status"
```

---

## Task 2: SQL Queries for Turn Execution

**Files:**
- Create: `sql/queries/interview.sql`
- Regenerate: `internal/db/` (via `sqlc generate`)

- [ ] **Step 1: Write new sqlc queries**

```sql
-- sql/queries/interview.sql

-- name: AcquireGeneratingStatus :one
-- Atomically set status to 'generating'. Returns the session ID if successful.
-- No row returned means the session is not active or already generating.
UPDATE interview_sessions
SET status = 'generating', generating_since = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'active'
RETURNING id;

-- name: ReleaseGeneratingStatus :exec
-- Revert status to 'active' after a turn completes or fails.
UPDATE interview_sessions
SET status = 'active', generating_since = NULL, updated_at = NOW()
WHERE id = $1 AND status = 'generating';

-- name: GetSessionForTurn :one
-- Load session + question + coach briefing in a single query for turn execution.
SELECT s.id, s.user_id, s.question_id, s.status,
       s.config_duration_minutes, s.config_tts_enabled,
       s.config_coach_briefing, s.started_at,
       q.title AS question_title, q.prompt AS question_prompt,
       q.difficulty AS question_difficulty, q.hints AS question_hints
FROM interview_sessions s
JOIN questions q ON q.id = s.question_id
WHERE s.id = $1;

-- name: GetMessagesBySessionOffset :many
-- Return messages after the given offset (for known_message_count cursor).
SELECT id, session_id, seq, role, content, input_method, audio_url, created_at
FROM messages
WHERE session_id = $1
ORDER BY seq
OFFSET $2;

-- name: CountInterviewerMessages :one
-- Count completed interviewer turns for turn_count derivation.
SELECT COUNT(*)::int AS count
FROM messages
WHERE session_id = $1 AND role = 'interviewer';

-- name: GetSessionStatus :one
-- Lightweight status check for EndSession/CancelSession polling.
SELECT status, generating_since
FROM interview_sessions
WHERE id = $1;

-- name: CleanupStaleGenerating :exec
-- Reset sessions stuck in 'generating' for too long (crash recovery).
UPDATE interview_sessions
SET status = 'active', generating_since = NULL, updated_at = NOW()
WHERE status = 'generating'
  AND generating_since < NOW() - INTERVAL '5 minutes';

-- name: InlineRecoverStaleGenerating :exec
-- Inline crash recovery for EndSession/CancelSession.
-- Conditional WHERE makes this idempotent and race-free.
UPDATE interview_sessions
SET status = 'active', generating_since = NULL, updated_at = NOW()
WHERE id = $1
  AND status = 'generating'
  AND generating_since < NOW() - INTERVAL '5 minutes';
```

- [ ] **Step 2: Run sqlc generate**

Run: `sqlc generate`
Expected: No errors. New Go methods in `internal/db/`.

- [ ] **Step 3: Verify generated code compiles**

Run: `go build ./internal/db/...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```
git add sql/queries/interview.sql internal/db/
git commit -m "feat: add sqlc queries for turn execution and crash recovery"
```

---

## Task 3: Proto Definition — InterviewService

**Files:**
- Create: `pb/drill/v1/interview.proto`
- Modify: `pb/drill/v1/session.proto` (add `SESSION_STATUS_GENERATING`)
- Regenerate: `internal/pb/` and `web/src/pb/` (via `buf generate`)

- [ ] **Step 1: Add GENERATING to SessionStatus enum**

In `pb/drill/v1/session.proto`, add after `SESSION_STATUS_CANCELLED = 7`:

```protobuf
  SESSION_STATUS_GENERATING = 8;
```

- [ ] **Step 2: Write interview.proto**

```protobuf
// pb/drill/v1/interview.proto
syntax = "proto3";
package drill.v1;

option go_package = "github.com/btc/drill/internal/pb/drill/v1;drillv1";

import "drill/v1/session.proto";
import "google/protobuf/timestamp.proto";

service InterviewService {
  // AIP-136: Custom method — submit a turn and stream the interviewer response.
  rpc SubmitTurn(SubmitTurnRequest) returns (stream TurnEvent);
  // AIP-131: Get current session state (messages, status, session info).
  rpc GetSessionState(GetSessionStateRequest) returns (GetSessionStateResponse);
  // AIP-136: Custom method — end the session (complete + evaluate).
  rpc EndSession(EndSessionRequest) returns (EndSessionResponse);
  // AIP-136: Custom method — cancel the session (archive + refund).
  rpc CancelSession(CancelSessionRequest) returns (CancelSessionResponse);
}

// --- SubmitTurn ---

message SubmitTurnRequest {
  string session_id = 1;
  string traceparent = 2;

  oneof input {
    TextInput text_input = 3;
    VoiceInput voice_input = 4;
  }
}

message TextInput {
  string content = 1;
}

message VoiceInput {
  bytes audio = 1;
  string audio_mime_type = 2;
}

message TurnEvent {
  oneof event {
    TranscriptionResult transcription_result = 1;
    InterviewerToken interviewer_token = 2;
    InterviewerDone interviewer_done = 3;
    TtsChunk tts_chunk = 4;
    TtsDone tts_done = 5;
    TurnError error = 6;
  }
}

message TranscriptionResult {
  string text = 1;
}

message InterviewerToken {
  string token = 1;
}

message InterviewerDone {
  string message_id = 1;
}

message TtsChunk {
  bytes data = 1;
  string message_id = 2;
  int32 seq = 3;
}

message TtsDone {
  string message_id = 1;
}

message TurnError {
  string code = 1;
  string message = 2;
}

// --- GetSessionState ---

message GetSessionStateRequest {
  string session_id = 1;
  int32 known_message_count = 2;
}

message GetSessionStateResponse {
  InterviewSessionInfo session_info = 1;
  repeated Message messages = 2;
  SessionStatus status = 3;
}

message InterviewSessionInfo {
  string session_id = 1;
  string question_title = 2;
  string question_prompt = 3;
  int32 duration_minutes = 4;
  bool tts_enabled = 5;
  google.protobuf.Timestamp start_time = 6;
}

// --- EndSession / CancelSession ---

message EndSessionRequest {
  string session_id = 1;
}

message EndSessionResponse {}

message CancelSessionRequest {
  string session_id = 1;
}

message CancelSessionResponse {}
```

Note: `Message` is already defined in `session.proto` — reuse it.

- [ ] **Step 3: Run buf generate**

Run: `buf generate`
Expected: Generated Go code in `internal/pb/drill/v1/` and TypeScript in `web/src/pb/drill/v1/`.

- [ ] **Step 4: Verify generated code compiles**

Run: `go build ./internal/pb/...`
Expected: Clean build.

- [ ] **Step 5: Commit**

```
git add pb/drill/v1/interview.proto pb/drill/v1/session.proto internal/pb/ web/src/pb/
git commit -m "proto: add InterviewService with SubmitTurn, GetSessionState, EndSession, CancelSession"
```

---

## Task 4: TurnEventSink Interface and ConnectRPC Adapter

**Files:**
- Create: `internal/backend/turn_sink.go`
- Create: `internal/rpc/interview/sink.go`

- [ ] **Step 1: Define TurnEventSink interface in backend**

```go
// internal/backend/turn_sink.go
package backend

import drillv1 "github.com/btc/drill/internal/pb/drill/v1"

// TurnEventSink receives events during turn execution and delivers them
// to the client. Implementations must silently drop writes that fail
// due to client disconnect — the pipeline must not abort on sink errors.
type TurnEventSink interface {
	TranscriptionResult(e *drillv1.TranscriptionResult)
	InterviewerToken(e *drillv1.InterviewerToken)
	InterviewerDone(e *drillv1.InterviewerDone)
	TtsChunk(e *drillv1.TtsChunk)
	TtsDone(e *drillv1.TtsDone)
	Error(e *drillv1.TurnError)
}
```

- [ ] **Step 2: Implement connectTurnSink adapter**

```go
// internal/rpc/interview/sink.go
package interview

import (
	"log/slog"

	"connectrpc.com/connect"

	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/backend"
)

var _ backend.TurnEventSink = (*connectTurnSink)(nil)

type connectTurnSink struct {
	stream *connect.ServerStream[drillv1.TurnEvent]
}

func (s *connectTurnSink) send(event *drillv1.TurnEvent) {
	if err := s.stream.Send(event); err != nil {
		slog.Debug("interview: sink send failed", "error", err)
	}
}

func (s *connectTurnSink) TranscriptionResult(e *drillv1.TranscriptionResult) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TranscriptionResult{TranscriptionResult: e}})
}

func (s *connectTurnSink) InterviewerToken(e *drillv1.InterviewerToken) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_InterviewerToken{InterviewerToken: e}})
}

func (s *connectTurnSink) InterviewerDone(e *drillv1.InterviewerDone) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_InterviewerDone{InterviewerDone: e}})
}

func (s *connectTurnSink) TtsChunk(e *drillv1.TtsChunk) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TtsChunk{TtsChunk: e}})
}

func (s *connectTurnSink) TtsDone(e *drillv1.TtsDone) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TtsDone{TtsDone: e}})
}

func (s *connectTurnSink) Error(e *drillv1.TurnError) {
	s.send(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_Error{Error: e}})
}
```

- [ ] **Step 3: Verify compilation**

Run: `go build ./internal/backend/... ./internal/rpc/interview/...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```
git add internal/backend/turn_sink.go internal/rpc/interview/sink.go
git commit -m "feat: add TurnEventSink interface and ConnectRPC adapter"
```

---

## Task 5: Backend.ExecuteTurn — Core Turn Pipeline

This is the largest task. It absorbs the turn execution logic from `conductor.go` into a backend method.

**Files:**
- Create: `internal/backend/turn.go`
- Create: `internal/backend/turn_test.go`

- [ ] **Step 1: Write the failing test for a text turn**

```go
// internal/backend/turn_test.go
package backend_test

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/backend"
	"github.com/btc/drill/internal/backendtest"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
)

// recordingSink captures all events emitted during a turn for assertions.
type recordingSink struct {
	mu     sync.Mutex
	events []*drillv1.TurnEvent
}

func (s *recordingSink) record(e *drillv1.TurnEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *recordingSink) TranscriptionResult(e *drillv1.TranscriptionResult) {
	s.record(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TranscriptionResult{TranscriptionResult: e}})
}
func (s *recordingSink) InterviewerToken(e *drillv1.InterviewerToken) {
	s.record(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_InterviewerToken{InterviewerToken: e}})
}
func (s *recordingSink) InterviewerDone(e *drillv1.InterviewerDone) {
	s.record(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_InterviewerDone{InterviewerDone: e}})
}
func (s *recordingSink) TtsChunk(e *drillv1.TtsChunk) {
	s.record(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TtsChunk{TtsChunk: e}})
}
func (s *recordingSink) TtsDone(e *drillv1.TtsDone) {
	s.record(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_TtsDone{TtsDone: e}})
}
func (s *recordingSink) Error(e *drillv1.TurnError) {
	s.record(&drillv1.TurnEvent{Event: &drillv1.TurnEvent_Error{Error: e}})
}

func (s *recordingSink) Events() []*drillv1.TurnEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*drillv1.TurnEvent(nil), s.events...)
}

func TestExecuteTurn_OpeningQuestion(t *testing.T) {
	b := backendtest.NewBackend(t)
	user := backendtest.SeedUser(t, b)
	sess := backendtest.SeedSession(t, b, user.ID)

	sink := &recordingSink{}
	err := b.ExecuteTurn(context.Background(), &drillv1.SubmitTurnRequest{
		SessionId: sess.ID.String(),
		Input:     &drillv1.SubmitTurnRequest_TextInput{TextInput: &drillv1.TextInput{Content: ""}},
	}, sink)
	require.NoError(t, err)

	events := sink.Events()
	// Must have at least one token and an interviewer_done.
	var hasToken, hasDone bool
	for _, e := range events {
		switch e.Event.(type) {
		case *drillv1.TurnEvent_InterviewerToken:
			hasToken = true
		case *drillv1.TurnEvent_InterviewerDone:
			hasDone = true
		}
	}
	assert.True(t, hasToken, "expected at least one interviewer token")
	assert.True(t, hasDone, "expected interviewer_done event")

	// Session status should be back to active.
	status, err := b.GetSessionStatus(context.Background(), sess.ID)
	require.NoError(t, err)
	assert.Equal(t, "active", status)
}

func TestExecuteTurn_TextTurn(t *testing.T) {
	b := backendtest.NewBackend(t)
	user := backendtest.SeedUser(t, b)
	sess := backendtest.SeedSession(t, b, user.ID)

	// First, run opening question.
	err := b.ExecuteTurn(context.Background(), &drillv1.SubmitTurnRequest{
		SessionId: sess.ID.String(),
		Input:     &drillv1.SubmitTurnRequest_TextInput{TextInput: &drillv1.TextInput{Content: ""}},
	}, &recordingSink{})
	require.NoError(t, err)

	// Now submit a real text turn.
	sink := &recordingSink{}
	err = b.ExecuteTurn(context.Background(), &drillv1.SubmitTurnRequest{
		SessionId: sess.ID.String(),
		Input:     &drillv1.SubmitTurnRequest_TextInput{TextInput: &drillv1.TextInput{Content: "The time complexity is O(n log n)."}},
	}, sink)
	require.NoError(t, err)

	events := sink.Events()
	var hasToken, hasDone bool
	for _, e := range events {
		switch e.Event.(type) {
		case *drillv1.TurnEvent_InterviewerToken:
			hasToken = true
		case *drillv1.TurnEvent_InterviewerDone:
			hasDone = true
		}
	}
	assert.True(t, hasToken)
	assert.True(t, hasDone)
}

func TestExecuteTurn_RejectsConcurrent(t *testing.T) {
	b := backendtest.NewBackend(t)
	user := backendtest.SeedUser(t, b)
	sess := backendtest.SeedSession(t, b, user.ID)

	// Manually set status to generating to simulate in-flight turn.
	_, err := b.AcquireGeneratingStatus(context.Background(), sess.ID)
	require.NoError(t, err)

	// Second turn should fail.
	sink := &recordingSink{}
	err = b.ExecuteTurn(context.Background(), &drillv1.SubmitTurnRequest{
		SessionId: sess.ID.String(),
		Input:     &drillv1.SubmitTurnRequest_TextInput{TextInput: &drillv1.TextInput{Content: "hello"}},
	}, sink)
	require.Error(t, err)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/backend/ -run TestExecuteTurn -v`
Expected: FAIL — `ExecuteTurn` method not defined.

- [ ] **Step 3: Implement ExecuteTurn**

Create `internal/backend/turn.go`. This absorbs logic from `conductor.go` lines 430-634. Key differences from conductor:
- No state machine — status column replaces it.
- No accumulated `c.messages` — reload from DB each turn.
- No `c.sequence` — derive from `GetMaxSeqForSession`.
- Uses `context.Background()` for DB persist to survive client disconnect.
- Sink methods use proto types.

```go
// internal/backend/turn.go
package backend

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/codes"

	"github.com/btc/drill/internal/ai"
	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
	"github.com/btc/drill/internal/interview/observer"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
)

// ExecuteTurn runs one turn of an interview session: acquire generating lock,
// optionally transcribe, persist candidate message, stream LLM response with
// TTS fan-out, persist interviewer message, release lock.
//
// The pipeline runs critical DB operations on context.Background() so that
// client disconnects do not leave partial state. Sink write failures are
// silently dropped — only LLM/STT/TTS/DB errors abort the pipeline.
func (b *Backend) ExecuteTurn(ctx context.Context, p *drillv1.SubmitTurnRequest, sink TurnEventSink) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.ExecuteTurn")
	defer func() { drilotel.End(span, err) }()

	sessionID, err := uuid.Parse(p.SessionId)
	if err != nil {
		return fmt.Errorf("parse session_id: %w", err)
	}

	// Step 1: Acquire generating status.
	acquired, err := b.AcquireGeneratingStatus(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("acquire generating status: %w", err)
	}
	if !acquired {
		return fmt.Errorf("session not active or already generating")
	}

	// Ensure status reverts to active on any exit path.
	defer func() {
		if releaseErr := b.ReleaseGeneratingStatus(context.Background(), sessionID); releaseErr != nil {
			slog.Error("turn: failed to release generating status",
				"error", releaseErr, "session_id", sessionID)
		}
	}()

	// Step 2: Load session context.
	row, err := b.queries().GetSessionForTurn(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	messages, err := b.queries().GetMessagesBySession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load messages: %w", err)
	}

	maxSeq, err := b.queries().GetMaxSeqForSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("get max seq: %w", err)
	}
	seq := maxSeq

	// Load coach briefing if enabled.
	var coachBriefing *db.CoachAnalysis
	if row.ConfigCoachBriefing {
		ca, caErr := b.GetLatestCoachAnalysis(ctx, row.UserID)
		if caErr == nil {
			coachBriefing = &ca
		}
	}

	// Step 3: Determine turn type.
	isOpeningQuestion := len(messages) == 0 && isEmptyTextInput(p)
	isCrashRecovery := len(messages) > 0 && messages[len(messages)-1].Role == "candidate"

	if !isOpeningQuestion && !isCrashRecovery {
		// Normal turn — process input.
		var candidateContent string
		messageID := uuid.New()

		switch input := p.Input.(type) {
		case *drillv1.SubmitTurnRequest_VoiceInput:
			voice := input.VoiceInput
			if len(voice.Audio) == 0 {
				return fmt.Errorf("no audio data provided")
			}
			if err := validateAudioMime(voice.AudioMimeType); err != nil {
				return err
			}

			// Fire audio upload goroutine (does not block pipeline).
			go b.uploadAudio(ctx, sessionID, messageID, voice.Audio, voice.AudioMimeType)

			// STT with single retry.
			ext := audioExt(voice.AudioMimeType)
			text, sttErr := b.Transcribe(ctx, voice.Audio, ext)
			if sttErr != nil {
				slog.Warn("turn: transcription failed, retrying",
					"error", sttErr, "session_id", sessionID)
				time.Sleep(500 * time.Millisecond)
				text, sttErr = b.Transcribe(ctx, voice.Audio, ext)
			}
			if sttErr != nil {
				return fmt.Errorf("transcription: %w", sttErr)
			}
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("transcription returned empty text")
			}

			sink.TranscriptionResult(&drillv1.TranscriptionResult{Text: text})
			candidateContent = text

		case *drillv1.SubmitTurnRequest_TextInput:
			if strings.TrimSpace(input.TextInput.Content) == "" {
				return fmt.Errorf("text content cannot be empty")
			}
			candidateContent = input.TextInput.Content

		default:
			return fmt.Errorf("no input provided")
		}

		// Persist candidate message.
		seq++
		inputMethod := "text"
		if _, ok := p.Input.(*drillv1.SubmitTurnRequest_VoiceInput); ok {
			inputMethod = "voice"
		}

		candidateMsg, persistErr := b.PersistMessage(context.Background(), PersistMessageParams{
			MessageID:   messageID,
			SessionID:   sessionID,
			Seq:         seq,
			Role:        "candidate",
			Content:     candidateContent,
			InputMethod: inputMethod,
		})
		if persistErr != nil {
			return fmt.Errorf("persist candidate message: %w", persistErr)
		}
		messages = append(messages, candidateMsg)
	}

	// Step 6: Stream interviewer response.
	return b.streamInterviewerResponse(ctx, streamParams{
		sessionID:     sessionID,
		userID:        row.UserID,
		question:      questionFromRow(row),
		coachBriefing: coachBriefing,
		messages:      messages,
		seq:           seq,
		ttsEnabled:    row.ConfigTtsEnabled,
		duration:      time.Duration(row.ConfigDurationMinutes) * time.Minute,
		startedAt:     row.StartedAt.Time,
		sink:          sink,
	})
}

type streamParams struct {
	sessionID     uuid.UUID
	userID        uuid.UUID
	question      db.Question
	coachBriefing *db.CoachAnalysis
	messages      []db.Message
	seq           int
	ttsEnabled    bool
	duration      time.Duration
	startedAt     time.Time
	sink          TurnEventSink
}

func (b *Backend) streamInterviewerResponse(ctx context.Context, p streamParams) (err error) {
	ctx, span := tracer.Start(ctx, "Backend.streamInterviewerResponse")
	defer func() { drilotel.End(span, err) }()

	messageID := uuid.New()

	// Build prompt.
	elapsed := time.Since(p.startedAt)
	remaining := max(0, p.duration-elapsed)

	system, promptMsgs := NewInterviewerPrompt().
		WithSystemInstructions().
		WithQuestion(p.question).
		WithCoachBriefing(p.coachBriefing).
		WithTimeContext(elapsed, remaining).
		WithTranscript(p.messages).
		Build()

	// Create observer fan-out.
	accumulator := observer.NewMessageAccumulator()
	tokenSink := &sinkTokenWriter{sink: p.sink, messageID: messageID}
	observers := []observer.TokenObserver{tokenSink, accumulator}

	if p.ttsEnabled {
		synth, synthErr := b.Synthesizer()
		if synthErr == nil && synth != nil {
			ttsSinkAdapter := &sinkTTSAdapter{sink: p.sink, messageID: messageID}
			observers = append(observers, observer.NewTTSAccumulator(observer.TTSAccumulatorParams{
				Sink:            ttsSinkAdapter,
				Synth:           synth,
				SentenceTimeout: b.cfg.Speech.TTSSentenceTimeout,
			}))
		}
	}
	fanOut := observer.NewTokenFanOut(observers...)

	// Stream LLM.
	stream, llmErr := b.StreamLLM(ctx, ai.StreamParams{
		Model:     b.cfg.LLM.InterviewerModel,
		System:    system,
		Messages:  promptMsgs,
		UserID:    p.userID,
		Role:      "interviewer",
		SessionID: p.sessionID,
	})
	if llmErr != nil {
		fanOut.OnError(llmErr)
		return fmt.Errorf("start llm stream: %w", llmErr)
	}

	for {
		token, tokenErr := stream.Next()
		if tokenErr == io.EOF {
			break
		}
		if tokenErr != nil {
			fanOut.OnError(tokenErr)
			slog.Error("turn: stream token error", "error", tokenErr, "session_id", p.sessionID)
			break
		}
		fanOut.OnToken(token)
	}

	fullText := accumulator.Text()
	fanOut.OnDone(fullText)

	go fanOut.Close()

	// Persist interviewer message.
	seq := p.seq + 1
	_, persistErr := b.PersistInterviewerTurn(context.Background(), stream, PersistMessageParams{
		MessageID:   messageID,
		SessionID:   p.sessionID,
		Seq:         seq,
		Role:        "interviewer",
		Content:     fullText,
		InputMethod: "",
	})
	if persistErr != nil {
		return fmt.Errorf("persist interviewer turn: %w", persistErr)
	}

	p.sink.InterviewerDone(&drillv1.InterviewerDone{MessageId: messageID.String()})
	return nil
}

// --- Sink adapters for observer interfaces ---

// sinkTokenWriter adapts TurnEventSink to observer.TokenObserver.
type sinkTokenWriter struct {
	sink      TurnEventSink
	messageID uuid.UUID
}

func (w *sinkTokenWriter) OnToken(token string) {
	w.sink.InterviewerToken(&drillv1.InterviewerToken{Token: token})
}

func (w *sinkTokenWriter) OnDone(_ string) {}

func (w *sinkTokenWriter) OnError(err error) {
	w.sink.Error(&drillv1.TurnError{Code: "llm_stream_error", Message: err.Error()})
}

// sinkTTSAdapter adapts TurnEventSink to observer.TTSSink.
type sinkTTSAdapter struct {
	sink      TurnEventSink
	messageID uuid.UUID
	seq       int
}

func (s *sinkTTSAdapter) HandleAudio(data []byte) {
	s.sink.TtsChunk(&drillv1.TtsChunk{
		Data:      data,
		MessageId: s.messageID.String(),
		Seq:       int32(s.seq),
	})
	s.seq++
}

func (s *sinkTTSAdapter) HandleTTSDone() {
	s.sink.TtsDone(&drillv1.TtsDone{MessageId: s.messageID.String()})
}

func (s *sinkTTSAdapter) HandleTTSError() {
	s.sink.Error(&drillv1.TurnError{Code: "tts_failed", Message: "text-to-speech synthesis failed"})
}

// --- Helpers ---

func isEmptyTextInput(p *drillv1.SubmitTurnRequest) bool {
	ti, ok := p.Input.(*drillv1.SubmitTurnRequest_TextInput)
	return ok && strings.TrimSpace(ti.TextInput.Content) == ""
}

func validateAudioMime(mime string) error {
	switch mime {
	case "audio/webm", "audio/mp4", "audio/ogg", "audio/wav", "audio/mpeg":
		return nil
	default:
		return fmt.Errorf("unsupported audio MIME type: %s", mime)
	}
}

func audioExt(mime string) string {
	switch mime {
	case "audio/webm":
		return "webm"
	case "audio/mp4":
		return "mp4"
	case "audio/ogg":
		return "ogg"
	case "audio/wav":
		return "wav"
	case "audio/mpeg":
		return "mp3"
	default:
		return "bin"
	}
}

func (b *Backend) uploadAudio(ctx context.Context, sessionID, messageID uuid.UUID, audio []byte, mime string) {
	uploadCtx, span := tracer.Start(context.WithoutCancel(ctx), "Backend.uploadAudio")
	defer span.End()

	ext := audioExt(mime)
	key := fmt.Sprintf("%s/%s.%s", sessionID, messageID, ext)
	url, err := b.StoreAudio(uploadCtx, key, audio, mime)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "audio upload failed")
		slog.Error("turn: audio upload failed", "error", err, "session_id", sessionID, "message_id", messageID)
		return
	}
	if setErr := b.SetAudioURL(uploadCtx, messageID, url); setErr != nil {
		span.RecordError(setErr)
		slog.Error("turn: failed to set audio_url", "error", setErr, "session_id", sessionID, "message_id", messageID)
	}
}

func questionFromRow(row db.GetSessionForTurnRow) db.Question {
	return db.Question{
		Title:      row.QuestionTitle,
		Prompt:     row.QuestionPrompt,
		Difficulty: row.QuestionDifficulty,
		Hints:      row.QuestionHints,
	}
}
```

Note: `b.queries()`, `b.AcquireGeneratingStatus()`, `b.ReleaseGeneratingStatus()`, `b.GetSessionStatus()`, and `b.GetLatestCoachAnalysis()` are small wrapper methods around sqlc-generated code. Add them to `backend.go` or `session.go` as thin wrappers. The exact signatures will depend on the sqlc-generated types — check `internal/db/` after sqlc generation and adapt. `NewInterviewerPrompt` is the existing prompt builder in `internal/interview/prompt.go` — import it.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/backend/ -run TestExecuteTurn -v`
Expected: PASS (may need to adjust based on exact backendtest helpers and sqlc types).

- [ ] **Step 5: Commit**

```
git add internal/backend/turn.go internal/backend/turn_test.go
git commit -m "feat: implement Backend.ExecuteTurn — stateless turn pipeline"
```

---

## Task 6: ConnectRPC Interview Server — All Four RPCs

**Files:**
- Create: `internal/rpc/interview/server.go`
- Modify: `internal/rpc/register.go`

- [ ] **Step 1: Implement the server**

```go
// internal/rpc/interview/server.go
package interview

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/btc/drill/internal/auth"
	"github.com/btc/drill/internal/backend"
	drillv1 "github.com/btc/drill/internal/pb/drill/v1"
	"github.com/btc/drill/internal/pb/drill/v1/drillv1connect"
)

var _ drillv1connect.InterviewServiceHandler = (*Server)(nil)

type Server struct {
	b *backend.Backend
}

func NewServer(b *backend.Backend) *Server {
	return &Server{b: b}
}

func (s *Server) SubmitTurn(
	ctx context.Context,
	req *connect.Request[drillv1.SubmitTurnRequest],
	stream *connect.ServerStream[drillv1.TurnEvent],
) error {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid session_id"))
	}

	// Verify ownership.
	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return connect.NewError(connect.CodePermissionDenied, fmt.Errorf("session not found"))
	}

	sink := &connectTurnSink{stream: stream}
	if err := s.b.ExecuteTurn(ctx, req.Msg, sink); err != nil {
		slog.Error("interview: submit_turn failed", "error", err, "session_id", sessionID)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("turn execution failed: %w", err))
	}
	return nil
}

func (s *Server) GetSessionState(
	ctx context.Context,
	req *connect.Request[drillv1.GetSessionStateRequest],
) (*connect.Response[drillv1.GetSessionStateResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid session_id"))
	}

	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("session not found"))
	}

	resp, err := s.b.GetSessionState(ctx, sessionID, int(req.Msg.KnownMessageCount))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(resp), nil
}

func (s *Server) EndSession(
	ctx context.Context,
	req *connect.Request[drillv1.EndSessionRequest],
) (*connect.Response[drillv1.EndSessionResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid session_id"))
	}

	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("session not found"))
	}

	if err := s.b.WaitAndCompleteSession(ctx, sessionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&drillv1.EndSessionResponse{}), nil
}

func (s *Server) CancelSession(
	ctx context.Context,
	req *connect.Request[drillv1.CancelSessionRequest],
) (*connect.Response[drillv1.CancelSessionResponse], error) {
	user := auth.UserFromContext(ctx)
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, fmt.Errorf("authentication required"))
	}

	sessionID, err := uuid.Parse(req.Msg.SessionId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid session_id"))
	}

	if err := s.b.VerifySessionOwnership(ctx, sessionID, user.ID); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, fmt.Errorf("session not found"))
	}

	if err := s.b.WaitAndCancelSession(ctx, sessionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&drillv1.CancelSessionResponse{}), nil
}
```

- [ ] **Step 2: Implement backend helper methods**

Add to `internal/backend/session.go`:

```go
// WaitAndCompleteSession polls until the session is not generating, then completes it.
func (b *Backend) WaitAndCompleteSession(ctx context.Context, sessionID uuid.UUID) error {
	if err := b.waitForGeneration(ctx, sessionID); err != nil {
		return err
	}
	turnCount, err := b.queries().CountInterviewerMessages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("count turns: %w", err)
	}
	return b.CompleteSession(ctx, sessionID, turnCount)
}

// WaitAndCancelSession polls until the session is not generating, then cancels it.
func (b *Backend) WaitAndCancelSession(ctx context.Context, sessionID uuid.UUID) error {
	if err := b.waitForGeneration(ctx, sessionID); err != nil {
		return err
	}
	turnCount, err := b.queries().CountInterviewerMessages(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("count turns: %w", err)
	}
	return b.CancelSession(ctx, sessionID, turnCount)
}

// waitForGeneration polls status every 500ms until the session is not generating.
// If generating_since exceeds 5 minutes, performs inline crash recovery.
// Times out after 60 seconds.
func (b *Backend) waitForGeneration(ctx context.Context, sessionID uuid.UUID) error {
	deadline := time.After(60 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		row, err := b.queries().GetSessionStatus(ctx, sessionID)
		if err != nil {
			return fmt.Errorf("check status: %w", err)
		}
		if row.Status != "generating" {
			return nil
		}

		// Inline crash recovery if stuck too long.
		if row.GeneratingSince.Valid {
			if time.Since(row.GeneratingSince.Time) > 5*time.Minute {
				_ = b.queries().InlineRecoverStaleGenerating(ctx, sessionID)
				continue
			}
		}

		select {
		case <-ticker.C:
			continue
		case <-deadline:
			return fmt.Errorf("timeout waiting for generation to complete")
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
```

- [ ] **Step 3: Register InterviewService in register.go**

Add to `internal/rpc/register.go`:

```go
import interviewsvc "github.com/btc/drill/internal/rpc/interview"
```

Add to `ConnectPathPrefixes`:
```go
drillv1connect.InterviewServiceName,
```

Add to `Register` function:
```go
mux.Handle(drillv1connect.NewInterviewServiceHandler(interviewsvc.NewServer(b), opts))
```

- [ ] **Step 4: Implement GetSessionState backend method**

Add to `internal/backend/session.go`:

```go
func (b *Backend) GetSessionState(ctx context.Context, sessionID uuid.UUID, knownCount int) (*drillv1.GetSessionStateResponse, error) {
	row, err := b.queries().GetSessionForTurn(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load session: %w", err)
	}

	var messages []db.Message
	if knownCount > 0 {
		messages, err = b.queries().GetMessagesBySessionOffset(ctx, db.GetMessagesBySessionOffsetParams{
			SessionID: sessionID,
			Offset:    int32(knownCount),
		})
	} else {
		messages, err = b.queries().GetMessagesBySession(ctx, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("load messages: %w", err)
	}

	return &drillv1.GetSessionStateResponse{
		SessionInfo: &drillv1.InterviewSessionInfo{
			SessionId:       sessionID.String(),
			QuestionTitle:   row.QuestionTitle,
			QuestionPrompt:  row.QuestionPrompt,
			DurationMinutes: row.ConfigDurationMinutes,
			TtsEnabled:      row.ConfigTtsEnabled,
			StartTime:       timestamppb.New(row.StartedAt.Time),
		},
		Messages: messagesToProto(messages),
		Status:   statusToProto(row.Status),
	}, nil
}
```

Note: `messagesToProto` and `statusToProto` are conversion helpers — check if they already exist in the session RPC package or db-to-proto converters. If not, write them following existing codebase patterns.

- [ ] **Step 5: Verify compilation**

Run: `go build ./...`
Expected: Clean build.

- [ ] **Step 6: Commit**

```
git add internal/rpc/interview/server.go internal/rpc/register.go internal/backend/session.go
git commit -m "feat: add InterviewService ConnectRPC handlers"
```

---

## Task 7: Stale Generating Cleanup Job

**Files:**
- Create: `internal/jobs/cleanup_generating.go`
- Modify: `internal/backend/backend.go` (register periodic job)

- [ ] **Step 1: Write the job worker**

```go
// internal/jobs/cleanup_generating.go
package jobs

import (
	"context"
	"log/slog"

	"github.com/riverqueue/river"

	"github.com/btc/drill/internal/db"
)

type CleanupStaleGeneratingArgs struct{}

func (CleanupStaleGeneratingArgs) Kind() string { return "cleanup_stale_generating" }

type CleanupStaleGeneratingWorker struct {
	river.WorkerDefaults[CleanupStaleGeneratingArgs]
	queries *db.Queries
}

func (w *CleanupStaleGeneratingWorker) Work(ctx context.Context, _ *river.Job[CleanupStaleGeneratingArgs]) error {
	if err := w.queries.CleanupStaleGenerating(ctx); err != nil {
		slog.Error("cleanup_stale_generating: failed", "error", err)
		return err
	}
	return nil
}
```

- [ ] **Step 2: Register in backend.go**

Add periodic job registration alongside existing periodic jobs. Follow the existing pattern for `CleanupAbandonedSessionsWorker`. Register with a 60-second interval.

- [ ] **Step 3: Verify compilation**

Run: `go build ./...`
Expected: Clean build.

- [ ] **Step 4: Commit**

```
git add internal/jobs/cleanup_generating.go internal/backend/backend.go
git commit -m "feat: add periodic cleanup job for stale generating sessions"
```

---

## Task 8: Frontend — New useInterview Hook

**Files:**
- Create: `web/src/hooks/use-interview.ts`
- Modify: `web/src/pages/interview.tsx`

- [ ] **Step 1: Write the new useInterview hook**

This replaces `web/src/ws/hooks.ts`. Uses ConnectRPC generated client for `SubmitTurn` (server-streaming) and unary RPCs for `GetSessionState`, `EndSession`, `CancelSession`.

```typescript
// web/src/hooks/use-interview.ts
import { useState, useRef, useCallback, useEffect } from "react";
import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { InterviewService } from "@/pb/drill/v1/interview_connect";
import type {
  TurnEvent,
  GetSessionStateResponse,
  InterviewSessionInfo,
} from "@/pb/drill/v1/interview_pb";
import { SessionStatus } from "@/pb/drill/v1/session_pb";

// Check the exact import paths after buf generate — they may differ
// based on the generated file names. Adjust accordingly.

interface InterviewMessage {
  id: string;
  role: "interviewer" | "candidate";
  content: string;
  seq: number;
}

type InterviewState =
  | "loading"
  | "ready"
  | "waiting"
  | "transcribing"
  | "processing"
  | "streaming"
  | "ended"
  | "cancelled"
  | "generating"; // mid-turn page refresh

interface UseInterviewReturn {
  messages: InterviewMessage[];
  streamingText: string;
  state: InterviewState;
  sessionInfo: InterviewSessionInfo | null;
  lastError: string | null;
  sendText: (content: string) => void;
  sendAudio: (audio: Uint8Array, mimeType: string) => void;
  endSession: () => void;
  cancelSession: () => void;
  onTtsChunk: ((data: Uint8Array, seq: number) => void) | null;
  setOnTtsChunk: (handler: ((data: Uint8Array, seq: number) => void) | null) => void;
  onTtsDone: (() => void) | null;
  setOnTtsDone: (handler: (() => void) | null) => void;
}

// JSON wire format for debuggability in browser devtools.
const transport = createConnectTransport({ baseUrl: "", useBinaryFormat: false });
const client = createClient(InterviewService, transport);

const POLL_DELAYS = [300, 600, 1200, 2000]; // ms, then cap at 2000

export function useInterview(sessionId: string): UseInterviewReturn {
  const [messages, setMessages] = useState<InterviewMessage[]>([]);
  const [streamingText, setStreamingText] = useState("");
  const [state, setState] = useState<InterviewState>("loading");
  const [sessionInfo, setSessionInfo] = useState<InterviewSessionInfo | null>(null);
  const [lastError, setLastError] = useState<string | null>(null);

  const streamingTextRef = useRef("");
  const onTtsChunkRef = useRef<((data: Uint8Array, seq: number) => void) | null>(null);
  const onTtsDoneRef = useRef<(() => void) | null>(null);
  const abortRef = useRef<AbortController | null>(null);

  // Load initial state.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const resp = await client.getSessionState({ sessionId, knownMessageCount: 0 });
        if (cancelled) return;
        setSessionInfo(resp.sessionInfo ?? null);
        setMessages(resp.messages.map((m) => ({
          id: m.id,
          role: m.role as "interviewer" | "candidate",
          content: m.content,
          seq: m.seq,
        })));

        if (resp.status === SessionStatus.GENERATING) {
          setState("generating");
          pollUntilActive(resp.messages.length);
        } else if (resp.status === SessionStatus.COMPLETED) {
          setState("ended");
        } else if (resp.status === SessionStatus.CANCELLED) {
          setState("cancelled");
        } else {
          setState(resp.messages.length === 0 ? "ready" : "waiting");
        }
      } catch (err) {
        if (!cancelled) setLastError(String(err));
      }
    })();
    return () => { cancelled = true; };
  }, [sessionId]);

  // Poll for mid-generation catch-up.
  const pollUntilActive = useCallback(async (knownCount: number) => {
    let attempt = 0;
    while (true) {
      const delay = POLL_DELAYS[Math.min(attempt, POLL_DELAYS.length - 1)];
      await new Promise((r) => setTimeout(r, delay));
      attempt++;

      try {
        const resp = await client.getSessionState({ sessionId, knownMessageCount: knownCount });
        if (resp.status !== SessionStatus.GENERATING) {
          setMessages((prev) => [
            ...prev,
            ...resp.messages.map((m) => ({
              id: m.id,
              role: m.role as "interviewer" | "candidate",
              content: m.content,
              seq: m.seq,
            })),
          ]);
          setState("waiting");
          return;
        }
      } catch {
        // Retry on error.
      }
    }
  }, [sessionId]);

  // Submit a turn (text or voice).
  const submitTurn = useCallback(async (req: Parameters<typeof client.submitTurn>[0]) => {
    streamingTextRef.current = "";
    setStreamingText("");
    setLastError(null);

    const abort = new AbortController();
    abortRef.current = abort;

    try {
      for await (const event of client.submitTurn(req, { signal: abort.signal })) {
        const e = event.event;
        if (!e) continue;

        switch (e.case) {
          case "transcriptionResult":
            setState("processing");
            setMessages((prev) => [
              ...prev,
              { id: crypto.randomUUID(), role: "candidate", content: e.value.text, seq: prev.length + 1 },
            ]);
            break;

          case "interviewerToken":
            setState("streaming");
            streamingTextRef.current += e.value.token;
            setStreamingText(streamingTextRef.current);
            break;

          case "interviewerDone": {
            const finalText = streamingTextRef.current;
            setMessages((prev) => [
              ...prev,
              { id: e.value.messageId, role: "interviewer", content: finalText, seq: prev.length + 1 },
            ]);
            setStreamingText("");
            streamingTextRef.current = "";
            setState("waiting");
            break;
          }

          case "ttsChunk":
            onTtsChunkRef.current?.(e.value.data, e.value.seq);
            break;

          case "ttsDone":
            onTtsDoneRef.current?.();
            break;

          case "error":
            setLastError(e.value.message);
            setState("waiting");
            break;
        }
      }
    } catch (err) {
      if (!abort.signal.aborted) {
        setLastError(String(err));
        setState("waiting");
      }
    }
  }, [sessionId]);

  const sendText = useCallback((content: string) => {
    submitTurn({ sessionId, input: { case: "textInput", value: { content } } });
  }, [sessionId, submitTurn]);

  const sendAudio = useCallback((audio: Uint8Array, mimeType: string) => {
    setState("transcribing");
    submitTurn({ sessionId, input: { case: "voiceInput", value: { audio, audioMimeType: mimeType } } });
  }, [sessionId, submitTurn]);

  const handleEndSession = useCallback(async () => {
    try {
      await client.endSession({ sessionId });
      setState("ended");
    } catch (err) {
      setLastError(String(err));
    }
  }, [sessionId]);

  const handleCancelSession = useCallback(async () => {
    try {
      await client.cancelSession({ sessionId });
      setState("cancelled");
    } catch (err) {
      setLastError(String(err));
    }
  }, [sessionId]);

  return {
    messages,
    streamingText,
    state,
    sessionInfo,
    lastError,
    sendText,
    sendAudio,
    endSession: handleEndSession,
    cancelSession: handleCancelSession,
    onTtsChunk: onTtsChunkRef.current,
    setOnTtsChunk: (h) => { onTtsChunkRef.current = h; },
    onTtsDone: onTtsDoneRef.current,
    setOnTtsDone: (h) => { onTtsDoneRef.current = h; },
  };
}
```

Note: The exact import paths and types from buf-generated code may differ. After `buf generate` (Task 3), check `web/src/pb/drill/v1/` for the actual file names and adjust imports. The `client.submitTurn` streaming API uses `for await` — verify this matches the ConnectRPC-ES client's async iterator pattern.

- [ ] **Step 2: Update interview.tsx to use the new hook**

Key changes in `web/src/pages/interview.tsx`:
- Replace `import { useInterview } from "@/ws/hooks"` with `import { useInterview } from "@/hooks/use-interview"`
- Remove `import { ConnectionState } from "@/ws/connection"`
- Remove `ConnectionBanner` component (no persistent connection to monitor)
- Remove `connectionState` from destructured hook return
- Remove `setRawMessageHandler` / `cmRef` — TTS wiring uses `setOnTtsChunk` / `setOnTtsDone` instead
- Update the audio player wiring to use the new TTS callback pattern
- On the ready page: fire `sendText("")` to trigger the opening question in background. Cache the streaming text and TTS audio. When user clicks "Ready", play back the cached audio.
- `inputDisabled` no longer checks `connectionState`

The `ReadyGate` component becomes:
```tsx
// Fire opening question when component mounts (before user clicks Ready).
useEffect(() => {
  if (messages.length === 0 && state === "ready") {
    sendText("");
  }
}, [messages.length, state, sendText]);

// Ready gate waits for the opening question to finish streaming.
if (!ready) {
  return (
    <ReadyGate
      questionTitle={sessionInfo?.questionTitle ?? session?.questionTitle ?? ""}
      questionPrompt={sessionInfo?.questionPrompt ?? session?.questionPrompt ?? ""}
      onReady={handleReady}
    />
  );
}
```

The opening question streams in the background. When the user clicks Ready, the opening interviewer message is already in `messages` (or still streaming in `streamingText`). Audio playback begins immediately from the cached TTS chunks.

- [ ] **Step 3: Wire TTS audio player to new hook callbacks**

Replace the `setRawMessageHandler` block:
```tsx
// OLD:
setRawMessageHandler((msg) => {
  if (msg.type === "tts_chunk") audioPlayer.enqueue(msg.data, msg.seq);
  else if (msg.type === "tts_done") audioPlayer.done();
});

// NEW:
useEffect(() => {
  setOnTtsChunk((data: Uint8Array, seq: number) => {
    audioPlayer.enqueue(data, seq);
  });
  setOnTtsDone(() => {
    audioPlayer.done();
  });
}, [audioPlayer, setOnTtsChunk, setOnTtsDone]);
```

- [ ] **Step 4: Verify frontend compiles**

Run: `cd web && npm run build`
Expected: Clean build (or TypeScript errors to fix based on exact generated types).

- [ ] **Step 5: Commit**

```
git add web/src/hooks/use-interview.ts web/src/pages/interview.tsx
git commit -m "feat: replace WebSocket interview hook with ConnectRPC streaming client"
```

---

## Task 9: Delete WebSocket Code

**Files:** All files listed in "What Gets Deleted" in the spec.

- [ ] **Step 1: Delete backend WebSocket files**

```bash
rm internal/handler/session_ws.go
rm internal/handler/session_ws_test.go
rm internal/interview/conductor.go
rm internal/interview/conductor_unit_test.go
rm internal/interview/state_machine.go
rm internal/interview/state_machine_test.go
rm internal/interview/transport/ws_client.go
rm internal/interview/transport/ws_client_test.go
rm internal/interview/ws_message.go
rm internal/interview/ws_message_test.go
rm internal/backend/session_lock.go
rm internal/backend/session_lock_test.go
```

- [ ] **Step 2: Delete frontend WebSocket files**

```bash
rm -r web/src/ws/
```

- [ ] **Step 3: Remove WebSocket route registration**

In the server/router file (likely `cmd/server/main.go` or `internal/handler/routes.go`), remove the WebSocket upgrade route (`/api/sessions/{id}/ws` or similar). Also remove the `nhooyr/websocket` import from any remaining files if no longer used.

- [ ] **Step 4: Remove WSConn from observer.go**

In `internal/interview/observer/observer.go`, remove the `WSConn` interface and the `"github.com/coder/websocket"` import. The observer package should have no WebSocket dependency.

- [ ] **Step 5: Clean up transport package**

Remove `internal/interview/transport/client.go` (the old `Client` interface — replaced by `TurnEventSink`). Keep `token_writer.go` and `tts_sink.go` only if they're still referenced by the new code. If Task 5 replaced them with inline `sinkTokenWriter` and `sinkTTSAdapter` in `turn.go`, delete them too.

Remove `internal/interview/transport/recorder.go` if it implements the old `Client` interface and is no longer used by any test.

- [ ] **Step 6: Verify build**

Run: `go build ./... && cd web && npm run build`
Expected: Clean build with no references to deleted files.

- [ ] **Step 7: Run all tests**

Run: `go test ./...`
Expected: All remaining tests pass. The deleted test files should not cause failures.

- [ ] **Step 8: Commit**

```
git add -A
git commit -m "refactor: remove WebSocket conductor, advisory locks, and WS frontend client"
```

---

## Task 10: Integration Test — Full Turn Round-Trip

**Files:**
- Create: `internal/rpc/interview/server_test.go`

- [ ] **Step 1: Write integration test for SubmitTurn**

Test the full round-trip: create session via existing SessionService, call SubmitTurn with empty text (opening question), verify streaming events, call SubmitTurn with text content, verify response, call EndSession.

Follow the existing ConnectRPC test patterns in the codebase (check `internal/rpc/session/server_test.go` or similar for the test setup pattern — likely uses `httptest.NewServer` with the ConnectRPC mux).

```go
func TestSubmitTurn_OpeningQuestion(t *testing.T) {
	// Set up test server with all services registered.
	// Create a session via the existing CreateSession RPC.
	// Call SubmitTurn with empty text input.
	// Collect all TurnEvents from the stream.
	// Assert: at least one InterviewerToken, exactly one InterviewerDone.
	// Call GetSessionState — verify messages include the interviewer message.
}

func TestSubmitTurn_TextTurn(t *testing.T) {
	// After opening question, submit a text turn.
	// Verify transcription_result is NOT emitted (text input).
	// Verify tokens + done events.
	// GetSessionState — verify 3 messages (opening interviewer, candidate, interviewer).
}

func TestEndSession_WaitsForGeneration(t *testing.T) {
	// Start a SubmitTurn in a goroutine.
	// Immediately call EndSession — should block until turn completes.
	// Verify session status is "completed" afterward.
}

func TestGetSessionState_KnownMessageCount(t *testing.T) {
	// After 2 turns, call GetSessionState with known_message_count=2.
	// Should return only messages after offset 2.
}

func TestCancelSession(t *testing.T) {
	// After opening question, call CancelSession.
	// Verify session status is "cancelled".
}
```

- [ ] **Step 2: Run integration tests**

Run: `go test ./internal/rpc/interview/ -v`
Expected: All pass.

- [ ] **Step 3: Commit**

```
git add internal/rpc/interview/server_test.go
git commit -m "test: add integration tests for InterviewService RPCs"
```

---

## Task 11: Manual Verification — Load in Browser

- [ ] **Step 1: Start local server**

Run: `./scripts/deploy.sh` (or however the local dev server starts).

- [ ] **Step 2: Navigate to interview page**

Open a session. Verify:
- Ready page loads, opening question streams in background.
- Clicking "Ready" plays back the opening interviewer message audio.
- Text input sends a turn, tokens stream back, TTS plays.
- Voice input records, uploads, transcribes, tokens stream, TTS plays.
- End session navigates to results.
- Cancel session navigates home.
- Page refresh mid-turn shows loading indicator, then completed message appears.
- Timer warnings appear at the right time.

- [ ] **Step 3: Commit any fixes**

```
git add -A
git commit -m "fix: address issues found during manual verification"
```

---

## Dependency Order

```
Task 1 (migration)
  → Task 2 (SQL queries)
    → Task 3 (proto)
      → Task 4 (sink interface + adapter)
        → Task 5 (ExecuteTurn)
          → Task 6 (RPC handlers)
            → Task 7 (cleanup job)
            → Task 8 (frontend hook)
              → Task 9 (delete WS code)
                → Task 10 (integration tests)
                  → Task 11 (manual verification)
```

Tasks 7 and 8 can run in parallel after Task 6.
