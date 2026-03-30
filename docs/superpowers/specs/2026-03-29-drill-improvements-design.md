# System Design Drill — Improvements Spec

## Overview

Seven improvements to the working drill app, plus LLM token tracking.

---

## 1. Session Lifecycle & Stateless Architecture

### URL Scheme

- `/` — Home / coach dashboard
- `/history` — session list with archive/unarchive
- `/sessions/:id` — the session. Renders interview UI if `active`, review UI if `reviewed`, results UI if `completed`/`evaluating`
- `/sessions/:id/learn` — educator deep analysis (full-page markdown)

### Creating a Session

Home page → user picks question → `POST /api/sessions` creates a DB row → navigate to `/sessions/:id`.

### The Orchestrator Always Loads from DB

There is no "start" vs "resume" distinction. On every WebSocket connect, the orchestrator receives `session_id`, queries the DB for the session, question, and messages, and reconstructs its state:

- `_session_id` from session row
- `_question` from `sessions.question_id` → questions table
- `_sequence` from `MAX(messages.sequence)`
- `_last_candidate_sequence` from `MAX(messages.sequence WHERE role='candidate')`
- `_timer_sec` from `sessions.timer_setting_sec`
- `_tts_enabled` from `sessions.tts_enabled`
- `_briefing` from `sessions.interviewer_briefed` + coach_reviews
- `_session_dir` from `sessions.audio_dir`
- State machine state: if zero messages → generate opening question. If messages exist → `WAITING_FOR_CANDIDATE`
- System prompt: rebuilt from question + timer + elapsed + briefing

Elapsed time = `now() - sessions.started_at`. Same source of truth for client timer and interviewer system prompt. No in-memory start time.

### Page Refresh

Navigating to `/sessions/:id` on an active session: loads messages from DB, populates chat, reconnects WebSocket, orchestrator loads from DB. Seamless resume.

### `_do_start` Removed

The current `_do_start` method is replaced by the DB load path. Session creation (`POST /api/sessions`) handles the DB insert. The orchestrator's init path handles loading state. If there are no messages yet, it generates the opening question as the first action.

---

## 2. Archive Sessions

### DB Change

```sql
ALTER TABLE sessions ADD COLUMN archived BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_sessions_archived ON sessions (archived);
```

### Behavior

- Archived sessions are excluded from: coach analysis, dimension averages, history default view
- Archived sessions remain viewable at `/sessions/:id`
- All aggregate queries add `WHERE archived = false`

### API

- `PATCH /api/sessions/:id/archive` — set `archived = true`
- `PATCH /api/sessions/:id/unarchive` — set `archived = false`
- `POST /api/sessions/archive-bulk` — body: `{"session_ids": [1, 2, 3]}`

### History Page UX

- Filter toggle: **Active** (default) | **Archived** | **All**
- Checkbox on each session row
- Header bar when any selected: "N selected — Archive | Select All | Clear"
- "Select All" checks all visible sessions. User unchecks ones to keep.
- When viewing Archived: action changes to "Unarchive"

---

## 3. Multi-Segment Recording

### Interaction Model

- **Hold spacebar** → recording (green indicator, waveform)
- **Release spacebar** → pause. Audio segment buffered. Status bar: `● 1 segment (4.2s) — SPACE for more | ENTER to submit | ESC to discard`
- **Hold spacebar again** → record another segment (appended)
- **Release** → pause. `● 2 segments (8.3s) — ...`
- **Press Enter** → concatenate all segments, base64 encode, send as `end_turn`
- **Press Escape** → discard all pending segments, reset to idle

### Implementation

- `useAudio` keeps `segmentsRef: Ref<Blob[]>` — array of raw Blobs, one per spacebar cycle
- Each hold/release cycle: `MediaRecorder.start()` / `.stop()` → blob appended to segments
- On Enter: `new Blob(segments, {type: "audio/webm"})` → concatenate → `blobToBase64` → send
- On Escape: `segmentsRef.current = []`, reset UI
- `pendingSegments` and `pendingDuration` state exposed for the status bar

### UI

Bottom bar shows pending segment info when segments exist but haven't been submitted. Text input and spacebar remain independent interactions (no mixing).

---

## 4. Educator Role

### Purpose

Runs after the evaluator. Takes the evaluation's gaps as input and produces deep technical educational content. **Triggered by user action** ("Generate Deep Analysis" button on session review page), not automatic.

### Model

Claude Opus (`claude-opus-4-6`). Token budget: `max_tokens: 8000`.

### Input

- Question title + prompt
- Full transcript
- Evaluator output (scores, gaps, strengths, advice)

### Output (Markdown)

**Model Answer** — what a strong answer to this specific problem looks like, tailored to the exact question as framed in the interview:
- Concrete architecture with specific technology choices and reasoning
- Data model with actual schemas
- Key algorithms or protocols
- Tradeoffs explicitly stated
- What separates a good answer from an exceptional one

**Gap Deep-Dives** — for each gap the evaluator identified:
- What the candidate should have known
- How this works in practice at real companies
- Concrete implementation details (not "go research X" but the actual explanation)
- Real system examples where relevant

### Storage

```sql
ALTER TABLE evaluations ADD COLUMN educator_model_answer TEXT;
ALTER TABLE evaluations ADD COLUMN educator_gap_deepdives TEXT;
ALTER TABLE evaluations ADD COLUMN educator_raw_response JSONB;
```

Both `model_answer` and `gap_deepdives` are markdown TEXT. The raw JSONB preserves the full LLM response.

### API

- `POST /api/sessions/:id/educate` — triggers educator analysis in background
- `GET /api/sessions/:id/educator` — returns educator content (null if not yet generated)

### UI

- Session review page: "Generate Deep Analysis" button (shows if educator content doesn't exist yet)
- Once generated: button becomes "View Deep Analysis" → navigates to `/sessions/:id/learn`
- `/sessions/:id/learn`: full-page rendered markdown. React-markdown for rendering. Navigation back to session review.

### LLM Implementation

Uses `tool_use` for structured output, same pattern as evaluator. Tool schema:

```json
{
  "name": "submit_education",
  "input_schema": {
    "type": "object",
    "required": ["model_answer", "gap_deepdives"],
    "properties": {
      "model_answer": {"type": "string", "description": "Markdown: what a strong answer looks like"},
      "gap_deepdives": {"type": "string", "description": "Markdown: detailed explanation for each gap"}
    }
  }
}
```

---

## 5. LLM Token Tracking

### No New Table

Token usage is already stored in `raw_response` JSONB on evaluations and coach_reviews. The remaining gap is interviewer calls (streaming, per-turn).

### DB Change

```sql
ALTER TABLE messages ADD COLUMN raw_response JSONB;
```

For interviewer messages: after the stream completes, store the final API response (which includes `usage`) in `messages.raw_response`. Candidate messages leave this null.

### Querying

Token usage across all roles via UNION ALL:

```sql
-- Interviewer tokens (per-turn)
SELECT session_id, 'interviewer' as role,
    raw_response->>'model' as model,
    (raw_response->'usage'->>'input_tokens')::int as input_tokens,
    (raw_response->'usage'->>'output_tokens')::int as output_tokens
FROM messages
WHERE role = 'interviewer' AND raw_response IS NOT NULL

UNION ALL

-- Evaluator tokens
SELECT session_id, 'evaluator' as role,
    raw_response->>'model',
    (raw_response->'usage'->>'input_tokens')::int,
    (raw_response->'usage'->>'output_tokens')::int
FROM evaluations

UNION ALL

-- Coach tokens
SELECT null, 'coach',
    raw_response->>'model',
    (raw_response->'usage'->>'input_tokens')::int,
    (raw_response->'usage'->>'output_tokens')::int
FROM coach_reviews

UNION ALL

-- Educator tokens (from evaluations table)
SELECT session_id, 'educator',
    educator_raw_response->>'model',
    (educator_raw_response->'usage'->>'input_tokens')::int,
    (educator_raw_response->'usage'->>'output_tokens')::int
FROM evaluations
WHERE educator_raw_response IS NOT NULL
```

Can wrap this in a Postgres VIEW for convenience.

### Dashboard Visibility

Home page: small token summary — "Total: 1.2M input / 340K output across 12 sessions"
History page: per-session token count column.

---

## 6. Coach "Refresh Analysis" Button

Home page coach card gets a "Refresh" button. Calls `POST /api/coach/analyze?force=true` which bypasses the debounce check. Useful after archiving sessions to get a fresh analysis based on remaining data.

---

## 7. Timer Accuracy

Elapsed time for the interviewer system prompt uses `now() - sessions.started_at` (DB timestamp), not an in-memory `time.time()` value. The client timer also computes from the DB `started_at` timestamp sent in the session data. Single source of truth eliminates drift.

---

## DB Migration Summary

```sql
-- 002_improvements.sql

-- Archive support
ALTER TABLE sessions ADD COLUMN archived BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX idx_sessions_archived ON sessions (archived);

-- TTS preference persistence
ALTER TABLE sessions ADD COLUMN tts_enabled BOOLEAN NOT NULL DEFAULT true;

-- Educator output
ALTER TABLE evaluations ADD COLUMN educator_model_answer TEXT;
ALTER TABLE evaluations ADD COLUMN educator_gap_deepdives TEXT;
ALTER TABLE evaluations ADD COLUMN educator_raw_response JSONB;

-- LLM token tracking (interviewer per-turn usage)
ALTER TABLE messages ADD COLUMN raw_response JSONB;

-- Convenience view for token aggregation across all roles
CREATE OR REPLACE VIEW llm_token_usage AS
    SELECT session_id, 'interviewer' as role,
        raw_response->>'model' as model,
        (raw_response->'usage'->>'input_tokens')::int as input_tokens,
        (raw_response->'usage'->>'output_tokens')::int as output_tokens,
        timestamp as created_at
    FROM messages
    WHERE role = 'interviewer' AND raw_response IS NOT NULL
    UNION ALL
    SELECT session_id, 'evaluator',
        raw_response->>'model',
        (raw_response->'usage'->>'input_tokens')::int,
        (raw_response->'usage'->>'output_tokens')::int,
        evaluated_at
    FROM evaluations
    UNION ALL
    SELECT null, 'coach',
        raw_response->>'model',
        (raw_response->'usage'->>'input_tokens')::int,
        (raw_response->'usage'->>'output_tokens')::int,
        created_at
    FROM coach_reviews
    UNION ALL
    SELECT session_id, 'educator',
        educator_raw_response->>'model',
        (educator_raw_response->'usage'->>'input_tokens')::int,
        (educator_raw_response->'usage'->>'output_tokens')::int,
        evaluated_at
    FROM evaluations
    WHERE educator_raw_response IS NOT NULL;
```
