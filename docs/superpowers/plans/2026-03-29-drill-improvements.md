# Drill Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the drill app stateless (survive page refreshes), add session archiving with bulk UX, multi-segment recording, an Educator LLM role (Opus), and LLM token tracking.

**Architecture:** Refactor the orchestrator to always load from DB (no "start" vs "resume"). Add `archived`, `tts_enabled` columns to sessions, `raw_response` to messages, educator columns to evaluations. New Educator module. Frontend URL scheme changes to `/sessions/:id`. Multi-segment recording in useAudio.

**Tech Stack:** Same as existing — Python/FastAPI/asyncpg/Anthropic/OpenAI/React/TypeScript

**Design spec:** `docs/superpowers/specs/2026-03-29-drill-improvements-design.md`

---

## Conventions

All conventions from the original plan apply. Additionally:

- Read the design spec for full requirements before implementing any task
- Read existing files before modifying — follow established patterns
- The orchestrator refactor (Task 2) is the most complex and most important task. Get it right.
- For DB queries: all existing functions are in `backend/database.py`, all take `conn: asyncpg.Connection` as first arg
- For routes: all use `Depends(get_db)` from `backend/deps.py`
- Frontend: single stylesheet `global.css`, no inline styles, no CSS modules

---

### Task 1: Database Migration + Model Updates

**Files:**
- Create: `backend/migrations/002_improvements.sql`
- Modify: `backend/models.py`
- Modify: `backend/database.py`

- [ ] **Step 1: Create migration `002_improvements.sql`**

```sql
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

-- Convenience view for token aggregation
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

- [ ] **Step 2: Run migration**

```bash
PYTHONPATH=. python scripts/init_db.py
```

- [ ] **Step 3: Update `backend/models.py`**

Add `archived: bool = False` and `tts_enabled: bool = True` to both `SessionCreate` and `Session` models.

Add `educator_model_answer: str | None = None`, `educator_gap_deepdives: str | None = None`, `educator_raw_response: dict | None = None` to `Evaluation` model.

Add `raw_response: dict | None = None` to both `MessageCreate` and `Message` models.

- [ ] **Step 4: Update `backend/database.py`**

Update `_row_to_session` to include `archived` and `tts_enabled`.
Update `_row_to_message` to include `raw_response` (json.loads if not None).
Update `_row_to_evaluation` to include educator fields.

Add new functions:
- `archive_session(conn, session_id, archived: bool)` — UPDATE sessions SET archived = $2
- `archive_sessions_bulk(conn, session_ids: list[int], archived: bool)` — UPDATE ... WHERE id = ANY($1)
- `list_sessions(conn, include_archived: bool = False)` — add `WHERE archived = false` unless include_archived
- `get_session_token_usage(conn, session_id)` — query the `llm_token_usage` view filtered by session_id

Update `get_dimension_averages` and `get_question_stats` to filter `WHERE archived = false` on joined sessions.

Update `insert_message` to accept and store `raw_response` (json.dumps if not None).

- [ ] **Step 5: Write tests for new DB functions**

Test archive/unarchive, bulk archive, filtered list_sessions, token usage view.

- [ ] **Step 6: Run all tests, verify pass**
- [ ] **Step 7: Commit**

---

### Task 2: Orchestrator Stateless Refactor

**Files:**
- Modify: `backend/orchestrator.py`
- Modify: `backend/messages.py`
- Modify: `backend/routes/ws.py`
- Modify: `tests/test_orchestrator.py`

This is the most complex task. The orchestrator currently has `_do_start` which creates a session in DB. After this refactor, session creation happens via REST API before the WebSocket connects. The orchestrator always loads session state from DB.

- [ ] **Step 1: Update `backend/messages.py`**

Replace the `start` message type with `load`:
```python
# Client sends: {"type": "load", "session_id": 5}
# Instead of: {"type": "start", "question_id": 5, "timer_sec": 2700, ...}
```

Add `session_id` field to WSMessage.

- [ ] **Step 2: Write failing tests for the new orchestrator**

```python
async def test_load_session_with_no_messages_generates_opening(mock_deps):
    """Loading a fresh session (no messages) generates the opening question."""
    # Insert a session in mock DB, no messages
    # Enqueue: load(session_id=1), shutdown
    # Assert: interviewer_text appears in results

async def test_load_session_with_messages_resumes(mock_deps):
    """Loading a session with existing messages resumes at WAITING_FOR_CANDIDATE."""
    # Insert a session with 3 messages in mock DB
    # Enqueue: load(session_id=1), text_input("test"), shutdown
    # Assert: no opening generated, interviewer responds to "test"

async def test_elapsed_time_from_db(mock_deps):
    """Elapsed time uses sessions.started_at, not in-memory time."""
    # Insert a session with started_at = 10 minutes ago
    # Load it, verify system prompt contains ~600 seconds elapsed
```

- [ ] **Step 3: Refactor `backend/orchestrator.py`**

Remove `_do_start` and `_do_start_inner`. Replace with `_do_load`:

```python
async def _do_load(self, msg: WSMessage, send: SendFn) -> None:
    """Load session state from DB. Always the first message."""
    async with self.deps.pool.acquire() as conn:
        session = await get_session(conn, msg.session_id)
        if not session or session.status != SessionStatus.active.value:
            await self._send(send, {"type": "error", "message": "Session not found or not active"})
            return

        self._session_id = session.id
        self._question = await get_question(conn, session.question_id)
        self._timer_sec = session.timer_setting_sec
        self._tts_enabled = session.tts_enabled
        self._session_dir = session.audio_dir or self.deps.storage.create_session_dir(session.id)

        # Update audio_dir if it wasn't set
        if not session.audio_dir:
            await update_session_status(conn, session.id, session.status, audio_dir=self._session_dir)

        # Load messages to determine sequence and state
        messages = await get_session_messages(conn, session.id)
        self._sequence = max((m.sequence for m in messages), default=0)
        candidate_seqs = [m.sequence for m in messages if m.role == MessageRole.candidate]
        self._last_candidate_sequence = max(candidate_seqs, default=0)

        # Briefing
        self._briefing = None
        if session.interviewer_briefed:
            review = await get_latest_coach_review(conn)
            if review:
                self._briefing = review.recommendation

    # Elapsed time from DB timestamp
    elapsed = int((datetime.now(timezone.utc) - session.started_at).total_seconds())

    # Build system prompt
    self._system_prompt = self._interviewer.build_system_prompt(
        question_title=self._question.title,
        question_prompt=self._question.prompt,
        timer_sec=self._timer_sec,
        elapsed_sec=elapsed,
        briefing=self._briefing,
    )

    # If no messages exist, generate the opening question
    if self._sequence == 0:
        self._state.transition(SessionState.STARTING)
        self._state.transition(SessionState.INTERVIEWER_SPEAKING)

        full_response = await self._stream_interviewer_opening(send)
        self._sequence += 1
        async with self.deps.pool.acquire() as conn:
            await insert_message(conn, MessageCreate(
                session_id=self._session_id,
                sequence=self._sequence,
                role=MessageRole.interviewer,
                content=full_response,
            ))

        if self._tts_enabled:
            self._tts_task = asyncio.create_task(self._stream_tts(full_response, send))
        else:
            await self._send(send, {"type": "interviewer_done"})

        self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
    else:
        # Resume: skip directly to WAITING_FOR_CANDIDATE
        self._state.transition(SessionState.STARTING)
        self._state.transition(SessionState.INTERVIEWER_SPEAKING)
        self._state.transition(SessionState.WAITING_FOR_CANDIDATE)

        # Send existing messages to client for chat repopulation
        async with self.deps.pool.acquire() as conn:
            messages = await get_session_messages(conn, self._session_id)
        for m in messages:
            await self._send(send, {
                "type": "message_history",
                "sequence": m.sequence,
                "role": m.role.value if hasattr(m.role, 'value') else m.role,
                "content": m.content,
                "timestamp": m.timestamp.isoformat() if m.timestamp else None,
            })

    await self._send(send, {
        "type": "session_loaded",
        "session_id": self._session_id,
        "started_at": session.started_at.isoformat(),
        "timer_sec": self._timer_sec,
        "tts_enabled": self._tts_enabled,
    })
    await self._send(send, {"type": "state", "state": self._state.state.value})
```

Update `_handle` to route `"load"` instead of `"start"`.

Update `_respond_as_interviewer` to compute elapsed from DB:
```python
# Instead of: self._state.elapsed_seconds
# Use: int((datetime.now(timezone.utc) - session_started_at).total_seconds())
# Store session.started_at on self during _do_load
```

- [ ] **Step 4: Update `backend/routes/ws.py`**

The WS handler no longer needs to receive question_id. It receives session_id:

```python
@router.websocket("/ws/interview/{session_id}")
async def interview_websocket(ws: WebSocket, session_id: int):
    await ws.accept()
    # ... create orchestrator
    # Immediately enqueue load message
    await orch.enqueue(WSMessage(type="load", session_id=session_id))
    runner = asyncio.create_task(orch.run(send))
    # ... rest same
```

The session_id comes from the URL path, not from a client message. The client just connects to the WS and receives session state.

- [ ] **Step 5: Add `POST /api/sessions` for session creation in routes/sessions.py**

The existing endpoint is `GET /api/sessions` (list). Add POST:

```python
@router.post("", response_model=Session)
async def create_session(body: SessionCreate, db: asyncpg.Connection = Depends(get_db)) -> Session:
    return await insert_session(db, body)
```

This is what the frontend calls when the user picks a question on the Home page. The response includes the session ID, and the frontend navigates to `/sessions/:id`.

- [ ] **Step 6: Update `backend/routes/sessions.py`** to add `include_archived` query param

```python
@router.get("")
async def list_all_sessions(
    include_archived: bool = Query(default=False),
    db = Depends(get_db),
):
    return await list_sessions(db, include_archived=include_archived)
```

- [ ] **Step 7: Run all tests, fix breakages**
- [ ] **Step 8: Commit**

---

### Task 3: Archive Routes

**Files:**
- Modify: `backend/routes/sessions.py`
- Create: `tests/test_archive.py` (or add to test_routes.py)

- [ ] **Step 1: Add archive endpoints to `backend/routes/sessions.py`**

```python
@router.patch("/{session_id}/archive")
async def archive_session_endpoint(session_id: int, db = Depends(get_db)):
    await archive_session(db, session_id, archived=True)
    return {"status": "archived"}

@router.patch("/{session_id}/unarchive")
async def unarchive_session_endpoint(session_id: int, db = Depends(get_db)):
    await archive_session(db, session_id, archived=False)
    return {"status": "unarchived"}

@router.post("/archive-bulk")
async def archive_bulk(body: dict, db = Depends(get_db)):
    await archive_sessions_bulk(db, body["session_ids"], archived=True)
    return {"status": "archived", "count": len(body["session_ids"])}
```

- [ ] **Step 2: Add `force` param to coach analyze**

In `backend/routes/coach_routes.py`, add `force: bool = Query(default=False)` to the analyze endpoint. If force, skip the debounce check.

- [ ] **Step 3: Write tests for archive endpoints**
- [ ] **Step 4: Run tests, verify pass**
- [ ] **Step 5: Commit**

---

### Task 4: Educator Module

**Files:**
- Create: `backend/educator.py`
- Modify: `backend/routes/evaluation.py` (add educator endpoints)
- Create: `tests/test_educator.py`

- [ ] **Step 1: Write failing tests for educator**

```python
def test_educator_tool_schema():
    educator = Educator()
    tools = educator.get_tool_schema()
    assert tools[0]["name"] == "submit_education"
    props = tools[0]["input_schema"]["properties"]
    assert "model_answer" in props
    assert "gap_deepdives" in props

def test_educator_builds_prompt_with_gaps():
    educator = Educator()
    prompt = educator.build_prompt(
        question_title="URL Shortener",
        question_prompt="Design a URL shortener.",
        transcript_text="...",
        evaluation_summary="Gaps: no cache invalidation discussion",
    )
    assert "URL Shortener" in prompt
    assert "cache invalidation" in prompt
```

- [ ] **Step 2: Implement `backend/educator.py`**

```python
@dataclass
class EducatorConfig:
    model: str = "claude-opus-4-6"
    max_tokens: int = 8000

EDUCATOR_TOOL = {
    "name": "submit_education",
    "description": "Submit the educational analysis.",
    "input_schema": {
        "type": "object",
        "required": ["model_answer", "gap_deepdives"],
        "properties": {
            "model_answer": {"type": "string", "description": "Markdown: what a strong answer looks like"},
            "gap_deepdives": {"type": "string", "description": "Markdown: detailed explanation for each gap"},
        },
    },
}

class Educator:
    def __init__(self, config: EducatorConfig | None = None):
        self.config = config or EducatorConfig()

    def get_tool_schema(self) -> list[dict]: ...
    def build_prompt(self, question_title, question_prompt, transcript_text, evaluation_summary) -> str: ...
    async def educate(self, client, question_title, question_prompt, transcript_text, evaluation_summary) -> tuple[str, str, dict]: ...
        # Returns (model_answer_md, gap_deepdives_md, raw_response)
```

System prompt: "You are an expert system design educator. Given an interview transcript and evaluation, produce two things: (1) a model answer showing what a strong response looks like for this specific problem, (2) detailed technical deep-dives on each gap identified by the evaluator..."

The prompt should emphasize: concrete over abstract, real-world over theoretical, implementation details over hand-waving. This is what distinguishes it from the evaluator.

- [ ] **Step 3: Add educator endpoints to routes**

In `backend/routes/evaluation.py`:

```python
@router.post("/{session_id}/educate")
async def trigger_educator(session_id: int, background_tasks, db, settings):
    # Check evaluation exists
    # Run educator in background
    # Save model_answer, gap_deepdives, educator_raw_response to evaluations table

@router.get("/{session_id}/educator")
async def get_educator_content(session_id: int, db):
    # Return educator_model_answer + educator_gap_deepdives from latest evaluation
    # Return null if not yet generated
```

- [ ] **Step 4: Run tests, verify pass**
- [ ] **Step 5: Commit**

---

### Task 5: LLM Token Capture in Orchestrator

**Files:**
- Modify: `backend/orchestrator.py`
- Modify: `backend/interviewer.py`

- [ ] **Step 1: Update interviewer streaming to capture usage**

The Anthropic streaming API provides usage in the final message event. Update `get_response_stream` to capture and return it:

```python
async def get_response_stream(self, client, system_prompt, messages):
    """Yields (token, None) for text, then (None, usage_dict) at the end."""
    async with client.messages.stream(...) as stream:
        async for text in stream.text_stream:
            yield text
    # After stream completes, get the final message with usage
    final = await stream.get_final_message()
    # Store usage on self or return it somehow
```

The cleanest approach: have `_respond_as_interviewer` capture the final message usage and pass it to `insert_message` as `raw_response`.

- [ ] **Step 2: Update orchestrator to store raw_response on interviewer messages**

In `_respond_as_interviewer`, after streaming completes, get the usage from the stream's final message and pass it to `insert_message`:

```python
async with self.deps.pool.acquire() as conn:
    await insert_message(conn, MessageCreate(
        session_id=self._session_id,
        sequence=self._sequence,
        role=MessageRole.interviewer,
        content=full_response,
        raw_response=final_message_dict,  # includes usage
    ))
```

- [ ] **Step 3: Verify raw_response is populated by checking DB after a test session**
- [ ] **Step 4: Commit**

---

### Task 6: Frontend URL Refactor + Stateless Interview

**Files:**
- Modify: `frontend/src/App.tsx`
- Modify: `frontend/src/pages/Interview.tsx`
- Modify: `frontend/src/pages/Home.tsx`
- Modify: `frontend/src/pages/SessionReview.tsx`
- Modify: `frontend/src/pages/Results.tsx`
- Modify: `frontend/src/api/client.ts`
- Modify: `frontend/src/api/ws.ts`
- Modify: `frontend/src/hooks/useWebSocket.ts`
- Modify: `frontend/src/types.ts`

- [ ] **Step 1: Update routes in `App.tsx`**

```tsx
<Route path="/" element={<Home />} />
<Route path="/history" element={<History />} />
<Route path="/sessions/:sessionId" element={<SessionPage />} />
<Route path="/sessions/:sessionId/learn" element={<Learn />} />
```

Remove `/interview/:questionId`, `/results/:sessionId`, `/session/:sessionId`. Replace with unified `/sessions/:id`.

- [ ] **Step 2: Create `SessionPage` component**

This component checks session status and renders the appropriate UI:
- `active` → Interview UI
- `completed` / `evaluating` → Results UI (polling for evaluation)
- `reviewed` / `evaluation_failed` → Session Review UI

```tsx
export default function SessionPage() {
    const { sessionId } = useParams();
    const [session, setSession] = useState<Session | null>(null);

    useEffect(() => { api.sessions.get(Number(sessionId)).then(setSession); }, [sessionId]);

    if (!session) return <div>Loading...</div>;

    switch (session.status) {
        case "active": return <Interview sessionId={session.id} session={session} />;
        case "completed":
        case "evaluating": return <Results sessionId={session.id} />;
        default: return <SessionReview sessionId={session.id} />;
    }
}
```

- [ ] **Step 3: Refactor Interview.tsx**

The interview page now receives `sessionId` and `session` as props (not from URL params). It no longer creates sessions.

On mount:
1. Connect WebSocket to `/ws/interview/{sessionId}`
2. The server-side orchestrator loads the session and sends `message_history` for each existing message, then `session_loaded` with metadata (started_at, timer_sec, tts_enabled)
3. Client populates chat from `message_history` messages
4. Client sets timer from `session_loaded.started_at` (elapsed = Date.now() - new Date(started_at))
5. No "Begin Interview" button needed — the session is already created

If the session has no messages yet (fresh), the orchestrator generates the opening question automatically.

Remove: `handleBegin`, the "Begin Interview" screen, the `started` state. The interview starts immediately on mount.

Keep: mic init (call on mount instead of on begin click — use an `AudioContext.resume()` call triggered by a user gesture if needed).

- [ ] **Step 4: Update Home.tsx**

Session creation moves here:
```tsx
async function handleStartSession(questionId: number) {
    const session = await api.sessions.create({
        question_id: questionId,
        timer_setting_sec: 2700,
        tts_enabled: true,
    });
    navigate(`/sessions/${session.id}`);
}
```

Update QuestionList to call `handleStartSession` instead of navigating to `/interview/:id`.

- [ ] **Step 5: Update `api/client.ts`**

Add `sessions.create(body: SessionCreate)` endpoint.
Update `evaluate.trigger` and `coach` paths if needed.
Add `sessions.archive`, `sessions.unarchive`, `sessions.archiveBulk`.
Add `sessions.educator` and `sessions.triggerEducator`.

- [ ] **Step 6: Update `api/ws.ts`**

WebSocket URL changes from `/ws/interview` to `/ws/interview/{sessionId}`:
```typescript
connect(sessionId: number): Promise<void> {
    const url = `${proto}//${host}/ws/interview/${sessionId}`;
    // ...
}
```

- [ ] **Step 7: Update `useWebSocket.ts`**

`connect` now takes `sessionId: number`.

- [ ] **Step 8: Add `message_history` and `session_loaded` to `WSServerMessage` types in `types.ts`**

- [ ] **Step 9: Handle `message_history` and `session_loaded` in Interview.tsx message handler**

```tsx
case "message_history":
    setMessages(prev => [...prev, {
        role: msg.role,
        content: msg.content,
        isStreaming: false,
    }]);
    break;
case "session_loaded":
    setTimerStartedAt(new Date(msg.started_at));
    break;
```

- [ ] **Step 10: Update `useTimer.ts`**

Timer should accept a `startedAt: Date` and compute elapsed = `(Date.now() - startedAt.getTime()) / 1000`. No more `start()` — just set the reference time.

- [ ] **Step 11: Update Results.tsx and SessionReview.tsx**

These may receive sessionId as props from SessionPage rather than from URL params. Adjust accordingly.

- [ ] **Step 12: Verify frontend build, run all backend tests**
- [ ] **Step 13: Commit**

---

### Task 7: Multi-Segment Recording

**Files:**
- Modify: `frontend/src/hooks/useAudio.ts`
- Modify: `frontend/src/pages/Interview.tsx`
- Modify: `frontend/src/components/AudioControls.tsx`
- Modify: `frontend/src/global.css`

- [ ] **Step 1: Update `useAudio.ts`**

Add segment tracking:
```typescript
const segmentsRef = useRef<Blob[]>([]);
const [pendingSegments, setPendingSegments] = useState(0);
const [pendingDuration, setPendingDuration] = useState(0);
```

Modify `stopRecording`: instead of encoding and returning base64, just append the blob to `segmentsRef` and update counts. Return void.

Add `submitRecording`: concatenate all segments, encode, return base64.
Add `discardRecording`: clear segments, reset counts.

```typescript
const stopRecording = useCallback(async () => {
    // Stop recorder, get blob, append to segmentsRef
    // Update pendingSegments and pendingDuration
    // Do NOT encode or return — just buffer
}, []);

const submitRecording = useCallback(async (): Promise<string> => {
    const combined = new Blob(segmentsRef.current, { type: "audio/webm" });
    segmentsRef.current = [];
    setPendingSegments(0);
    setPendingDuration(0);
    return blobToBase64(combined);
}, []);

const discardRecording = useCallback(() => {
    segmentsRef.current = [];
    setPendingSegments(0);
    setPendingDuration(0);
}, []);
```

- [ ] **Step 2: Update Interview.tsx key handlers**

```
Spacebar down → startRecording()
Spacebar up → stopRecording() (appends segment, no submit)
Enter → submitRecording() → send end_turn
Escape → discardRecording()
```

When `pendingSegments > 0`, show the segment status bar.

- [ ] **Step 3: Update AudioControls.tsx**

Add props for `pendingSegments` and `pendingDuration`. Show status bar when segments are pending:

```
● 2 segments (8.3s) — SPACE for more | ENTER to submit | ESC to discard
```

- [ ] **Step 4: Add CSS for pending segments status bar**
- [ ] **Step 5: Verify build**
- [ ] **Step 6: Commit**

---

### Task 8: History Page Bulk Archive

**Files:**
- Modify: `frontend/src/pages/History.tsx`
- Modify: `frontend/src/api/client.ts` (if not done in Task 6)
- Modify: `frontend/src/global.css`

- [ ] **Step 1: Add archive filter and selection to History.tsx**

Filter toggle: Active (default) | Archived | All — controls query param to `api.sessions.list`.

Checkbox on each session row. Selection state tracked in component:
```typescript
const [selected, setSelected] = useState<Set<number>>(new Set());
const [filter, setFilter] = useState<"active" | "archived" | "all">("active");
```

- [ ] **Step 2: Add action bar when items selected**

Shows when `selected.size > 0`:
```
"N selected — [Archive] [Select All] [Clear]"
```

When viewing Archived filter, action changes to "Unarchive".

"Archive" calls `api.sessions.archiveBulk(Array.from(selected))`, refreshes list.

- [ ] **Step 3: Add CSS for selection UI**
- [ ] **Step 4: Verify build**
- [ ] **Step 5: Commit**

---

### Task 9: Educator Frontend + Learn Page

**Files:**
- Create: `frontend/src/pages/Learn.tsx`
- Modify: `frontend/src/pages/SessionReview.tsx`
- Modify: `frontend/src/App.tsx` (route already added in Task 6)
- Add dependency: `react-markdown`

- [ ] **Step 1: Install react-markdown**

```bash
cd frontend && npm install react-markdown
```

- [ ] **Step 2: Create `Learn.tsx`**

Full-page markdown renderer:
```tsx
export default function Learn() {
    const { sessionId } = useParams();
    const [content, setContent] = useState<{model_answer: string, gap_deepdives: string} | null>(null);

    useEffect(() => {
        api.sessions.educator(Number(sessionId)).then(setContent);
    }, [sessionId]);

    if (!content) return <div>Loading...</div>;

    return (
        <div className="learn-page">
            <header>
                <button onClick={() => navigate(`/sessions/${sessionId}`)}>Back to Review</button>
                <h1>Deep Analysis</h1>
            </header>
            <section>
                <h2>Model Answer</h2>
                <ReactMarkdown>{content.model_answer}</ReactMarkdown>
            </section>
            <section>
                <h2>Gap Deep-Dives</h2>
                <ReactMarkdown>{content.gap_deepdives}</ReactMarkdown>
            </section>
        </div>
    );
}
```

- [ ] **Step 3: Add "Generate Deep Analysis" button to SessionReview.tsx**

```tsx
const [educatorContent, setEducatorContent] = useState(null);
const [educatorLoading, setEducatorLoading] = useState(false);

// On mount, check if educator content exists
useEffect(() => {
    api.sessions.educator(sessionId).then(setEducatorContent);
}, [sessionId]);

// In render:
{educatorContent ? (
    <button onClick={() => navigate(`/sessions/${sessionId}/learn`)}>View Deep Analysis</button>
) : (
    <button onClick={handleGenerateEducator} disabled={educatorLoading}>
        {educatorLoading ? "Generating..." : "Generate Deep Analysis (Opus)"}
    </button>
)}
```

- [ ] **Step 4: Add CSS for learn page (markdown styling)**
- [ ] **Step 5: Verify build**
- [ ] **Step 6: Commit**

---

### Task 10: Coach Refresh + Remaining Polish

**Files:**
- Modify: `frontend/src/components/CoachCard.tsx`
- Modify: `frontend/src/api/client.ts` (if not done)

- [ ] **Step 1: Add refresh button to CoachCard**

```tsx
<button onClick={() => api.coach.analyze(true).then(onRefresh)}>
    Refresh Analysis
</button>
```

The `api.coach.analyze` function passes `?force=true` when the argument is true.

- [ ] **Step 2: Update Home.tsx to handle coach refresh**

Pass an `onRefresh` callback to CoachCard that re-fetches the latest review.

- [ ] **Step 3: Verify build**
- [ ] **Step 4: Run all tests**
- [ ] **Step 5: Commit**

---

## Self-Review

**Spec coverage:**
- [x] Stateless architecture — Task 2 (orchestrator), Task 6 (frontend)
- [x] Session URL scheme `/sessions/:id` — Task 6
- [x] Page refresh resilience — Task 2 (DB load) + Task 6 (frontend)
- [x] Timer from DB `started_at` — Task 2 (orchestrator) + Task 6 (useTimer)
- [x] Archive sessions — Task 1 (DB) + Task 3 (routes) + Task 8 (frontend)
- [x] Bulk archive UX — Task 8
- [x] Multi-segment recording — Task 7
- [x] Educator role (Opus) — Task 4
- [x] Educator UI + Learn page — Task 9
- [x] LLM token tracking — Task 1 (DB) + Task 5 (capture)
- [x] Coach refresh button — Task 10
- [x] `tts_enabled` persisted — Task 1 (DB) + Task 2 (orchestrator loads it)
- [x] `raw_response` on messages — Task 1 (DB) + Task 5 (capture)

**Known dependencies:**
- Task 1 must be first (DB migration)
- Task 2 depends on Task 1 (orchestrator uses new columns)
- Task 3 depends on Task 1 (archive queries)
- Task 4 is independent after Task 1
- Task 5 depends on Task 2 (orchestrator changes)
- Task 6 depends on Task 2 (WS protocol changes)
- Task 7 is frontend-only, independent after Task 6
- Task 8 depends on Task 3 + Task 6
- Task 9 depends on Task 4 + Task 6
- Task 10 depends on Task 3 + Task 6
