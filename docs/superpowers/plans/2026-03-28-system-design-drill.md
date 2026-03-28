# System Design Drill Implementation Plan (v2)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a local voice-driven system design interview drill with LLM interviewer, evaluator, coach, and progress tracking.

**Architecture:** FastAPI backend with three LLM role modules (interviewer, evaluator, coach), speech module (Whisper + TTS), PostgreSQL for structured data, filesystem for audio artifacts, React + TypeScript frontend with five screens. OpenTelemetry tracing with local file export.

**Tech Stack:** Python 3.11+ / FastAPI / asyncpg / Anthropic SDK / OpenAI SDK / React / Vite / TypeScript / PostgreSQL / OpenTelemetry

**Design spec:** `docs/superpowers/specs/2026-03-28-system-design-drill-design.md`

---

## Conventions

These apply across all tasks:

### Dependency injection, not global state

- `Settings` is instantiated once at startup, stored on `app.state`, injected via `Depends()`. Never instantiate `Settings()` inline.
- Database pool lives on `app.state`. Connections are acquired via a `Depends()` function that yields a connection from the pool. No global `_pool` variable, no `get_pool()`.
- LLM clients (Anthropic, OpenAI) are created once at startup from settings, stored on `app.state`, injected where needed.

```python
# Pattern for all route handlers and background tasks:
async def get_db(request: Request) -> AsyncGenerator[asyncpg.Connection, None]:
    async with request.app.state.pool.acquire() as conn:
        yield conn

async def get_settings(request: Request) -> Settings:
    return request.app.state.settings
```

### LLM response parsing

All LLM calls that expect structured JSON output must use this pattern:

1. **Prefer Claude's `tool_use`** for structured output where possible. Define the expected schema as a tool, and Claude returns structured JSON guaranteed to match the schema. This solves *syntactic* parsing (no markdown fences, no preamble text, valid JSON guaranteed).
2. **Semantic validation after parsing.** Tool use guarantees structure but not sense. After extracting the tool call input, validate: scores are not all identical (degenerate), strengths/gaps arrays are non-empty, advice is more than one sentence. If validation fails, retry once with feedback ("scores appear degenerate — please re-evaluate more carefully"). If retry also fails, accept the result but flag `status_detail` on the session so the user sees "evaluation may need re-review."
3. **If using raw text output**, wrap `json.loads` in a `parse_llm_json()` helper that: strips markdown fences, strips any text before the first `{` or `[`, retries once on parse failure with a "please return valid JSON only" follow-up, and raises a clear error with the raw text for debugging.
4. **Always store `raw_response`** — the unparsed LLM output — in both the database and filesystem, so parse failures can be diagnosed and re-parsed later.
5. **On API errors**, retry once with exponential backoff. If the retry fails, store the error in `status_detail` and surface it to the user. Do not fall back to `parse_llm_json()` on API errors — that's a different failure mode.

### Error propagation from background tasks

Background tasks (evaluation, coach analysis) must:
1. Catch all exceptions and store the error state (e.g., `session.status = 'evaluation_failed'`, error message in a `status_detail` TEXT column on sessions).
2. The frontend polls status and displays errors rather than spinning forever. Add a timeout (60s for evaluation, 90s for coach) after which the frontend shows "Evaluation timed out — retry?"

### Audio encoding

Never use `btoa(String.fromCharCode(...spread))` for binary data — it crashes on buffers >~100KB. Use a chunked encoding approach:

```typescript
function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve) => {
    const reader = new FileReader();
    reader.onloadend = () => {
      const result = reader.result as string;
      resolve(result.split(",")[1]); // strip data URL prefix
    };
    reader.readAsDataURL(blob);
  });
}
```

### File naming for audio chunks

Audio chunks within a turn use the format `turn_{turn:03d}_chunk_{chunk:03d}.{ext}`. This prevents overwriting when TTS streams multiple chunks per turn, or when the user speaks in multiple segments.

### CSS

Single stylesheet (`global.css`) with scoped class names. No CSS modules, no Tailwind. This is a personal tool with ~15 components — class name collision risk is zero. One file is simpler to iterate on than a dozen `.module.css` files.

### Audio playback

TTS returns MP3 chunks. The browser plays them using an `AudioContext` source buffer queue:

1. Each incoming MP3 chunk is appended to a buffer array.
2. A playback loop checks the buffer. When a chunk is available, it creates an `AudioBufferSourceNode`, schedules it to start at the end of the previous chunk's duration, and plays it.
3. If the user interrupts (presses spacebar), stop all scheduled sources and clear the buffer.

Do NOT use `new Audio()` per chunk (overlapping playback) or `decodeAudioData` on incomplete buffers (requires complete audio files). Instead, stream MP3 data through `MediaSource` + `SourceBuffer` which handles partial data natively.

### Connection loss during interviews

If the WebSocket drops mid-interview:
1. The orchestrator's `run()` loop exits via the `shutdown` message.
2. The session is saved in its current state (`status: active`, all messages so far preserved in DB).
3. The frontend shows "Connection lost. Your session has been saved." with options: "Review what we have" (navigates to session review with partial transcript) or "Back to Home."
4. There is no automatic resume. A new WS connection creates a new orchestrator. The partial session's data is preserved for review but the interview cannot be continued. This is acceptable for v1 — resume would require serializing orchestrator state, which is complex for minimal benefit.

### Coach analysis locking

Only one coach analysis can run at a time. Use a simple advisory lock:

```python
async def try_run_coach(conn):
    locked = await conn.fetchval("SELECT pg_try_advisory_lock(1)")
    if not locked:
        return  # another analysis is running
    try:
        # ... run coach
    finally:
        await conn.fetchval("SELECT pg_advisory_unlock(1)")
```

This also handles the debounce: the endpoint checks `latest_review.created_at >= latest_eval.evaluated_at`. If a second evaluation completes while the coach is running, the next Home load will trigger a new analysis (the lock will be released by then).

### Database creation

Do NOT use shell `createdb`. Use asyncpg to connect to the `postgres` database and issue `CREATE DATABASE drill` via SQL. This works regardless of pg_hba.conf, socket paths, or default roles:

```python
conn = await asyncpg.connect("postgresql://localhost/postgres")
await conn.execute("CREATE DATABASE drill")
```

### Frontend processing feedback

When the orchestrator sends `{"type": "state", "state": "PROCESSING"}`, the frontend must show visible feedback:
- A pulsing indicator or "Thinking..." label below the chat
- The timer continues counting
- The text input is disabled (prevents sending while processing)

This prevents the "app looks frozen" problem during the 3-5 second Whisper+Claude pipeline.

### Testing

Test the hardest code, not the easiest. Priorities:

1. **Interview orchestrator** — the core interaction loop. Include error paths:
   - Whisper returns empty transcription → send error, let user re-record or type
   - Claude stream errors partway through → save partial response, send error, offer retry
   - DB connection drops mid-session → catch, attempt reconnect, save what we can
2. **State machine** — all transitions including edge cases
3. **LLM response parsing** — malformed JSON, markdown fences, missing fields, semantic validation (degenerate scores)
4. **Database constraints** — verify CHECK constraints, foreign keys, and enums reject bad data
5. **WebSocket handler** — test with Starlette TestClient, verify message routing, verify TTS interrupt works outside the queue

Do NOT write tests that only verify mock setup. If a test mocks the return value and asserts that value, it's not testing anything.

---

## File Structure

```
drill/
├── backend/
│   ├── __init__.py
│   ├── main.py                  # FastAPI app, lifespan, CORS, DI setup
│   ├── config.py                # Settings from .env via pydantic-settings
│   ├── deps.py                  # Depends() factories: get_db, get_settings, get_clients
│   ├── models.py                # All Pydantic models (typed interfaces)
│   ├── database.py              # Raw SQL query functions (take conn as arg)
│   ├── storage.py               # Filesystem ops (audio dirs, transcripts, traces)
│   ├── llm_parse.py             # parse_llm_json() helper, markdown fence stripping
│   ├── interviewer.py           # Interviewer LLM role
│   ├── evaluator.py             # Evaluator LLM role (uses tool_use for structured output)
│   ├── coach.py                 # Coach LLM role (uses tool_use for structured output)
│   ├── speech.py                # Whisper + TTS wrappers
│   ├── audio_queue.py           # Server-side audio chunk management
│   ├── orchestrator.py          # InterviewOrchestrator: decomposed WS handler
│   ├── session_manager.py       # State machine only (no I/O)
│   ├── tracing.py               # OTEL provider, file exporter, setup
│   ├── routes/
│   │   ├── __init__.py          # Router aggregation
│   │   ├── questions.py         # Question bank CRUD
│   │   ├── sessions.py          # Session history + detail + stats
│   │   ├── evaluation.py        # Trigger evaluation, get status
│   │   ├── coach_routes.py      # Coach review endpoints
│   │   ├── traces.py            # Trace data for frontend widget
│   │   └── ws.py                # WebSocket endpoint — thin, delegates to orchestrator
│   ├── seed_questions.py        # 18 seed questions
│   └── migrations/              # Numbered SQL migration files
│       ├── 001_initial.sql
│       └── run.py               # Migration runner
├── scripts/
│   ├── init_db.py               # createdb + run migrations + seed
│   └── run.py                   # Start backend + frontend
├── tests/
│   ├── conftest.py              # Fixtures: test DB, mock clients, app factory
│   ├── test_orchestrator.py     # Core interview loop tests
│   ├── test_session_manager.py  # State machine transitions
│   ├── test_evaluator.py        # LLM parsing, malformed JSON handling
│   ├── test_coach.py            # History summary, logic correctness
│   ├── test_database.py         # CRUD + constraint enforcement
│   ├── test_llm_parse.py        # JSON extraction from messy LLM output
│   └── test_routes.py           # REST endpoint integration tests
├── frontend/
│   ├── index.html
│   ├── package.json
│   ├── vite.config.ts           # Dev proxy to localhost:8000
│   ├── tsconfig.json
│   └── src/
│       ├── main.tsx
│       ├── App.tsx
│       ├── types.ts             # Mirrors backend Pydantic models
│       ├── api/
│       │   ├── client.ts        # REST fetch wrapper
│       │   └── ws.ts            # WebSocket client with reconnect
│       ├── hooks/
│       │   ├── useWebSocket.ts  # Uses ref for stable callback
│       │   ├── useAudio.ts      # Recording + queued playback
│       │   └── useTimer.ts
│       ├── pages/
│       │   ├── Home.tsx
│       │   ├── Interview.tsx    # Voice + text input, push-to-talk
│       │   ├── Results.tsx      # Polls with timeout, shows errors
│       │   ├── History.tsx
│       │   └── SessionReview.tsx
│       ├── components/
│       │   ├── ChatMessage.tsx
│       │   ├── TextInput.tsx    # Always-visible text input for typing
│       │   ├── Timer.tsx
│       │   ├── AudioControls.tsx
│       │   ├── ScoreBar.tsx
│       │   ├── ScoreDashboard.tsx
│       │   ├── QuestionList.tsx
│       │   ├── CoachCard.tsx
│       │   └── TraceWidget.tsx
│       └── global.css           # Single stylesheet, dark theme
├── data/                        # Created at runtime
├── .env.example
├── requirements.txt
└── pyproject.toml
```

Key differences from v1:
- `deps.py` for dependency injection
- `llm_parse.py` for robust JSON extraction
- `orchestrator.py` replaces the monolithic WS handler
- `ws.py` is now a thin shell that delegates to `orchestrator`
- `migrations/` directory for schema versioning
- `TextInput.tsx` component for text-based conversation
- `styles/` directory with CSS modules
- `vite.config.ts` includes dev proxy config

---

### Task 1: Project Scaffolding + Config + Database

**Files:** `pyproject.toml`, `requirements.txt`, `.env.example`, `.gitignore`, `backend/__init__.py`, `backend/config.py`, `backend/deps.py`, `migrations/001_initial.sql`, `migrations/run.py`, `scripts/init_db.py`, `tests/conftest.py`, `tests/test_config.py`

- [ ] **Step 1: Create `pyproject.toml` with pytest config (asyncio_mode=auto, strict pyright)**
- [ ] **Step 2: Create `requirements.txt` with `>=` lower bounds, not pinned versions**

```
fastapi>=0.110.0
uvicorn[standard]>=0.29.0
asyncpg>=0.29.0
pydantic>=2.7.0
pydantic-settings>=2.3.0
anthropic>=0.30.0
openai>=1.40.0
httpx>=0.27.0
opentelemetry-api>=1.25.0
opentelemetry-sdk>=1.25.0
opentelemetry-instrumentation-fastapi>=0.46b0
opentelemetry-instrumentation-httpx>=0.46b0
pytest>=8.0.0
pytest-asyncio>=0.23.0
```

- [ ] **Step 3: Create `.env.example` with all config vars**
- [ ] **Step 4: Write failing test for config — verify defaults, verify missing keys raise**
- [ ] **Step 5: Implement `backend/config.py`** — `Settings(BaseSettings)` with all fields typed, no `type: ignore` needed

- [ ] **Step 6: Create `backend/deps.py`** — dependency injection factories

```python
from typing import AsyncGenerator
from fastapi import Request
import asyncpg
from backend.config import Settings

async def get_db(request: Request) -> AsyncGenerator[asyncpg.Connection, None]:
    async with request.app.state.pool.acquire() as conn:
        yield conn

def get_settings(request: Request) -> Settings:
    return request.app.state.settings

def get_anthropic(request: Request):
    return request.app.state.anthropic_client

def get_openai(request: Request):
    return request.app.state.openai_client
```

- [ ] **Step 7: Create `migrations/001_initial.sql`** — full schema from design spec (all ENUMs, tables, indexes, constraints). Add `status_detail TEXT` column to sessions for error messages.

- [ ] **Step 8: Create `migrations/run.py`** — simple migration runner

Reads `migrations/` directory, finds `.sql` files sorted numerically, tracks applied migrations in a `_migrations` table, applies unapplied ones in order.

- [ ] **Step 9: Create `backend/seed_questions.py`** — 18 seed questions from design spec (URL shortener through CDN). Each has title, prompt (deliberately vague), difficulty, tags, optional hints.

- [ ] **Step 10a: Create `scripts/init_db.py`** — connects to `postgres` DB via asyncpg, issues `CREATE DATABASE drill` (catches "already exists"), runs migrations, seeds questions
- [ ] **Step 10b: Create `tests/conftest.py`**

```python
# Creates drill_test DB, runs migrations, provides db_conn fixture
# with per-test transaction rollback for isolation.
# Also provides app fixture via create_app(database_url=test_db)
# that injects test pool/settings onto app.state.
```

- [ ] **Step 11: Run migrations against test DB, verify schema**
- [ ] **Step 12: Commit**

---

### Task 2: Pydantic Models + Database Layer

**Files:** `backend/models.py`, `backend/database.py`, `tests/test_models.py`, `tests/test_database.py`

- [ ] **Step 1: Write failing tests for models** — validate score range enforcement, defaults, enum values
- [ ] **Step 2: Implement `backend/models.py`** — all types from design spec. Key: `EvaluationCreate` uses `field_validator` for 1-5 range on all score fields.
- [ ] **Step 3: Run model tests, verify pass**
- [ ] **Step 4: Write failing tests for database layer**

Test CRUD for all tables. Also test constraint enforcement:

```python
async def test_session_rejects_invalid_status(db_conn):
    """Verify the enum constraint rejects invalid status values."""
    with pytest.raises(asyncpg.exceptions.InvalidTextRepresentationError):
        await db_conn.execute(
            "INSERT INTO sessions (question_id, status) VALUES ($1, $2)", 1, "bogus"
        )

async def test_evaluation_rejects_score_out_of_range(db_conn):
    """Verify CHECK constraint on scores."""
    # ... insert with score_requirements=6, expect exception
```

- [ ] **Step 5: Implement `backend/database.py`**

All query functions take `conn: asyncpg.Connection` as first arg — no global pool access. Include `get_dimension_averages`, `get_question_stats`, and a new `get_sessions_since(conn, since: datetime)` for the coach debounce logic.

- [ ] **Step 6: Run database tests, verify pass**
- [ ] **Step 7: Commit**

---

### Task 3: LLM Parse Helper + Speech Module

**Files:** `backend/llm_parse.py`, `backend/speech.py`, `tests/test_llm_parse.py`, `tests/test_speech.py`

- [ ] **Step 1: Write failing tests for `llm_parse.py`**

```python
def test_strips_markdown_fences():
    raw = '```json\n{"key": "value"}\n```'
    assert parse_llm_json(raw) == {"key": "value"}

def test_strips_preamble_text():
    raw = 'Here is the JSON:\n{"key": "value"}'
    assert parse_llm_json(raw) == {"key": "value"}

def test_handles_trailing_comma():
    raw = '{"key": "value",}'
    # Should either fix or raise clear error

def test_raises_on_truly_unparseable():
    with pytest.raises(LLMParseError) as exc:
        parse_llm_json("This is not JSON at all")
    assert "This is not JSON at all" in str(exc.value)  # raw text in error
```

- [ ] **Step 2: Implement `backend/llm_parse.py`**

```python
class LLMParseError(Exception):
    def __init__(self, message: str, raw_text: str):
        super().__init__(message)
        self.raw_text = raw_text

def parse_llm_json(text: str) -> dict[str, Any]:
    """Extract JSON from LLM output that may include markdown fences or preamble."""
    # 1. Strip markdown fences
    # 2. Find first { or [ and last } or ]
    # 3. Try json.loads
    # 4. On failure, raise LLMParseError with raw text
```

- [ ] **Step 3: Run tests, verify pass**

- [ ] **Step 4: Write tests for speech module** — test that `transcribe_audio` correctly constructs the API call (file format, model parameter). Test error handling: what happens when the API returns an error?

- [ ] **Step 5: Implement `backend/speech.py`** — `transcribe_audio` and `generate_tts` as async functions. `generate_tts` yields bytes chunks. Both take the client as an argument (injected, not created internally).

- [ ] **Step 6: Commit**

---

### Task 4: Interviewer Module

**Files:** `backend/interviewer.py`, `tests/test_interviewer.py`

- [ ] **Step 1: Write failing tests**

```python
def test_system_prompt_includes_elapsed_time():
    interviewer = Interviewer(config=InterviewerConfig())
    prompt = interviewer.build_system_prompt(
        question_title="Rate Limiter", question_prompt="...",
        timer_sec=2700, elapsed_sec=1200,
    )
    assert "1200" in prompt or "20 minutes" in prompt

def test_system_prompt_without_briefing_has_no_briefing_section():
    prompt = interviewer.build_system_prompt(..., briefing=None)
    assert "Candidate Briefing" not in prompt

def test_build_messages_maps_roles_correctly():
    # Interviewer → assistant, Candidate → user

def test_build_messages_with_empty_history():
    messages = interviewer.build_messages([])
    assert messages == []
```

- [ ] **Step 2: Implement `backend/interviewer.py`**

Key fix from review: **`elapsed_sec` must be interpolated into the template.** Add:

```
Current elapsed time: {elapsed_sec} seconds ({elapsed_min} minutes {elapsed_remaining_sec} seconds).
Time remaining: approximately {remaining_min} minutes.
```

The `Interviewer` class has:
- `build_system_prompt()` — returns the full prompt with elapsed time and optional briefing
- `build_messages()` — converts `list[Message]` to Anthropic API format
- `get_response_stream()` — streams tokens from Claude
- `get_opening()` — gets the first interviewer message

- [ ] **Step 3: Run tests, verify pass**
- [ ] **Step 4: Commit**

---

### Task 5: Evaluator Module

**Files:** `backend/evaluator.py`, `tests/test_evaluator.py`

- [ ] **Step 1: Write failing tests**

```python
def test_parse_evaluation_response_valid():
    # Well-formed response → EvaluationCreate with correct scores

def test_parse_evaluation_response_missing_field():
    # Missing "gaps" key → clear error, not a crash

def test_evaluator_uses_tool_use():
    """Verify the evaluator defines a tool schema for structured output."""
    evaluator = Evaluator()
    tools = evaluator.get_tool_schema()
    assert tools[0]["name"] == "submit_evaluation"
    # Verify all score fields are in the schema with min/max constraints
```

- [ ] **Step 2: Implement `backend/evaluator.py`**

**Key design decision: use Claude's `tool_use` for structured output.** Define a tool called `submit_evaluation` with the exact schema we expect. Claude will return the structured data as a tool call, which is guaranteed to be valid JSON matching the schema. This eliminates the JSON parsing fragility.

```python
EVALUATION_TOOL = {
    "name": "submit_evaluation",
    "description": "Submit the structured evaluation of the interview transcript.",
    "input_schema": {
        "type": "object",
        "required": ["scores", "strengths", "gaps", "advice", "annotations"],
        "properties": {
            "scores": {
                "type": "object",
                "required": ["requirements", "highlevel", "deepdive", "scalability", "communication", "overall"],
                "properties": {
                    "requirements": {"type": "integer", "minimum": 1, "maximum": 5},
                    # ... same for all dimensions
                },
            },
            "strengths": {"type": "array", "items": {"type": "string"}},
            "gaps": {"type": "array", "items": {"type": "string"}},
            "advice": {"type": "string"},
            "annotations": {
                "type": "array",
                "items": {
                    "type": "object",
                    "required": ["message_sequence", "type", "content"],
                    "properties": {
                        "message_sequence": {"type": "integer"},
                        "type": {"type": "string", "enum": ["strength", "gap", "missed_opportunity", "note"]},
                        "content": {"type": "string"},
                    },
                },
            },
        },
    },
}
```

The evaluator sends the transcript with `tools=[EVALUATION_TOOL]` and `tool_choice={"type": "tool", "name": "submit_evaluation"}`. The response is guaranteed structured JSON. Fall back to `parse_llm_json()` if tool_use somehow fails.

System prompt: full rubric from design spec. Include the metacognitive feedback instructions.

- [ ] **Step 3: Run tests, verify pass**
- [ ] **Step 4: Commit**

---

### Task 6: Coach Module

**Files:** `backend/coach.py`, `tests/test_coach.py`

- [ ] **Step 1: Write failing tests**

```python
def test_build_history_summary_maps_questions_correctly():
    """Verify the coach resolves session→question correctly."""
    sessions = [Session(id=1, question_id=10, ...), Session(id=2, question_id=20, ...)]
    evaluations = [Evaluation(session_id=1, ...), Evaluation(session_id=2, ...)]
    questions = [Question(id=10, title="URL Shortener", ...), Question(id=20, title="Chat", ...)]

    coach = Coach()
    summary = coach.build_history_summary(sessions, evaluations, questions)
    assert "URL Shortener" in summary
    assert "Chat" in summary

def test_build_history_summary_only_includes_attempted_tags():
    """Only questions that have sessions should contribute tags."""
    sessions = [Session(id=1, question_id=10, ...)]
    evaluations = [Evaluation(session_id=1, ...)]
    questions = [
        Question(id=10, title="URL Shortener", tags=["caching"], ...),
        Question(id=20, title="Chat", tags=["websocket"], ...),  # not attempted
    ]
    coach = Coach()
    summary = coach.build_history_summary(sessions, evaluations, questions)
    assert "caching" in summary
    assert "websocket" not in summary
```

- [ ] **Step 2: Implement `backend/coach.py`**

**Key fix from review: `build_history_summary` must accept sessions, evaluations, AND questions, and correctly map session_id → question_id → question.**

```python
def build_history_summary(
    self,
    sessions: list[Session],
    evaluations: list[Evaluation],
    questions: list[Question],
) -> str:
    question_map = {q.id: q for q in questions}
    session_map = {s.id: s for s in sessions}

    for ev in evaluations:
        session = session_map.get(ev.session_id)
        if not session:
            continue
        question = question_map.get(session.question_id)  # correct mapping
        # ...

    # Only include tags from actually attempted questions
    attempted_question_ids = {s.question_id for s in sessions if s.status == SessionStatus.REVIEWED}
    attempted_tags = set()
    for q in questions:
        if q.id in attempted_question_ids:
            attempted_tags.update(q.tags)
```

Use `tool_use` for structured output, same pattern as evaluator. Tool schema defines `recommendation`, `gap_analysis`, and optional `generated_question`.

- [ ] **Step 3: Run tests, verify pass**
- [ ] **Step 4: Commit**

---

### Task 7: Storage + Session State Machine

**Files:** `backend/storage.py`, `backend/session_manager.py`, `tests/test_storage.py`, `tests/test_session_manager.py`

- [ ] **Step 1: Write tests for storage** — create dirs, save chunks with chunk index, save transcript, verify no overwrites
- [ ] **Step 2: Implement `backend/storage.py`**

Key fix: audio chunks include chunk index in filename:

```python
def save_audio_chunk(self, session_dir: str, direction: str, turn: int, chunk_index: int, data: bytes, format: str = "webm") -> str:
    filename = f"turn_{turn:03d}_chunk_{chunk_index:03d}.{format}"
    # ...
```

- [ ] **Step 3: Write tests for session state machine** — all valid transitions, invalid transitions raise, interrupt flow, end-session from any active state
- [ ] **Step 4: Implement `backend/session_manager.py`** — pure state machine, no I/O. Same as v1 but with `elapsed_sec` computed from stored start time.
- [ ] **Step 5: Run all tests, verify pass**
- [ ] **Step 6: Commit**

---

### Task 8: Interview Orchestrator (sequential message queue)

**Files:** `backend/orchestrator.py`, `backend/messages.py`, `tests/test_orchestrator.py`

This is the most critical piece. The architecture is a **sequential message queue** — one message processed at a time, no concurrency except TTS playback. This eliminates race conditions by construction: two messages can never be in-flight simultaneously, so state machine checks become assertions rather than guards.

**Why sequential:** Transcription, LLM calls, DB writes, and state transitions should never overlap. There's nothing useful the user can do while the pipeline is processing a turn. The only genuinely concurrent operation is TTS playback (audio plays while the user reads or thinks, and needs to be interruptible). TTS runs as a separate task with explicit cancellation.

**TTS interrupt is the one exception to the queue.** The queue blocks during LLM calls. If the user wants to interrupt TTS during that time, `cancel_tts` must be callable directly — it doesn't go through the queue. This is a small, contained exception.

- [ ] **Step 1: Define message types**

Create `backend/messages.py`:

```python
from __future__ import annotations
from dataclasses import dataclass
from typing import Literal

@dataclass
class WSMessage:
    type: str
    question_id: int | None = None
    timer_sec: int | None = None
    tts_enabled: bool | None = None
    briefed: bool | None = None
    audio_data: bytes | None = None
    text: str | None = None

def parse_ws_message(raw: dict) -> WSMessage:
    """Parse raw JSON dict into typed WSMessage."""
    msg_type = raw["type"]
    return WSMessage(
        type=msg_type,
        question_id=raw.get("question_id"),
        timer_sec=raw.get("timer_sec"),
        tts_enabled=raw.get("tts_enabled"),
        briefed=raw.get("briefed"),
        audio_data=base64.b64decode(raw["audio_data"]) if "audio_data" in raw else None,
        text=raw.get("text"),
    )
```

- [ ] **Step 2: Write failing tests for orchestrator**

Tests enqueue messages, drain the queue, and assert on results. No timing-sensitive assertions, no wondering about interleavings.

```python
@pytest.mark.asyncio
async def test_start_then_end_turn_flow(mock_deps):
    """Full turn: start → end_turn → produces transcription + interviewer response."""
    orch = InterviewOrchestrator(mock_deps)
    results = []

    await orch.enqueue(WSMessage(type="start", question_id=1, timer_sec=2700, tts_enabled=False, briefed=False))
    await orch.enqueue(WSMessage(type="end_turn", audio_data=b"fake-audio"))
    await orch.enqueue(WSMessage(type="shutdown"))

    await orch.run(send=results.append)

    types = [r["type"] for r in results]
    assert "interviewer_text" in types  # opening
    assert "transcription" in types
    # Second interviewer_text after the transcription
    text_indices = [i for i, r in enumerate(results) if r["type"] == "interviewer_text"]
    trans_index = types.index("transcription")
    assert any(i > trans_index for i in text_indices)

@pytest.mark.asyncio
async def test_text_input_skips_whisper(mock_deps):
    """Text input bypasses Whisper, goes straight to Claude."""
    orch = InterviewOrchestrator(mock_deps)
    results = []

    await orch.enqueue(WSMessage(type="start", question_id=1, timer_sec=2700, tts_enabled=False, briefed=False))
    await orch.enqueue(WSMessage(type="text_input", text="I'd start by clarifying requirements..."))
    await orch.enqueue(WSMessage(type="shutdown"))

    await orch.run(send=results.append)

    types = [r["type"] for r in results]
    assert "transcription" not in types  # no whisper involved
    assert "interviewer_text" in types

@pytest.mark.asyncio
async def test_end_turn_before_start_sends_error(mock_deps):
    """Sending end_turn without starting sends error, doesn't crash."""
    orch = InterviewOrchestrator(mock_deps)
    results = []

    await orch.enqueue(WSMessage(type="end_turn", audio_data=b"audio"))
    await orch.enqueue(WSMessage(type="shutdown"))

    await orch.run(send=results.append)

    assert any(r["type"] == "error" for r in results)

@pytest.mark.asyncio
async def test_messages_are_sequential(mock_deps):
    """Two rapid end_turns process one at a time, never overlap."""
    call_count = 0
    max_concurrent = 0

    original_transcribe = mock_deps.openai_client.audio.transcriptions.create

    async def counting_transcribe(*args, **kwargs):
        nonlocal call_count, max_concurrent
        call_count += 1
        current = call_count
        max_concurrent = max(max_concurrent, current)
        result = await original_transcribe(*args, **kwargs)
        call_count -= 1
        return result

    mock_deps.openai_client.audio.transcriptions.create = counting_transcribe

    orch = InterviewOrchestrator(mock_deps)
    await orch.enqueue(WSMessage(type="start", question_id=1, timer_sec=2700, tts_enabled=False, briefed=False))
    await orch.enqueue(WSMessage(type="end_turn", audio_data=b"audio1"))
    await orch.enqueue(WSMessage(type="end_turn", audio_data=b"audio2"))
    await orch.enqueue(WSMessage(type="shutdown"))

    results = []
    await orch.run(send=results.append)

    assert max_concurrent <= 1  # never more than one transcription in flight

@pytest.mark.asyncio
async def test_whisper_empty_transcription_sends_error(mock_deps):
    """Empty Whisper result sends error, doesn't crash the loop."""
    mock_deps.openai_client.audio.transcriptions.create = AsyncMock(
        return_value=MagicMock(text="")
    )
    orch = InterviewOrchestrator(mock_deps)
    results = []

    await orch.enqueue(WSMessage(type="start", question_id=1, timer_sec=2700, tts_enabled=False, briefed=False))
    await orch.enqueue(WSMessage(type="end_turn", audio_data=b"silence"))
    await orch.enqueue(WSMessage(type="shutdown"))

    await orch.run(send=results.append)

    assert any(r["type"] == "error" for r in results)
    # Loop didn't crash — shutdown was processed
    assert orch._state.state in {SessionState.WAITING_FOR_CANDIDATE, SessionState.ENDED}

@pytest.mark.asyncio
async def test_claude_stream_error_saves_partial(mock_deps):
    """If Claude errors mid-stream, save what we got, send error, continue."""
    # Mock that yields 2 tokens then raises
    async def failing_stream(*args, **kwargs):
        yield "I would"
        yield " suggest"
        raise anthropic.APIError("Connection reset")

    mock_deps.anthropic_client.messages.stream = failing_stream

    orch = InterviewOrchestrator(mock_deps)
    results = []

    await orch.enqueue(WSMessage(type="start", question_id=1, ...))
    await orch.enqueue(WSMessage(type="text_input", text="Let me discuss requirements..."))
    await orch.enqueue(WSMessage(type="shutdown"))

    await orch.run(send=results.append)

    errors = [r for r in results if r["type"] == "error"]
    assert len(errors) >= 1
    # Partial response should still have been streamed to the client
    texts = [r for r in results if r["type"] == "interviewer_text"]
    assert len(texts) > 0
```

- [ ] **Step 3: Implement `backend/orchestrator.py`**

```python
@dataclass
class OrchestratorDeps:
    """All external dependencies, injectable for testing."""
    pool: asyncpg.Pool
    anthropic_client: anthropic.AsyncAnthropic
    openai_client: AsyncOpenAI
    settings: Settings
    storage: SessionStorage


class InterviewOrchestrator:
    """Sequential message processor for one interview session.

    Architecture: one asyncio.Queue, one consumer. Messages are processed
    one at a time in order. No two handlers ever run concurrently.

    The only concurrent operation is TTS playback, which runs as a
    separate task and can be cancelled from outside the queue via
    cancel_tts().
    """

    def __init__(self, deps: OrchestratorDeps) -> None:
        self.deps = deps
        self._queue: asyncio.Queue[WSMessage] = asyncio.Queue()
        self._state = SessionStateMachine()
        self._interviewer = Interviewer(InterviewerConfig(model=deps.settings.interviewer_model))
        self._session_id: int | None = None
        self._session_dir: str | None = None
        self._question: Question | None = None
        self._system_prompt: str = ""
        self._sequence: int = 0
        self._tts_enabled: bool = True
        self._tts_task: asyncio.Task | None = None

    async def enqueue(self, msg: WSMessage) -> None:
        """Called by the WS handler. Never blocks meaningfully."""
        await self._queue.put(msg)

    def cancel_tts(self) -> None:
        """Cancel TTS playback. Callable outside the queue (for interrupts).
        This is the ONE exception to 'everything goes through the queue.'"""
        if self._tts_task and not self._tts_task.done():
            self._tts_task.cancel()

    async def run(self, send: Callable) -> None:
        """Main loop. Processes one message at a time.
        Run this as a task when the WS connects."""
        while True:
            msg = await self._queue.get()
            if msg.type == "shutdown":
                self.cancel_tts()
                await self._finalize_if_active(send)
                break
            try:
                await self._handle(msg, send)
            except Exception as e:
                await send({"type": "error", "message": str(e)})

    async def _handle(self, msg: WSMessage, send: Callable) -> None:
        """Dispatch to the appropriate handler. Sequential by construction."""
        match msg.type:
            case "start":
                await self._do_start(msg, send)
            case "end_turn":
                await self._do_end_turn(msg, send)
            case "text_input":
                await self._do_text_input(msg, send)
            case "edit_transcript":
                await self._do_edit_transcript(msg)
            case "end_session":
                await self._do_end_session(msg, send)
            case _:
                await send({"type": "error", "message": f"Unknown message type: {msg.type}"})

    async def _do_start(self, msg: WSMessage, send: Callable) -> None:
        """Create session, get opening statement, optionally start TTS."""
        self._state.transition(SessionState.STARTING)
        # ... create session in DB, build system prompt, get opening
        self._state.transition(SessionState.INTERVIEWER_SPEAKING)

        response_text = await self._stream_interviewer_opening(send)
        await self._save_message(MessageRole.INTERVIEWER, response_text)

        if self._tts_enabled:
            self._tts_task = asyncio.create_task(self._stream_tts(response_text, send))
        else:
            await send({"type": "interviewer_done"})

        self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
        await send({"type": "state", "state": self._state.state.value})

    async def _do_end_turn(self, msg: WSMessage, send: Callable) -> None:
        """Transcribe → display → Claude → TTS. All sequential except TTS."""
        self.cancel_tts()  # interrupt any playing TTS
        self._state.transition(SessionState.CANDIDATE_SPEAKING)
        self._state.transition(SessionState.PROCESSING)
        await send({"type": "state", "state": "PROCESSING"})

        # 1. Transcribe (sequential — blocks until done)
        transcript = await self._transcribe(msg.audio_data)
        await send({"type": "transcription", "text": transcript})
        await self._save_message(MessageRole.CANDIDATE, transcript, raw_content=transcript)

        # 2. Get interviewer response (sequential — streams tokens via send)
        self._state.transition(SessionState.INTERVIEWER_SPEAKING)
        response_text = await self._get_interviewer_response(send)
        await self._save_message(MessageRole.INTERVIEWER, response_text)

        # 3. TTS is fire-and-forget with cancellation handle (the ONE concurrent thing)
        if self._tts_enabled:
            self._tts_task = asyncio.create_task(self._stream_tts(response_text, send))
        else:
            await send({"type": "interviewer_done"})

        self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
        await send({"type": "state", "state": self._state.state.value})
        await send({"type": "timer", "elapsed_seconds": self._state.elapsed_seconds})

    async def _do_text_input(self, msg: WSMessage, send: Callable) -> None:
        """Same as end_turn but skip Whisper. Text provided directly."""
        self.cancel_tts()
        self._state.transition(SessionState.CANDIDATE_SPEAKING)
        self._state.transition(SessionState.PROCESSING)

        await self._save_message(MessageRole.CANDIDATE, msg.text)

        self._state.transition(SessionState.INTERVIEWER_SPEAKING)
        response_text = await self._get_interviewer_response(send)
        await self._save_message(MessageRole.INTERVIEWER, response_text)

        if self._tts_enabled:
            self._tts_task = asyncio.create_task(self._stream_tts(response_text, send))
        else:
            await send({"type": "interviewer_done"})

        self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
        await send({"type": "state", "state": self._state.state.value})

    async def _do_end_session(self, msg: WSMessage, send: Callable) -> None:
        """Get closing remark, finalize session, save transcript."""
        self.cancel_tts()
        self._state.transition(SessionState.ENDING)
        # ... get closing remark, save, transition to ENDED

    async def _do_edit_transcript(self, msg: WSMessage) -> None:
        """Update the most recent candidate message content."""
        # ... UPDATE messages SET content = $1 WHERE ...

    # --- Private helpers (all sequential, no concurrency concerns) ---

    async def _transcribe(self, audio_data: bytes) -> str: ...
    async def _get_interviewer_response(self, send: Callable) -> str: ...
    async def _stream_tts(self, text: str, send: Callable) -> None: ...
    async def _save_message(self, role: MessageRole, content: str, raw_content: str | None = None) -> None: ...
    async def _finalize_if_active(self, send: Callable) -> None: ...
```

**Key properties of this design:**

1. **Sequential by construction.** `_handle` is awaited before the next `_queue.get()`. Two handlers never run simultaneously. State machine checks are assertions, not race-prone guards.

2. **TTS is the one exception.** It runs as a `create_task` because audio should play while the user reads/thinks. `cancel_tts()` is callable from outside the queue — the WS receive loop can call it directly when it sees an `audio` message (the user started talking, interrupt playback).

3. **Testable without timing.** Enqueue messages, call `run()`, assert on the collected `send` results. The queue serializes everything. No mocking of asyncio, no sleep-based assertions.

4. **Error containment.** Each `_handle` call is wrapped in try/except. A failed transcription sends an error message but doesn't crash the loop. The next message in the queue still gets processed.

- [ ] **Step 4: Run orchestrator tests, verify pass**
- [ ] **Step 5: Commit**

---

### Task 9: Routes + Thin WebSocket Handler

**Files:** `backend/main.py`, `backend/routes/*.py`, `tests/test_routes.py`

- [ ] **Step 1: Implement `backend/main.py`** — lifespan creates pool (min=1, max=3), creates Anthropic + OpenAI clients, stores on `app.state`. Sets up CORS and tracing.

- [ ] **Step 2: Implement REST routes** — all use `Depends(get_db)`, `Depends(get_settings)`. Evaluation and coach routes run background tasks that catch exceptions and update session status on failure.

Coach analysis endpoint checks if new sessions exist since last review before triggering:

```python
@router.post("/analyze")
async def trigger_coach_analysis(
    db: asyncpg.Connection = Depends(get_db),
    settings: Settings = Depends(get_settings),
):
    latest_review = await get_latest_coach_review(db)
    latest_eval = await get_latest_evaluation_time(db)
    if latest_review and latest_eval and latest_review.created_at >= latest_eval:
        return {"status": "up_to_date"}  # no new sessions
    # ... trigger analysis
```

- [ ] **Step 3: Implement `backend/routes/ws.py`** — trivial shell, delegates everything to orchestrator queue

```python
@router.websocket("/ws/interview")
async def interview_websocket(ws: WebSocket) -> None:
    await ws.accept()
    deps = OrchestratorDeps(
        pool=ws.app.state.pool,
        anthropic_client=ws.app.state.anthropic_client,
        openai_client=ws.app.state.openai_client,
        settings=ws.app.state.settings,
        storage=SessionStorage(ws.app.state.settings.data_dir),
    )
    orch = InterviewOrchestrator(deps)

    async def send(data: dict) -> None:
        await ws.send_text(json.dumps(data))

    # The orchestrator runs its queue consumer as a task
    runner = asyncio.create_task(orch.run(send))

    try:
        while True:
            raw = await ws.receive_text()
            msg = parse_ws_message(json.loads(raw))

            # TTS interrupt is the ONE thing handled outside the queue.
            # User started talking — cancel audio immediately, don't wait
            # for the queue to drain.
            if msg.type == "audio":
                orch.cancel_tts()
                # Audio chunks are accumulated; end_turn carries final blob
                continue

            await orch.enqueue(msg)
    except WebSocketDisconnect:
        await orch.enqueue(WSMessage(type="shutdown"))
        await runner
```

**Audio protocol resolution:** The client accumulates audio chunks in the browser via MediaRecorder. `audio` messages are sent during recording for real-time waveform display (and to trigger TTS interrupt). The actual audio blob is sent with the `end_turn` message as `audio_data`. The server does NOT buffer audio chunks — the browser owns the recording buffer. This eliminates the ambiguity from v2.

- [ ] **Step 4: Write route tests** — REST endpoints (questions, sessions, stats). WebSocket test using Starlette `TestClient.websocket_connect()`.
- [ ] **Step 5: Run tests, verify pass**
- [ ] **Step 6: Commit**

---

### Task 10: Tracing

**Files:** `backend/tracing.py`, `backend/routes/traces.py`, `tests/test_tracing.py`

- [ ] **Step 1: Implement `backend/tracing.py`** — `FileSpanExporter` writes JSON to `data/traces/YYYY-MM-DD/`. `ACTION_LABELS` maps span names to user-friendly labels. Auto-instrumentation for FastAPI and httpx. Manual spans wrapped in the orchestrator.
- [ ] **Step 2: Implement `backend/routes/traces.py`** — returns recent trace entries for the widget.
- [ ] **Step 3: Write test** — verify FileSpanExporter creates files, verify ACTION_LABELS covers all span names.
- [ ] **Step 4: Commit**

---

### Task 11: Frontend Scaffolding

**Files:** `frontend/` — package.json, vite config, types, API client, WebSocket client, hooks

- [ ] **Step 1: Initialize Vite React+TS project, install react-router-dom and recharts**

- [ ] **Step 2: Configure `vite.config.ts` with dev proxy**

```typescript
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: {
      "/api": "http://localhost:8000",
      "/ws": { target: "ws://localhost:8000", ws: true },
    },
  },
});
```

- [ ] **Step 3: Create `src/types.ts`** — mirrors backend Pydantic models. Includes WS message types with `text_input` message type.

- [ ] **Step 4: Create `src/api/client.ts`** — fetch wrapper using relative paths (goes through vite proxy in dev)

- [ ] **Step 5: Create `src/api/ws.ts`** — WebSocket client with reconnect logic. Uses relative URL `/ws/interview` (proxied).

- [ ] **Step 6: Create hooks**

`useWebSocket.ts` — stores `onMessage` in a `useRef` that updates every render. The socket reads from the ref, so it never captures stale closures:

```typescript
export function useWebSocket(onMessage: (msg: WSServerMessage) => void) {
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;  // always latest

  const connect = useCallback(async () => {
    const socket = new InterviewSocket();
    await socket.connect();
    socket.onMessage((msg) => onMessageRef.current(msg));  // reads ref
    socketRef.current = socket;
  }, []);
  // ...
}
```

`useAudio.ts` — recording via MediaRecorder + **queued playback**:

```typescript
// Audio playback queue: chunks are buffered and played sequentially
// using AudioContext.decodeAudioData, not individual Audio() elements.
// A queue processes chunks in order, starting the next when the current finishes.
```

`useTimer.ts` — elapsed seconds counter.

- [ ] **Step 7: Create `src/styles/global.css`** — dark theme base styles, centered layout container

- [ ] **Step 8: Create App.tsx with routes, placeholder pages**
- [ ] **Step 9: Verify `npm run build` succeeds**
- [ ] **Step 10: Commit**

---

### Task 12: Interview Screen (Voice + Text)

**Files:** `frontend/src/pages/Interview.tsx`, `frontend/src/components/ChatMessage.tsx`, `frontend/src/components/Timer.tsx`, `frontend/src/components/AudioControls.tsx`, `frontend/src/components/TextInput.tsx`, CSS modules

- [ ] **Step 1: Implement components** — ChatMessage (interviewer left, candidate right), Timer (with warning color), AudioControls (waveform when recording), TextInput (always visible, Enter to send)

- [ ] **Step 2: Implement Interview page**

Key elements:
- Centered column layout (max-width 720px)
- Chat log with auto-scroll
- Timer in header with warning colors
- Push-to-talk: spacebar down starts recording, spacebar up sends to Whisper
- **Text input** always visible at bottom — type and press Enter as an alternative to voice
- Audio encoding uses `blobToBase64` with FileReader (not btoa spread)
- `useEffect` for WS connect has proper cleanup and error handling
- End Session button

```typescript
// Push-to-talk audio encoding (safe for large buffers):
const blob = await audio.stopRecording();
const base64 = await blobToBase64(blob);
ws.send({ type: "end_turn", audio_data: base64 });
```

```typescript
// Text input handler:
const handleTextSubmit = (text: string) => {
  ws.send({ type: "text_input", text });
  setMessages(prev => [...prev, { role: "candidate", content: text }]);
};
```

- [ ] **Step 3: Verify build**
- [ ] **Step 4: Commit**

---

### Task 13: Remaining Frontend Screens + Trace Widget

**Files:** `frontend/src/pages/Home.tsx`, `History.tsx`, `Results.tsx`, `SessionReview.tsx`, all remaining components, CSS modules

- [ ] **Step 1: Implement ScoreBar, ScoreDashboard, CoachCard, QuestionList components**

- [ ] **Step 2: Implement Home page**
- Coach card at top with recommendation and suggested question
- Summary stats row
- Dimension score bars
- Question bank (filterable)
- Coach analysis triggered only if new sessions exist (check via `/api/coach/latest` timestamp)

- [ ] **Step 3: Implement History page**
- Score trend chart (left=oldest, right=newest)
- Session list with score chips, clickable into review

- [ ] **Step 4: Implement Results page**
- Polls for evaluation with **timeout** — after 60s, show "Evaluation timed out" with retry button
- Checks `session.status` for `evaluation_failed` and shows the error
- Scores, strengths, gaps, advice when ready

- [ ] **Step 5: Implement Session Review page**
- Two-panel: transcript (left) with inline annotations, evaluation sidebar (right, sticky)
- Toggle for interviewer annotations (off by default)
- Annotations color-coded: green for strengths, red for gaps, orange for missed opportunities

- [ ] **Step 6: Implement TraceWidget**
- Fixed to viewport bottom-left
- Collapsed: latest trace ID, user-friendly action label, duration, clipboard copy icon
- Expanded: scrollable list of recent actions, each expandable to show child spans
- Polls `/api/traces/recent` every 5 seconds

- [ ] **Step 7: Verify build**
- [ ] **Step 8: Commit**

---

### Task 14: Run Script + Final Integration

**Files:** `scripts/run.py`, `.gitignore`, health check endpoint

- [ ] **Step 1: Create `scripts/run.py`** — starts uvicorn + vite dev server in parallel, opens browser
- [ ] **Step 2: Add health check endpoint** `/api/health` — returns 200 with DB connection status
- [ ] **Step 3: Create `.gitignore`** — `__pycache__/`, `.env`, `data/`, `node_modules/`, `frontend/dist/`, `.superpowers/`
- [ ] **Step 4: Full integration test** — start app, verify health check, verify questions endpoint returns seed data
- [ ] **Step 5: Commit**

---

## Self-Review Checklist

**Spec coverage:**
- [x] Three LLM roles with separate modules (Tasks 5, 6, 7)
- [x] Data model with all tables and constraints (Task 1, 2)
- [x] Interviewer behavior with elapsed time awareness (Task 5)
- [x] Evaluator with per-message annotations and tool_use (Task 6)
- [x] Coach with correct history mapping and debounced analysis (Task 7)
- [x] Speech module (Task 4)
- [x] State machine (Task 8)
- [x] Decomposed WebSocket handler via Orchestrator (Task 9, 10)
- [x] Text input fallback (Task 13)
- [x] Audio chunk storage with chunk index (Task 8)
- [x] Tracing with file export and widget (Task 11, 14)
- [x] All five frontend screens (Tasks 13, 14)
- [x] Seed questions (Task 3)
- [x] Error propagation from background tasks (Convention + Tasks 10, 14)
- [x] Migration strategy (Task 1)
- [x] Robust LLM JSON parsing (Task 4)
- [x] Dependency injection throughout (Convention + Task 1, 10)

**v1 bugs fixed:**
- [x] Coach `build_history_summary` logic error — correct session→question mapping
- [x] Audio chunk overwrite — chunk index in filename
- [x] `stream_tts` closure capture — instance method on Orchestrator
- [x] `elapsed_sec` not interpolated — included in prompt template
- [x] `btoa` crash — FileReader-based encoding
- [x] Audio playback overlap — queued playback via AudioContext
- [x] LLM JSON parsing fragility — tool_use + parse_llm_json fallback
- [x] Background task error swallowing — status field + frontend timeout
- [x] Settings re-parsed inline — dependency injection
- [x] Global mutable DB pool — app.state + Depends()
- [x] useEffect stale closures — useRef for callback
- [x] Coach fires on every Home load — debounce via timestamp check
- [x] Dependency versions — >= lower bounds
- [x] No migration path — migrations/ directory

**v2→v3 fixes (from second review):**
- [x] Sequential message queue eliminates concurrency bugs by construction
- [x] Audio protocol clarified: browser owns recording buffer, server receives final blob
- [x] Semantic validation for LLM responses (not just syntactic)
- [x] Coach advisory lock prevents concurrent analyses
- [x] `createdb` via asyncpg SQL, not shell command
- [x] Frontend shows "Processing..." during pipeline latency
- [x] Connection loss acknowledged with session preservation, no phantom resume
- [x] TTS playback via MediaSource + SourceBuffer (not decodeAudioData)
- [x] Single stylesheet instead of CSS modules
- [x] Seeds merged into Task 1 (14 tasks total, not 15)
- [x] Error-path tests for orchestrator: empty transcription, Claude stream failure

**Known limitations:**
- Context window summarization for 60+ minute sessions not implemented
- No audio playback of past sessions in browser (audio is stored on disk)
- Speech tests mock the API — integration tests require API keys
- No frontend test infrastructure (Vitest + React Testing Library would be a good follow-up)
- 30+ repetitive CRUD functions in database.py — acknowledged as maintenance surface, acceptable for a tool this size
- No automatic interview resume after connection loss (v2 feature)
