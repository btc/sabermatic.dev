# Plan Critique: System Design Drill Implementation Plan

**Plan reviewed:** `docs/superpowers/plans/2026-03-28-system-design-drill.md`
**Date:** 2026-03-28

---

## Architecture & Design Issues

### 1. PostgreSQL is overkill for a local-only tool

The plan specifies PostgreSQL with asyncpg, connection pools, and a `createdb` step. This is a **local voice-drill app for one person**. SQLite would eliminate an entire dependency (running Postgres), simplify setup from "install and configure Postgres, create database, run schema" to "it just works." The plan never justifies why Postgres is needed — there's no concurrent write contention, no need for `LISTEN/NOTIFY`, no multi-user access, nothing that SQLite can't handle. The `asyncpg.Pool(min_size=2, max_size=10)` is absurd for a single-user local app.

### 2. No text input fallback — voice-only is fragile

The entire interview flow is designed around push-to-talk audio. The WebSocket handler *technically* has a `text` field on `end_turn`, but the frontend has no text input UI. If Whisper is slow, the mic doesn't work, or you're in a noisy environment, you're stuck. For a practice tool, text input should be a first-class citizen, not a hidden message field.

### 3. The WebSocket handler is a 230-line monolith

`routes/ws.py` is a single function with deeply nested conditionals, inline closures (`stream_tts`), multiple `nonlocal` variables, and `# type: ignore` annotations sprinkled throughout. This will be extremely difficult to test, debug, or modify. The plan has **no tests for the WebSocket handler** — `test_routes.py` only tests REST endpoints. The most complex piece of the system is completely untested.

### 4. Global mutable state in `database.py`

```python
_pool: asyncpg.Pool | None = None
```

A global variable with `get_pool()` that asserts it's not None. This makes testing awkward (the `conftest.py` works around it by passing connections directly, bypassing the pool entirely), makes the app non-reentrant, and is a classic anti-pattern. FastAPI has dependency injection (`Depends`) — use it.

### 5. `Settings()` instantiated inline everywhere

`Settings()` is called inside route handlers and background tasks:

```python
settings = Settings()  # type: ignore[call-arg]
```

That `# type: ignore` appears **four times**. The settings should be injected via `Depends` or stored on the app at startup, not re-parsed from `.env` on every request. The type-ignore also signals the plan knows this code won't typecheck.

---

## Correctness Bugs

### 6. Coach `build_history_summary` has a logic bug

Line 2297:

```python
if any(e.session_id for e in evaluations):
    attempted_tags.update(q.tags)
```

This adds tags from **every** question if **any** evaluation exists — the condition doesn't check if the question was actually attempted. It should be checking if any evaluation's `session_id` maps to a session for that question. The `question_map` is keyed by `q.id` but evaluations have `session_id`, not `question_id`, so the mapping is also wrong (line 2268: `question_map = {q.id: q for q in questions}` then `q = question_map.get(ev.session_id)` — this looks up a session ID in a question-ID-keyed map).

### 7. Audio chunk saving overwrites previous data

In the WebSocket handler, `save_audio_chunk` is called for TTS output with the same `sequence` number for the opening statement:

```python
storage.save_audio_chunk(session_dir, "out", sequence, chunk, "mp3")
```

But TTS streams multiple chunks — each chunk overwrites `turn_001.mp3`. The storage layer writes to a single file path per turn, not appending or using chunk indices.

### 8. `stream_tts` closure captures stale variables

Line 3148:

```python
tts_task = asyncio.create_task(stream_tts(full_response))
```

But `stream_tts` is defined inside the `"start"` message handler block. When used in the `"end_turn"` block, `stream_tts` references the closure from the `"start"` scope. It might not even be defined if the code path changes. This is confirmed by `# type: ignore[possibly-undefined]`.

### 9. `elapsed_sec` is passed to the system prompt but never used in the template

The `Interviewer.build_system_prompt` accepts `elapsed_sec` but the `SYSTEM_PROMPT_TEMPLATE` doesn't include it. The prompt says "Current elapsed time will be provided to you" but never actually interpolates it. The interviewer has no idea how much time has passed.

### 10. The `playAudioChunk` function doesn't queue

Each chunk creates a new `Audio()` element and calls `.play()`. Multiple chunks arriving rapidly will play simultaneously/overlap, producing garbled audio. There's no queuing, no AudioContext-based buffering, no waiting for one chunk to finish before playing the next.

---

## Testing Gaps

### 11. Tests are mostly import-level, not behavioral

Most test steps follow: "write test that imports -> verify ImportError -> implement -> verify pass." The actual tests are thin:

- `test_settings_loads_defaults` — checks defaults exist
- `test_question_create_validation` — checks a field value
- `test_file_span_exporter_writes_json` — checks a directory exists (doesn't test any export)

There's no test for the evaluator actually parsing LLM output. No test for the coach analyzing multiple sessions. No test for the state machine + WebSocket interaction. No test for TTS interruption. No integration test that exercises the actual interview loop.

### 12. Mock-heavy speech tests test nothing

```python
mock_client.audio.transcriptions.create = AsyncMock(return_value=mock_response)
result = await transcribe_audio(mock_client, b"fake-audio-data", "webm")
assert result == "Design a URL shortener service"
```

This test asserts that if you mock the return value, you get the mocked value back. It doesn't test error handling, retry logic, format negotiation, or anything the function actually does.

---

## Frontend Issues

### 13. Inline styles everywhere, no CSS system

Every component uses inline `style={{}}` objects. This is:

- Not extractable or reusable
- Creates new objects on every render (minor perf issue, major readability issue)
- Makes the plan 2x longer than necessary (hundreds of lines of style objects)
- Can't handle hover states, media queries, or animations properly

The plan installs Vite but doesn't set up Tailwind, CSS modules, or any styling system. The `index.css` is mentioned but never shown.

### 14. `useEffect` with empty deps calling `ws.connect()` + `ws.send()`

```javascript
useEffect(() => {
    (async () => {
      await ws.connect();
      ws.send({ type: "start", ... });
      timer.start();
    })();
    return () => ws.disconnect();
}, []);  // eslint-disable-line react-hooks/exhaustive-deps
```

The ESLint suppression is a red flag. `ws`, `timer`, `questionId`, and `timerTotal` are all dependencies. More importantly, this fires an async operation inside useEffect with no error handling — if `connect()` fails, there's no user feedback.

### 15. `btoa(String.fromCharCode(...new Uint8Array(buffer)))` will crash on large audio

This is in the push-to-talk keyup handler:

```javascript
const base64 = btoa(String.fromCharCode(...new Uint8Array(buffer)));
```

The spread operator creates an argument list from the entire audio buffer. For even a few seconds of audio (~100KB+), this will hit the maximum call stack size and throw a `RangeError`. This is a well-known JavaScript gotcha.

### 16. The Home page triggers coach analysis on every load

```javascript
useEffect(() => {
    api.coach.analyze().catch(() => null);
}, []);
```

Every time you navigate home, it fires an LLM call. This burns API credits, is slow, and produces duplicate coach reviews. There's no debounce, no check for whether new sessions exist since the last analysis.

---

## Plan Structure Issues

### 17. Pinned dependency versions are already stale

```
fastapi==0.115.0
anthropic==0.40.0
openai==1.55.0
```

These exact versions don't exist or are outdated. This plan will fail at `pip install` on step 1. Use `>=` lower bounds or at minimum verify the versions exist.

### 18. No error handling for LLM JSON parsing

Both the evaluator and coach do:

```python
raw_text = response.content[0].text
raw = json.loads(raw_text)
```

If the LLM returns malformed JSON (which it will, eventually — markdown fencing, trailing commas, explanatory text), this crashes with an unhelpful `json.JSONDecodeError`. No retry, no extraction, no structured output mode.

### 19. No migration strategy

The plan has a `scripts/init_db.py` with raw DDL. If you ever change the schema (which you will — this is v1), you have to drop and recreate the database, losing all your session history. No Alembic, no versioning, no `ALTER TABLE`.

### 20. The plan is too long and too prescriptive

At 5200 lines with complete source code for every file, this isn't a plan — it's a codebase in markdown. An implementing agent will either copy-paste it (in which case, why have an agent?) or deviate from it (in which case, the excess detail is wasted context). A plan should specify *what* and *why*, leaving *how* to the implementer. The full React component JSX, the full SQL schema, and the full prompt templates could each be 90% shorter as specifications.

---

## Severity Summary

| # | Issue | Severity |
|---|---|---|
| 17 | Dependency versions don't exist | **Blocker** — won't install |
| 6 | Coach logic bug (wrong map key) | **Bug** — will produce wrong recommendations |
| 7 | Audio chunk overwrite | **Bug** — corrupted audio files |
| 8 | `stream_tts` closure capture | **Bug** — undefined reference on 2nd+ turn |
| 9 | `elapsed_sec` never interpolated | **Bug** — interviewer can't pace the session |
| 15 | `btoa` crash on large audio | **Bug** — will crash in normal use |
| 18 | LLM JSON parsing with no fallback | **Reliability** — will crash on ~5-10% of evaluations |
| 3 | WebSocket handler untested monolith | **High** — hardest code to debug, zero coverage |
| 1 | Postgres overkill | **Design smell** — adds setup friction for no benefit |
| 2 | No text input UI | **UX gap** — unusable without working mic |
| 4 | Global mutable DB pool | **Design smell** — hinders testing and reuse |
| 5 | Settings re-parsed on every request | **Design smell** — type errors, wasted work |
| 10 | Audio playback not queued | **Bug** — garbled TTS audio |
| 11 | Tests are import-level only | **Quality** — false confidence |
| 12 | Mock tests test mocks | **Quality** — no real coverage |
| 13 | Inline styles, no CSS system | **Maintainability** — bloats plan and code |
| 14 | useEffect missing deps | **Bug** — potential stale closures |
| 16 | Coach analysis on every Home load | **Waste** — burns API credits |
| 19 | No migration strategy | **Design gap** — schema changes lose data |
| 20 | Plan is a codebase in markdown | **Process** — defeats purpose of a plan |
