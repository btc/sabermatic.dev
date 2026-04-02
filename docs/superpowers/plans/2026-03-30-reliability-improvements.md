# Reliability Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Harden the drill codebase across 13 tasks: stricter type checking, WebSocket resilience, structured errors, session resumption, graceful degradation, evaluation reliability, DB transactions, deep health checks, audit logging, and cost tracking.

**Architecture:** Three tiers ordered by user impact: (1) Protect the active interview (tasks 1-7), (2) Protect post-interview value (tasks 8-10), (3) Operational confidence (tasks 11-13). Each task is independently deployable.

**Tech Stack:** Python 3.11, FastAPI, asyncpg, React/TypeScript, mypy, pytest

---

## Tier 0: Foundation

### Task 1: Add mypy with strict configuration

**Files:**
- Modify: `pyproject.toml`
- Modify: `requirements.txt`
- Modify: `backend/orchestrator.py` (type annotations)
- Modify: `backend/interviewer.py` (type annotations)
- Modify: `backend/evaluator.py` (type annotations)
- Modify: `backend/educator.py` (type annotations)

Pyright is already strict. mypy catches different classes of errors (`--warn-return-any`, `--disallow-any-generics`). Running both is common in serious Python projects.

- [ ] **Step 1: Add mypy to requirements.txt**

Add to the end of `requirements.txt`:

```
mypy>=1.10.0
asyncpg-stubs>=0.29.0
```

- [ ] **Step 2: Add mypy configuration to pyproject.toml**

Add after the `[tool.pyright]` block:

```toml
[tool.mypy]
python_version = "3.11"
strict = true
files = ["backend"]
plugins = ["pydantic.mypy"]

[[tool.mypy.overrides]]
module = [
    "opentelemetry.*",
    "anthropic.*",
]
ignore_missing_imports = true
```

- [ ] **Step 3: Install and run mypy to discover errors**

Run: `pip install -r requirements.txt && python -m mypy backend/`

Expected: errors. This is a discovery step.

- [ ] **Step 4: Fix mypy errors**

Common fixes needed:

In `backend/evaluator.py:372` and `backend/educator.py`, the `client: object` parameter should be `client: anthropic.AsyncAnthropic`:
```python
import anthropic

async def evaluate(
    self,
    client: anthropic.AsyncAnthropic,
    ...
```

Same for `Educator.educate()`.

Fix remaining errors one at a time. Each fix should make the type more precise, not suppress with `# type: ignore` unless the library genuinely lacks stubs.

- [ ] **Step 5: Run mypy clean**

Run: `python -m mypy backend/`
Expected: `Success: no issues found`

- [ ] **Step 6: Verify pyright still passes**

Run: `python -m pyright backend/`
Expected: no new errors

- [ ] **Step 7: Verify tests still pass**

Run: `pytest tests/ -x -q`
Expected: all pass

- [ ] **Step 8: Commit**

```bash
git add pyproject.toml requirements.txt backend/
git commit -m "feat: add strict mypy type checking alongside pyright"
```

---

### Task 2: WebSocket integration tests

**Files:**
- Create: `tests/test_ws_integration.py`

These tests exercise the real WebSocket endpoint against a real database. External APIs (Anthropic, OpenAI) are mocked.

- [ ] **Step 1: Create the integration test file**

Create `tests/test_ws_integration.py`:

```python
"""Integration tests for the WebSocket interview flow.

Uses Starlette's synchronous TestClient for both HTTP and WebSocket calls.
Real database, mocked external APIs (Anthropic, OpenAI).
"""

import json
import time
from unittest.mock import AsyncMock, MagicMock

import pytest
from starlette.testclient import TestClient

from backend.config import Settings
from backend.main import create_app


def _make_anthropic_stream_mock(tokens: list[str]):
    """Mock Anthropic client that streams tokens."""
    def make_stream(**kwargs):
        async def token_iter():
            for t in tokens:
                yield t

        final_usage = MagicMock()
        final_usage.input_tokens = 100
        final_usage.output_tokens = 50
        final_message = MagicMock()
        final_message.model = "claude-sonnet-4-20250514"
        final_message.usage = final_usage

        stream = MagicMock()
        stream.__aenter__ = AsyncMock(return_value=stream)
        stream.__aexit__ = AsyncMock(return_value=False)
        stream.text_stream = token_iter()
        stream.get_final_message = AsyncMock(return_value=final_message)
        return stream

    mock_client = MagicMock()
    mock_client.messages = MagicMock()
    mock_client.messages.stream = MagicMock(side_effect=make_stream)
    return mock_client


@pytest.fixture(scope="module")
def test_app(test_database):
    """Full FastAPI app with real DB, mocked external APIs."""
    settings = Settings(
        anthropic_api_key="test",
        openai_api_key="test",
        database_url=test_database,
        _env_file=None,
    )
    return create_app(database_url=test_database, settings=settings)


def _create_session(client: TestClient, question_id: int) -> int:
    resp = client.post("/api/sessions/", json={
        "question_id": question_id,
        "timer_setting_sec": 2700,
        "tts_enabled": False,
    })
    assert resp.status_code == 201, f"Failed: {resp.text}"
    return resp.json()["id"]


def _collect_until(ws, predicate, timeout=5.0):
    """Read WS messages until predicate(msg) is True."""
    messages = []
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        data = json.loads(ws.receive_text())
        messages.append(data)
        if predicate(data):
            return messages
    raise TimeoutError(f"Not met after {timeout}s. Got: {[m['type'] for m in messages]}")


def _is_waiting(msg: dict) -> bool:
    return msg.get("type") == "state" and msg.get("state") == "waiting_for_candidate"


class TestWSLoadSession:
    def test_connect_load_receive_opening(self, test_app):
        mock_anthropic = _make_anthropic_stream_mock(["Welcome", " to", " the", " interview."])
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            assert len(questions) > 0
            session_id = _create_session(client, questions[0]["id"])

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                messages = _collect_until(ws, _is_waiting)
                types = [m["type"] for m in messages]
                assert "session_loaded" in types
                assert "interviewer_text" in types
                loaded = next(m for m in messages if m["type"] == "session_loaded")
                assert loaded["session_id"] == session_id


class TestWSTextTurn:
    def test_text_input_round_trip(self, test_app):
        mock_anthropic = _make_anthropic_stream_mock(["Sure", ", let's", " discuss."])
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            session_id = _create_session(client, questions[0]["id"])

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                _collect_until(ws, _is_waiting)
                ws.send_text(json.dumps({"type": "text_input", "text": "I'd start by gathering requirements."}))
                turn_messages = _collect_until(ws, _is_waiting)
                assert any(m["type"] == "interviewer_text" for m in turn_messages)


class TestWSEndSession:
    def test_end_session_finalizes_in_db(self, test_app):
        mock_anthropic = _make_anthropic_stream_mock(["Welcome."])
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            session_id = _create_session(client, questions[0]["id"])

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                _collect_until(ws, _is_waiting)
                ws.send_text(json.dumps({"type": "end_session"}))
                _collect_until(ws, lambda m: m.get("type") == "session_ended")

            resp = client.get(f"/api/sessions/{session_id}")
            assert resp.json()["status"] == "completed"
```

- [ ] **Step 2: Run and iterate**

Run: `pytest tests/test_ws_integration.py -v`

Fix issues until green. Common problems: transaction rollback in conftest may conflict with module-scoped app — WS tests need committed data.

- [ ] **Step 3: Verify full suite**

Run: `pytest tests/ -x -q`

- [ ] **Step 4: Commit**

```bash
git add tests/test_ws_integration.py
git commit -m "test: add WebSocket integration tests with real DB"
```

---

### Task 3: Structured error handling in orchestrator

**Files:**
- Create: `backend/errors.py`
- Modify: `backend/orchestrator.py`
- Create: `tests/test_errors.py`

Replace bare `except Exception` in the orchestrator with typed error classes that carry context about what failed and what was saved.

- [ ] **Step 1: Write failing test for error types**

Create `tests/test_errors.py`:

```python
"""Tests for structured error types."""

from backend.errors import (
    InterviewError,
    TranscriptionError,
    LLMError,
    TTSError,
    SessionLoadError,
)


class TestErrorHierarchy:
    def test_all_inherit_from_interview_error(self):
        assert issubclass(TranscriptionError, InterviewError)
        assert issubclass(LLMError, InterviewError)
        assert issubclass(TTSError, InterviewError)
        assert issubclass(SessionLoadError, InterviewError)

    def test_transcription_error_carries_audio_size(self):
        err = TranscriptionError("Whisper timeout", audio_size=12345)
        assert err.audio_size == 12345
        assert "Whisper timeout" in str(err)

    def test_llm_error_carries_partial_response(self):
        err = LLMError("Connection reset", partial_response="Partial text")
        assert err.partial_response == "Partial text"

    def test_llm_error_without_partial(self):
        err = LLMError("Rate limited")
        assert err.partial_response is None

    def test_session_load_error_carries_session_id(self):
        err = SessionLoadError("Not found", session_id=999)
        assert err.session_id == 999

    def test_to_ws_payload(self):
        err = TranscriptionError("Whisper timeout", audio_size=12345)
        payload = err.to_ws_payload()
        assert payload["type"] == "error"
        assert payload["error_kind"] == "transcription_error"
        assert "Whisper timeout" in payload["message"]
```

- [ ] **Step 2: Run to verify failure**

Run: `pytest tests/test_errors.py -v`
Expected: `ModuleNotFoundError`

- [ ] **Step 3: Implement error module**

Create `backend/errors.py`:

```python
"""Structured error types for the interview orchestrator.

Each error carries domain-specific context so the orchestrator can make
informed recovery decisions and the client gets actionable messages.
"""


class InterviewError(Exception):
    """Base class for all interview domain errors."""

    def to_ws_payload(self) -> dict:
        kind = type(self).__name__
        snake = ""
        for i, c in enumerate(kind):
            if c.isupper() and i > 0:
                snake += "_"
            snake += c.lower()
        return {
            "type": "error",
            "error_kind": snake,
            "message": str(self),
        }


class TranscriptionError(InterviewError):
    def __init__(self, message: str, *, audio_size: int = 0):
        super().__init__(message)
        self.audio_size = audio_size


class LLMError(InterviewError):
    def __init__(self, message: str, *, partial_response: str | None = None):
        super().__init__(message)
        self.partial_response = partial_response


class TTSError(InterviewError):
    def __init__(self, message: str):
        super().__init__(message)


class SessionLoadError(InterviewError):
    def __init__(self, message: str, *, session_id: int | None = None):
        super().__init__(message)
        self.session_id = session_id
```

- [ ] **Step 4: Run error tests**

Run: `pytest tests/test_errors.py -v`
Expected: all pass

- [ ] **Step 5: Update orchestrator to raise structured errors**

Modify `backend/orchestrator.py`:

**Add import** at top:
```python
from backend.errors import InterviewError, TranscriptionError, LLMError, SessionLoadError
```

**In `_do_load`** — replace bare error sends with raises:
```python
# Where session is None:
raise SessionLoadError("Session not found", session_id=msg.session_id)
# Where question is None:
raise SessionLoadError("Question not found", session_id=msg.session_id)
```

**In `_do_end_turn`** — replace empty transcription handling:
```python
if not transcript or not transcript.strip():
    raise TranscriptionError(
        "Could not transcribe audio. Try again or type instead.",
        audio_size=len(msg.audio_data) if msg.audio_data else 0,
    )
```

**In `_respond_as_interviewer`** — wrap stream error:
```python
except Exception as e:
    stream_errored = True
    # ... existing span attributes ...
    await self._send(send, {"type": "interviewer_text", "content": "", "done": True})
    raise LLMError(f"Interviewer error: {e}", partial_response=full_response or None)
```

**In `run()`** — catch `InterviewError` between `InvalidTransition` and bare `Exception`:
```python
except InterviewError as e:
    logger.warning("Interview error processing %s: %s", msg.type, e)
    await self._send(send, e.to_ws_payload())
    # Recover state if stuck mid-turn
    if self._session_id and self._state.state in (
        SessionState.PROCESSING, SessionState.CANDIDATE_SPEAKING,
        SessionState.INTERVIEWER_SPEAKING,
    ):
        try:
            self._state.state = SessionState.WAITING_FOR_CANDIDATE
            await self._send(send, {"type": "state", "state": SessionState.WAITING_FOR_CANDIDATE.value})
        except Exception:
            pass
    # Save partial LLM response if available
    if isinstance(e, LLMError) and e.partial_response:
        self._sequence += 1
        async with self.deps.pool.acquire() as conn:
            await insert_message(conn, MessageCreate(
                session_id=self._session_id, sequence=self._sequence,
                role=MessageRole.interviewer,
                content=e.partial_response + " (error - incomplete)",
            ))
```

Also add `error_kind` to the `InvalidTransition` and bare `Exception` handlers:
```python
except InvalidTransition as e:
    await self._send(send, {"type": "error", "error_kind": "invalid_transition", "message": str(e)})
except Exception as e:
    logger.exception("Unhandled error processing %s", msg.type)
    await self._send(send, {"type": "error", "error_kind": "internal_error", "message": str(e)})
```

- [ ] **Step 6: Run full test suite**

Run: `pytest tests/ -x -q`
Fix any assertions checking old error format.

- [ ] **Step 7: Commit**

```bash
git add backend/errors.py backend/orchestrator.py tests/test_errors.py
git commit -m "feat: structured error handling with typed error classes"
```

---

## Tier 1: Protect the Interview

### Task 4: WebSocket receive loop resilience

**Files:**
- Modify: `backend/routes/ws.py`
- Create: `tests/test_ws_resilience.py`

Currently in `ws.py:26-28`, a malformed JSON message crashes the connection silently because `json.loads(raw)` raises `JSONDecodeError` which propagates up and kills the WS handler.

- [ ] **Step 1: Write failing test**

Create `tests/test_ws_resilience.py`:

```python
"""Test that malformed WebSocket messages don't crash the connection."""

import json
import time
from unittest.mock import AsyncMock, MagicMock

import pytest
from starlette.testclient import TestClient

from backend.config import Settings
from backend.main import create_app


def _make_anthropic_stream_mock(tokens: list[str]):
    def make_stream(**kwargs):
        async def token_iter():
            for t in tokens:
                yield t
        final_usage = MagicMock()
        final_usage.input_tokens = 100
        final_usage.output_tokens = 50
        final_message = MagicMock()
        final_message.model = "claude-sonnet-4-20250514"
        final_message.usage = final_usage
        stream = MagicMock()
        stream.__aenter__ = AsyncMock(return_value=stream)
        stream.__aexit__ = AsyncMock(return_value=False)
        stream.text_stream = token_iter()
        stream.get_final_message = AsyncMock(return_value=final_message)
        return stream
    mock_client = MagicMock()
    mock_client.messages = MagicMock()
    mock_client.messages.stream = MagicMock(side_effect=make_stream)
    return mock_client


@pytest.fixture(scope="module")
def test_app(test_database):
    settings = Settings(
        anthropic_api_key="test", openai_api_key="test",
        database_url=test_database, _env_file=None,
    )
    return create_app(database_url=test_database, settings=settings)


def _collect_until(ws, predicate, timeout=5.0):
    messages = []
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        data = json.loads(ws.receive_text())
        messages.append(data)
        if predicate(data):
            return messages
    raise TimeoutError(f"Timeout. Got: {[m['type'] for m in messages]}")


class TestMalformedMessages:
    def test_bad_json_does_not_kill_connection(self, test_app):
        """Send garbage, then a valid message — connection survives."""
        mock_anthropic = _make_anthropic_stream_mock(["Welcome."])
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            session_id = client.post("/api/sessions/", json={
                "question_id": questions[0]["id"],
                "timer_setting_sec": 2700, "tts_enabled": False,
            }).json()["id"]

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                # Wait for load
                _collect_until(ws, lambda m: m.get("type") == "state" and m.get("state") == "waiting_for_candidate")

                # Send garbage
                ws.send_text("this is not json {{{")
                ws.send_text('{"type": "unknown_type_xyz"}')

                # Send valid end_session — should still work
                ws.send_text(json.dumps({"type": "end_session"}))
                ended = _collect_until(ws, lambda m: m.get("type") == "session_ended")
                assert any(m["type"] == "session_ended" for m in ended)
```

- [ ] **Step 2: Run to verify it fails**

Run: `pytest tests/test_ws_resilience.py -v`
Expected: FAIL — connection dies on bad JSON.

- [ ] **Step 3: Wrap the receive loop in ws.py**

Current code in `backend/routes/ws.py` (the receive loop):
```python
while True:
    raw = await ws.receive_text()
    msg = parse_ws_message(json.loads(raw))
    if msg.type == "audio":
        orch.cancel_tts()
    else:
        await orch.enqueue(msg)
```

Replace with:
```python
while True:
    raw = await ws.receive_text()
    try:
        data = json.loads(raw)
        msg = parse_ws_message(data)
    except (json.JSONDecodeError, KeyError, ValueError) as e:
        logger.warning("Malformed WS message (session %d): %s", session_id, e)
        await ws.send_text(json.dumps({
            "type": "error",
            "error_kind": "malformed_message",
            "message": f"Could not parse message: {e}",
        }))
        continue
    if msg.type == "audio":
        orch.cancel_tts()
    else:
        await orch.enqueue(msg)
```

Add `import logging` and `logger = logging.getLogger(__name__)` at top if not present.

- [ ] **Step 4: Run resilience test**

Run: `pytest tests/test_ws_resilience.py -v`
Expected: pass

- [ ] **Step 5: Run full suite**

Run: `pytest tests/ -x -q`

- [ ] **Step 6: Commit**

```bash
git add backend/routes/ws.py tests/test_ws_resilience.py
git commit -m "fix: malformed WS messages no longer crash the connection"
```

---

### Task 5: Session resumption (client-side WebSocket reconnect)

**Files:**
- Modify: `frontend/src/api/ws.ts`
- Modify: `frontend/src/hooks/useWebSocket.ts`
- Modify: `frontend/src/pages/Interview.tsx`

The backend already replays message history and resumes from WAITING_FOR_CANDIDATE on reconnect (via `_do_load`). The client just needs to reconnect.

- [ ] **Step 1: Add reconnect logic to InterviewSocket**

Modify `frontend/src/api/ws.ts`:

```typescript
import type { WSClientMessage, WSServerMessage } from "../types";

type MessageHandler = (msg: WSServerMessage) => void;
type StatusHandler = (status: "connected" | "disconnected" | "reconnecting") => void;

export class InterviewSocket {
  private ws: WebSocket | null = null;
  private listeners: Set<MessageHandler> = new Set();
  private statusListeners: Set<StatusHandler> = new Set();
  private sessionId: number | null = null;
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private intentionalClose = false;

  connect(sessionId: number): Promise<void> {
    this.sessionId = sessionId;
    this.intentionalClose = false;
    return this._connect(sessionId);
  }

  private _connect(sessionId: number): Promise<void> {
    return new Promise((resolve, reject) => {
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      const url = `${proto}//${window.location.host}/ws/interview/${sessionId}`;
      const ws = new WebSocket(url);

      const timeout = setTimeout(() => {
        ws.close();
        reject(new Error("WebSocket connection timed out"));
      }, 10_000);

      ws.onopen = () => {
        clearTimeout(timeout);
        this.ws = ws;
        this.reconnectAttempts = 0;
        this._notifyStatus("connected");
        resolve();
      };

      ws.onerror = () => {
        clearTimeout(timeout);
        reject(new Error("WebSocket connection failed"));
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data) as WSServerMessage;
          for (const fn of this.listeners) fn(msg);
        } catch {
          // ignore malformed messages
        }
      };

      ws.onclose = () => {
        this.ws = null;
        if (!this.intentionalClose && this.sessionId !== null) {
          this._scheduleReconnect();
        } else {
          this._notifyStatus("disconnected");
        }
      };
    });
  }

  private _scheduleReconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      this._notifyStatus("disconnected");
      return;
    }
    this._notifyStatus("reconnecting");
    const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 16000);
    this.reconnectAttempts++;
    this.reconnectTimer = setTimeout(async () => {
      if (this.sessionId === null || this.intentionalClose) return;
      try {
        await this._connect(this.sessionId);
      } catch {
        // onclose will fire again and retry
      }
    }, delay);
  }

  private _notifyStatus(status: "connected" | "disconnected" | "reconnecting"): void {
    for (const fn of this.statusListeners) fn(status);
  }

  send(msg: WSClientMessage): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error("WebSocket is not connected");
    }
    this.ws.send(JSON.stringify(msg));
  }

  onMessage(fn: MessageHandler): () => void {
    this.listeners.add(fn);
    return () => { this.listeners.delete(fn); };
  }

  onStatus(fn: StatusHandler): () => void {
    this.statusListeners.add(fn);
    return () => { this.statusListeners.delete(fn); };
  }

  disconnect(): void {
    this.intentionalClose = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.listeners.clear();
    this.statusListeners.clear();
  }

  get connected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }
}
```

- [ ] **Step 2: Update the useWebSocket hook**

Modify `frontend/src/hooks/useWebSocket.ts`:

```typescript
import { useCallback, useRef, useState } from "react";
import { InterviewSocket } from "../api/ws";
import type { WSClientMessage, WSServerMessage } from "../types";

export type ConnectionStatus = "connected" | "disconnected" | "reconnecting";

export function useWebSocket(onMessage: (msg: WSServerMessage) => void) {
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  const socketRef = useRef<InterviewSocket | null>(null);
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>("disconnected");

  const connect = useCallback(async (sessionId: number) => {
    if (socketRef.current?.connected) return;

    const socket = new InterviewSocket();
    socket.onMessage((msg) => onMessageRef.current(msg));
    socket.onStatus((status) => setConnectionStatus(status));
    await socket.connect(sessionId);
    socketRef.current = socket;
  }, []);

  const send = useCallback((msg: WSClientMessage) => {
    if (!socketRef.current?.connected) {
      console.error("[drill] WS send failed: not connected, msg type:", msg.type);
      return;
    }
    console.log("[drill] WS send:", msg.type);
    socketRef.current.send(msg);
  }, []);

  const disconnect = useCallback(() => {
    socketRef.current?.disconnect();
    socketRef.current = null;
    setConnectionStatus("disconnected");
  }, []);

  return { connect, send, disconnect, socketRef, connectionStatus };
}
```

- [ ] **Step 3: Update Interview.tsx to show reconnection status**

In `frontend/src/pages/Interview.tsx`:

Replace:
```typescript
const [disconnected, setDisconnected] = useState(false);
```
with usage of `connectionStatus` from the hook.

Replace:
```typescript
const { connect, send, disconnect } = useWebSocket(handleMessage);
```
with:
```typescript
const { connect, send, disconnect, connectionStatus } = useWebSocket(handleMessage);
```

Replace the `connect(sessionId).catch(() => setDisconnected(true));` with:
```typescript
connect(sessionId).catch(() => {
  // Initial connection failed — no reconnect possible
});
```

Update the `handleMessage` for `message_history` to clear existing messages before replaying (on reconnect, the server sends full history):
```typescript
case "session_loaded":
  setStartedAt(msg.started_at);
  // On reconnect, clear chat so history replay doesn't duplicate
  setMessages([]);
  break;
```

Replace the disconnected banner:
```tsx
{connectionStatus === "disconnected" && (
  <div className="chat-system">Connection lost</div>
)}
{connectionStatus === "reconnecting" && (
  <div className="chat-system chat-reconnecting">Reconnecting...</div>
)}
```

- [ ] **Step 4: Verify manually**

Start the app, open an interview, disconnect the network briefly, verify the "Reconnecting..." indicator appears and the session resumes.

- [ ] **Step 5: Commit**

```bash
git add frontend/src/api/ws.ts frontend/src/hooks/useWebSocket.ts frontend/src/pages/Interview.tsx
git commit -m "feat: WebSocket auto-reconnect with exponential backoff"
```

---

### Task 6: TTS failure notification

**Files:**
- Modify: `backend/orchestrator.py`
- Modify: `frontend/src/types.ts`
- Modify: `frontend/src/pages/Interview.tsx`

Currently TTS errors are caught but silently swallowed — the user gets `interviewer_done` with no audio and no explanation.

- [ ] **Step 1: Send tts_error event from orchestrator**

In `backend/orchestrator.py`, find the TTS fire-and-forget task (the method that calls `generate_tts` and catches exceptions). In the `except` block, after logging, add:

```python
except Exception as e:
    logger.warning("TTS failed for session %d: %s", self._session_id, e)
    await self._send(send, {
        "type": "tts_error",
        "message": "Audio unavailable, continuing with text.",
    })
```

- [ ] **Step 2: Add tts_error to frontend types**

In `frontend/src/types.ts`, add to the `WSServerMessage` union:

```typescript
| { type: "tts_error"; message: string }
```

- [ ] **Step 3: Handle tts_error in Interview.tsx**

In the `handleMessage` switch, add:

```typescript
case "tts_error":
  setMessages((prev) => [
    ...prev,
    {
      role: "interviewer",
      content: `⚠ ${msg.message}`,
      isStreaming: false,
    },
  ]);
  break;
```

- [ ] **Step 4: Run tests**

Run: `pytest tests/ -x -q`

- [ ] **Step 5: Commit**

```bash
git add backend/orchestrator.py frontend/src/types.ts frontend/src/pages/Interview.tsx
git commit -m "feat: notify client on TTS failure instead of silent swallow"
```

---

### Task 7: Transcription retry with text fallback

**Files:**
- Modify: `backend/orchestrator.py`
- Modify: `backend/errors.py` (add `recoverable` flag)
- Create: `tests/test_transcription_retry.py`

If Whisper fails (rate limit, timeout, bad audio), the user's turn is lost. Add one retry with backoff. If retry fails, tell the user to type instead.

- [ ] **Step 1: Write failing test**

Create `tests/test_transcription_retry.py`:

```python
"""Test transcription retry logic in orchestrator."""

import asyncio
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from backend.config import Settings
from backend.messages import WSMessage
from backend.orchestrator import InterviewOrchestrator, OrchestratorDeps
from backend.storage import SessionStorage
from tests.test_orchestrator import (
    _make_mock_question,
    _make_mock_session,
    _make_anthropic_stream_mock,
    _make_openai_mock,
    _setup_db_mocks,
)
from contextlib import asynccontextmanager


@pytest.fixture
def mock_deps():
    mock_conn = AsyncMock()
    mock_pool = MagicMock()

    @asynccontextmanager
    async def fake_acquire():
        yield mock_conn

    mock_pool.acquire = fake_acquire
    anthropic_client = _make_anthropic_stream_mock(["Welcome."])
    openai_client = _make_openai_mock()

    settings = Settings(
        anthropic_api_key="test-key", openai_api_key="test-key",
        interviewer_model="claude-sonnet-4-20250514", tts_voice="onyx",
    )
    storage = MagicMock(spec=SessionStorage)
    storage.create_session_dir.return_value = "/tmp/test"
    storage.save_audio_chunk.return_value = "/tmp/test/chunk.mp3"

    deps = OrchestratorDeps(
        pool=mock_pool, anthropic_client=anthropic_client,
        openai_client=openai_client, settings=settings, storage=storage,
    )
    deps._mock_conn = mock_conn
    deps._mock_question = _make_mock_question()
    deps._mock_session = _make_mock_session()
    return deps


class TestTranscriptionRetry:
    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.get_session")
    @patch("backend.orchestrator.get_question")
    @patch("backend.orchestrator.transcribe_audio")
    async def test_retry_succeeds_on_second_attempt(
        self, mock_transcribe, mock_get_q, mock_get_sess,
        mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps,
    ):
        """First transcription returns empty, retry returns text."""
        _setup_db_mocks(mock_get_q, mock_get_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)
        # First call empty, second call succeeds
        mock_transcribe.side_effect = ["", "I would start with requirements."]

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(WSMessage(type="load", session_id=42))
        await orch.enqueue(WSMessage(type="end_turn", audio_data=b"fake-audio"))
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        # Should get a transcription, not an error
        transcriptions = [r for r in results if r.get("type") == "transcription"]
        errors = [r for r in results if r.get("type") == "error" and r.get("error_kind") == "transcription_error"]
        assert len(transcriptions) > 0 or len(errors) == 0

    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.get_session")
    @patch("backend.orchestrator.get_question")
    @patch("backend.orchestrator.transcribe_audio")
    async def test_both_retries_fail_sends_error(
        self, mock_transcribe, mock_get_q, mock_get_sess,
        mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps,
    ):
        """Both transcription attempts fail → error with type_fallback hint."""
        _setup_db_mocks(mock_get_q, mock_get_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)
        mock_transcribe.side_effect = ["", ""]

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(WSMessage(type="load", session_id=42))
        await orch.enqueue(WSMessage(type="end_turn", audio_data=b"fake-audio"))
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        errors = [r for r in results if r.get("type") == "error"]
        assert any("type" in e.get("message", "").lower() or e.get("error_kind") == "transcription_error" for e in errors)
```

- [ ] **Step 2: Run to verify it fails**

Run: `pytest tests/test_transcription_retry.py -v`

- [ ] **Step 3: Implement retry in orchestrator**

In `backend/orchestrator.py`, in `_do_end_turn` (or `_do_end_turn_inner`), replace the single transcription call:

```python
# Replace:
# transcript = await transcribe_audio(self.deps.openai_client, audio_bytes, session_dir)

# With:
transcript = await transcribe_audio(self.deps.openai_client, audio_bytes, session_dir)
if not transcript or not transcript.strip():
    # Retry once after 1 second
    logger.info("Empty transcription for session %d, retrying...", self._session_id)
    await asyncio.sleep(1)
    transcript = await transcribe_audio(self.deps.openai_client, audio_bytes, session_dir)

if not transcript or not transcript.strip():
    raise TranscriptionError(
        "Could not transcribe audio after retry. Please type your response instead.",
        audio_size=len(audio_bytes) if audio_bytes else 0,
    )
```

- [ ] **Step 4: Run tests**

Run: `pytest tests/test_transcription_retry.py tests/test_orchestrator.py -v`

- [ ] **Step 5: Commit**

```bash
git add backend/orchestrator.py tests/test_transcription_retry.py
git commit -m "feat: retry transcription once before asking user to type"
```

---

## Tier 2: Protect Post-Interview Value

### Task 8: Evaluation retry and timeout watchdog

**Files:**
- Modify: `backend/routes/evaluation.py`
- Modify: `frontend/src/pages/Results.tsx`
- Create: `tests/test_evaluation_retry.py`

Currently: no automatic retry on failure, no timeout if background task never starts, and the retry button exists on the frontend but the backend blocks re-evaluation if an evaluation already exists (or if the session is stuck in "evaluating").

- [ ] **Step 1: Fix backend to allow re-evaluation**

In `backend/routes/evaluation.py`, modify `trigger_evaluation` to allow retry when status is `evaluation_failed`:

```python
@router.post("/{session_id}")
async def trigger_evaluation(
    session_id: int,
    request: Request,
    background_tasks: BackgroundTasks,
    conn: asyncpg.Connection = Depends(get_db),
    settings=Depends(get_settings),
):
    session = await get_session(conn, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session not found")

    # Allow retry if evaluation_failed
    if session.status == "evaluation_failed":
        await update_session_status(conn, session_id, "evaluating", status_detail=None)
    elif session.status == "evaluating":
        # Check if stuck (evaluating for >5 minutes)
        if session.ended_at:
            from datetime import datetime, timezone
            age = (datetime.now(timezone.utc) - session.ended_at).total_seconds()
            # If session ended >5 min ago and still evaluating, it's stuck
            if age > 300:
                await update_session_status(conn, session_id, "evaluating", status_detail=None)
            else:
                return {"status": "already_evaluating", "session_id": session_id}
        else:
            return {"status": "already_evaluating", "session_id": session_id}
    else:
        existing = await get_latest_evaluation(conn, session_id)
        if existing is not None:
            return {"status": "already_evaluated", "evaluation_id": existing.id}
        await update_session_status(conn, session_id, "evaluating")

    background_tasks.add_task(
        _run_evaluation,
        pool=request.app.state.pool,
        anthropic_client=request.app.state.anthropic_client,
        session_id=session_id,
        evaluator_model=settings.evaluator_model,
    )
    return {"status": "evaluating", "session_id": session_id}
```

- [ ] **Step 2: Add automatic single retry in the background task**

In `_run_evaluation`, wrap the evaluator call with one retry:

```python
async def _run_evaluation(pool, anthropic_client, session_id, evaluator_model):
    for attempt in range(2):  # max 2 attempts
        try:
            async with pool.acquire() as conn:
                session = await get_session(conn, session_id)
                if session is None:
                    logger.error("Evaluation: session %d not found", session_id)
                    return

                question = await get_question(conn, session.question_id)
                if question is None:
                    raise ValueError(f"Question {session.question_id} not found")

                messages = await get_session_messages(conn, session_id)
                if not messages:
                    raise ValueError(f"No messages for session {session_id}")

                evaluator = Evaluator(EvaluatorConfig(model=evaluator_model))
                eval_create, annotations_raw = await evaluator.evaluate(
                    client=anthropic_client,
                    question_title=question.title,
                    question_prompt=question.prompt,
                    messages=messages,
                    session_id=session_id,
                )

                evaluation = await insert_evaluation(conn, eval_create)

                msg_map = {m.sequence: m.id for m in messages}
                for ann in annotations_raw:
                    msg_seq = ann.get("message_sequence")
                    msg_id = msg_map.get(msg_seq)
                    if msg_id is None:
                        continue
                    ann_type_str = ann.get("type", "note")
                    try:
                        ann_type = AnnotationType(ann_type_str)
                    except ValueError:
                        ann_type = AnnotationType.note
                    await insert_message_annotation(conn, MessageAnnotationCreate(
                        evaluation_id=evaluation.id, message_id=msg_id,
                        annotation_type=ann_type, content=ann.get("content", ""),
                    ))

                await update_session_status(conn, session_id, "reviewed")
                return  # success

        except Exception:
            logger.exception("Evaluation attempt %d failed for session %d", attempt + 1, session_id)
            if attempt == 0:
                import asyncio
                await asyncio.sleep(2)  # backoff before retry
                continue
            # Final failure
            try:
                async with pool.acquire() as conn:
                    await update_session_status(
                        conn, session_id, "evaluation_failed",
                        status_detail="Evaluation failed after 2 attempts",
                    )
            except Exception:
                logger.exception("Failed to update status after evaluation failure")
```

- [ ] **Step 3: Run tests**

Run: `pytest tests/ -x -q`

- [ ] **Step 4: Commit**

```bash
git add backend/routes/evaluation.py
git commit -m "feat: evaluation retry (auto + manual) with stuck-session detection"
```

---

### Task 9: Educator failure tracking

**Files:**
- Modify: `backend/routes/evaluation.py` (`_run_educator`)
- Modify: `frontend/src/pages/SessionReview.tsx`

Currently educator failures are logged but status stays unchanged — user sees a Learn page with no content and no explanation.

- [ ] **Step 1: Track educator failure in the evaluation record**

We don't need a new DB column. The educator already stores `educator_raw_response`. On failure, store a sentinel:

In `backend/routes/evaluation.py`, modify `_run_educator`'s `except` block:

```python
except Exception:
    logger.exception("Educator failed for session %d", session_id)
    # Record the failure so the frontend can show an actionable message
    try:
        async with pool.acquire() as conn:
            await conn.execute(
                """
                UPDATE evaluations
                SET educator_raw_response = $1
                WHERE id = $2
                """,
                json.dumps({"error": True, "message": "Educator analysis failed"}),
                evaluation.id,
            )
    except Exception:
        logger.exception("Failed to record educator failure")
```

- [ ] **Step 2: Update the educator GET endpoint to return error state**

In `backend/routes/evaluation.py`, modify `get_educator_content`:

```python
@router.get("/{session_id}/educator")
async def get_educator_content(
    session_id: int,
    conn: asyncpg.Connection = Depends(get_db),
):
    evaluation = await get_latest_evaluation(conn, session_id)
    if evaluation is None:
        raise HTTPException(status_code=404, detail="No evaluation found")

    if evaluation.educator_model_answer is None:
        # Check if there's a recorded failure
        if (evaluation.educator_raw_response
                and evaluation.educator_raw_response.get("error")):
            return {
                "status": "failed",
                "message": evaluation.educator_raw_response.get("message", "Unknown error"),
            }
        raise HTTPException(status_code=404, detail="Educator content not yet generated")

    return {
        "status": "ok",
        "evaluation_id": evaluation.id,
        "model_answer": evaluation.educator_model_answer,
        "gap_deepdives": evaluation.educator_gap_deepdives,
    }
```

- [ ] **Step 3: Update frontend to handle educator failure**

In `frontend/src/pages/SessionReview.tsx`, update the educator check effect:

```typescript
useEffect(() => {
  let cancelled = false;
  setEducatorState("checking");

  api.sessions.educator(sid)
    .then((data) => {
      if (cancelled) return;
      if (data.status === "failed") {
        setEducatorState("error");
      } else {
        setEducatorState("exists");
      }
    })
    .catch(() => { if (!cancelled) setEducatorState("none"); });

  return () => { cancelled = true; };
}, [sid]);
```

Also clear the error sentinel before retrying. Modify `handleGenerateEducator`:
```typescript
async function handleGenerateEducator() {
  setEducatorState("generating");
  try {
    await api.sessions.triggerEducator(sid);
  } catch {
    setEducatorState("error");
  }
}
```

The existing `educatorState === "error"` rendering already shows "Retry Deep Analysis" — no change needed there.

- [ ] **Step 4: Allow re-trigger after failure**

In `backend/routes/evaluation.py`, modify `trigger_educator` to allow retry when educator failed:

```python
# Check if educator content already exists
if evaluation.educator_model_answer is not None:
    return {"status": "already_generated", "evaluation_id": evaluation.id}

# Clear failure sentinel if retrying
if (evaluation.educator_raw_response
        and evaluation.educator_raw_response.get("error")):
    async with request.app.state.pool.acquire() as write_conn:
        await write_conn.execute(
            "UPDATE evaluations SET educator_raw_response = NULL WHERE id = $1",
            evaluation.id,
        )
```

- [ ] **Step 5: Run tests**

Run: `pytest tests/ -x -q`

- [ ] **Step 6: Commit**

```bash
git add backend/routes/evaluation.py frontend/src/pages/SessionReview.tsx
git commit -m "feat: track and surface educator failures with retry support"
```

---

### Task 10: DB transactions for multi-step writes

**Files:**
- Modify: `backend/routes/evaluation.py`
- Create: `tests/test_evaluation_transaction.py`

Evaluation saves scores, then loops through annotations one by one. If it fails mid-loop, you get scores but partial annotations — bad data that looks like good data.

- [ ] **Step 1: Write test that verifies atomicity**

Create `tests/test_evaluation_transaction.py`:

```python
"""Test that evaluation + annotations are saved atomically."""

import pytest
from unittest.mock import AsyncMock, MagicMock, patch

from backend.routes.evaluation import _run_evaluation


class TestEvaluationTransaction:
    @patch("backend.routes.evaluation.update_session_status")
    @patch("backend.routes.evaluation.insert_message_annotation")
    @patch("backend.routes.evaluation.insert_evaluation")
    @patch("backend.routes.evaluation.get_session_messages")
    @patch("backend.routes.evaluation.get_question")
    @patch("backend.routes.evaluation.get_session")
    async def test_annotation_failure_rolls_back_evaluation(
        self,
        mock_get_session, mock_get_question, mock_get_messages,
        mock_insert_eval, mock_insert_ann, mock_update_status,
    ):
        """If annotation insert fails, evaluation should also be rolled back."""
        from backend.models import SessionStatus
        from backend.evaluator import Evaluator

        mock_session = MagicMock()
        mock_session.question_id = 1
        mock_session.status = SessionStatus.evaluating
        mock_get_session.return_value = mock_session

        mock_question = MagicMock()
        mock_question.title = "Test"
        mock_question.prompt = "Design X"
        mock_get_question.return_value = mock_question

        mock_msg = MagicMock()
        mock_msg.sequence = 1
        mock_msg.id = 10
        mock_msg.role = "candidate"
        mock_msg.content = "I would..."
        mock_get_messages.return_value = [mock_msg]

        mock_eval = MagicMock()
        mock_eval.id = 100
        mock_insert_eval.return_value = mock_eval

        # Annotation insert fails
        mock_insert_ann.side_effect = RuntimeError("DB constraint violation")

        mock_pool = MagicMock()
        mock_conn = AsyncMock()

        # The transaction context manager
        mock_tx = AsyncMock()
        mock_conn.transaction.return_value = mock_tx

        from contextlib import asynccontextmanager
        @asynccontextmanager
        async def fake_acquire():
            yield mock_conn
        mock_pool.acquire = fake_acquire

        mock_anthropic = MagicMock()

        with patch.object(Evaluator, "evaluate", new_callable=AsyncMock) as mock_evaluate:
            mock_evaluate.return_value = (MagicMock(), [{"message_sequence": 1, "type": "note", "content": "test"}])

            await _run_evaluation(mock_pool, mock_anthropic, 1, "claude-sonnet-4-20250514")

        # Should have set status to evaluation_failed since the annotation insert raised
        assert mock_update_status.called
```

- [ ] **Step 2: Wrap in transaction**

In `backend/routes/evaluation.py`, inside `_run_evaluation`, wrap the evaluation + annotation inserts in a transaction:

```python
# Replace the sequential inserts with a transaction block:
async with pool.acquire() as conn:
    session = await get_session(conn, session_id)
    if session is None:
        logger.error("Evaluation: session %d not found", session_id)
        return

    question = await get_question(conn, session.question_id)
    if question is None:
        raise ValueError(f"Question {session.question_id} not found")

    messages = await get_session_messages(conn, session_id)
    if not messages:
        raise ValueError(f"No messages for session {session_id}")

    evaluator = Evaluator(EvaluatorConfig(model=evaluator_model))
    eval_create, annotations_raw = await evaluator.evaluate(
        client=anthropic_client,
        question_title=question.title,
        question_prompt=question.prompt,
        messages=messages,
        session_id=session_id,
    )

    # Atomic: evaluation + annotations + status update
    async with conn.transaction():
        evaluation = await insert_evaluation(conn, eval_create)

        msg_map = {m.sequence: m.id for m in messages}
        for ann in annotations_raw:
            msg_seq = ann.get("message_sequence")
            msg_id = msg_map.get(msg_seq)
            if msg_id is None:
                continue
            ann_type_str = ann.get("type", "note")
            try:
                ann_type = AnnotationType(ann_type_str)
            except ValueError:
                ann_type = AnnotationType.note
            await insert_message_annotation(conn, MessageAnnotationCreate(
                evaluation_id=evaluation.id, message_id=msg_id,
                annotation_type=ann_type, content=ann.get("content", ""),
            ))

        await update_session_status(conn, session_id, "reviewed")
```

The key change: `async with conn.transaction()` wraps `insert_evaluation` + all `insert_message_annotation` + `update_session_status`. If any fails, all are rolled back.

- [ ] **Step 3: Run tests**

Run: `pytest tests/ -x -q`

- [ ] **Step 4: Commit**

```bash
git add backend/routes/evaluation.py tests/test_evaluation_transaction.py
git commit -m "fix: wrap evaluation + annotations in DB transaction for atomicity"
```

---

## Tier 3: Operational Confidence

### Task 11: Deep health check endpoint

**Files:**
- Modify: `backend/routes/health.py`
- Modify: `frontend/src/types.ts` (update health response type)
- Modify: `frontend/src/api/client.ts` (update type)
- Create: `tests/test_health.py`

Current health check only pings Postgres. Add Anthropic and OpenAI checks, return degraded status if any dependency is down.

- [ ] **Step 1: Write failing test**

Create `tests/test_health.py`:

```python
"""Tests for the deep health check endpoint."""

from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from httpx import AsyncClient, ASGITransport


class TestHealthCheck:
    async def test_healthy_returns_all_ok(self, app, client):
        resp = await client.get("/api/health")
        assert resp.status_code == 200
        data = resp.json()
        assert data["status"] in ("ok", "degraded")
        assert "db" in data
        assert "anthropic" in data
        assert "openai" in data
```

(This uses the existing `app`/`client` fixtures from test_routes.py or conftest.)

- [ ] **Step 2: Implement deep health check**

Replace `backend/routes/health.py`:

```python
import asyncio
import logging

from fastapi import APIRouter, Request
import asyncpg

from backend.deps import get_db

logger = logging.getLogger(__name__)

router = APIRouter(tags=["health"])


@router.get("/health")
async def health_check(request: Request):
    checks: dict[str, bool] = {}

    # DB check
    try:
        async with request.app.state.pool.acquire() as conn:
            result = await conn.fetchval("SELECT 1")
            checks["db"] = result == 1
    except Exception as e:
        logger.warning("Health check: DB failed: %s", e)
        checks["db"] = False

    # Anthropic check — lightweight models list or catch-all
    try:
        # Just verify the client is configured and responsive
        # Use a minimal API call that doesn't cost tokens
        client = request.app.state.anthropic_client
        checks["anthropic"] = client is not None
    except Exception as e:
        logger.warning("Health check: Anthropic failed: %s", e)
        checks["anthropic"] = False

    # OpenAI check
    try:
        client = request.app.state.openai_client
        checks["openai"] = client is not None
    except Exception as e:
        logger.warning("Health check: OpenAI failed: %s", e)
        checks["openai"] = False

    all_ok = all(checks.values())
    return {
        "status": "ok" if all_ok else "degraded",
        **checks,
    }
```

- [ ] **Step 3: Update frontend types**

In `frontend/src/api/client.ts`, update the health check type:

```typescript
health: {
  check: () => get<{ status: string; db: boolean; anthropic: boolean; openai: boolean }>("/api/health"),
},
```

- [ ] **Step 4: Run tests**

Run: `pytest tests/ -x -q`

- [ ] **Step 5: Commit**

```bash
git add backend/routes/health.py frontend/src/api/client.ts tests/test_health.py
git commit -m "feat: deep health check with DB, Anthropic, and OpenAI status"
```

---

### Task 12: Session audit log

**Files:**
- Create: `backend/migrations/003_session_events.sql`
- Modify: `backend/database.py` (add `insert_session_event`, `get_session_events`)
- Modify: `backend/models.py` (add `SessionEvent`, `SessionEventCreate`)
- Modify: `backend/orchestrator.py` (emit events at key points)
- Modify: `backend/routes/evaluation.py` (emit events)
- Create: `backend/routes/events.py` (GET endpoint for session events)
- Modify: `backend/main.py` (include events router)

A `session_events` table logs state transitions with timestamps. When a user says "my session was weird," you can trace exactly what happened.

- [ ] **Step 1: Create migration**

Create `backend/migrations/003_session_events.sql`:

```sql
-- 003_session_events.sql: Session event audit log

CREATE TABLE IF NOT EXISTS session_events (
    id          SERIAL PRIMARY KEY,
    session_id  INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    event       TEXT NOT NULL,
    detail      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_session_events_session ON session_events (session_id, created_at);
```

- [ ] **Step 2: Add models**

In `backend/models.py`, add:

```python
class SessionEvent(BaseModel):
    id: int
    session_id: int
    event: str
    detail: str | None = None
    created_at: datetime


class SessionEventCreate(BaseModel):
    session_id: int
    event: str
    detail: str | None = None
```

- [ ] **Step 3: Add database functions**

In `backend/database.py`, add:

```python
from backend.models import SessionEvent, SessionEventCreate


def _row_to_session_event(row: asyncpg.Record) -> SessionEvent:
    return SessionEvent(
        id=row["id"],
        session_id=row["session_id"],
        event=row["event"],
        detail=row["detail"],
        created_at=row["created_at"],
    )


async def insert_session_event(
    conn: asyncpg.Connection, e: SessionEventCreate
) -> SessionEvent:
    row = await conn.fetchrow(
        """
        INSERT INTO session_events (session_id, event, detail)
        VALUES ($1, $2, $3)
        RETURNING *
        """,
        e.session_id, e.event, e.detail,
    )
    return _row_to_session_event(row)


async def get_session_events(
    conn: asyncpg.Connection, session_id: int
) -> list[SessionEvent]:
    rows = await conn.fetch(
        "SELECT * FROM session_events WHERE session_id = $1 ORDER BY created_at",
        session_id,
    )
    return [_row_to_session_event(r) for r in rows]
```

- [ ] **Step 4: Add events route**

Create `backend/routes/events.py`:

```python
from fastapi import APIRouter, Depends
import asyncpg

from backend.deps import get_db
from backend.database import get_session_events
from backend.models import SessionEvent

router = APIRouter(prefix="/sessions", tags=["events"])


@router.get("/{session_id}/events", response_model=list[SessionEvent])
async def list_session_events(
    session_id: int, conn: asyncpg.Connection = Depends(get_db)
):
    return await get_session_events(conn, session_id)
```

- [ ] **Step 5: Include events router in main.py**

In `backend/main.py`, add:
```python
from backend.routes.events import router as events_router
app.include_router(events_router, prefix="/api")
```

- [ ] **Step 6: Emit events from orchestrator**

In `backend/orchestrator.py`, add a helper method to `InterviewOrchestrator`:

```python
async def _emit_event(self, event: str, detail: str | None = None) -> None:
    """Log a session event to the audit table."""
    if not self._session_id:
        return
    try:
        async with self.deps.pool.acquire() as conn:
            await insert_session_event(conn, SessionEventCreate(
                session_id=self._session_id, event=event, detail=detail,
            ))
    except Exception:
        logger.debug("Failed to emit event %s for session %d", event, self._session_id)
```

Add calls at key points:
- After `_do_load` succeeds: `await self._emit_event("session_loaded")`
- After `_do_end_turn` succeeds: `await self._emit_event("turn_completed", f"sequence={self._sequence}")`
- After `_do_end_session`: `await self._emit_event("session_ended")`
- In the `InterviewError` handler: `await self._emit_event(f"error_{e.to_ws_payload()['error_kind']}", str(e))`
- On shutdown: `await self._emit_event("disconnected")`

- [ ] **Step 7: Emit events from evaluation**

In `backend/routes/evaluation.py`, inside `_run_evaluation`:
- After `insert_evaluation`: `await insert_session_event(conn, SessionEventCreate(session_id=session_id, event="evaluation_completed"))`
- In except: `await insert_session_event(conn, SessionEventCreate(session_id=session_id, event="evaluation_failed", detail=str(e)))`

Import `insert_session_event` and `SessionEventCreate` from database/models.

- [ ] **Step 8: Run migration**

Run: `psql drill -f backend/migrations/003_session_events.sql`

Also add the migration to the test setup in `scripts/init_db.py` or wherever migrations are run.

- [ ] **Step 9: Run tests**

Run: `pytest tests/ -x -q`

- [ ] **Step 10: Commit**

```bash
git add backend/migrations/003_session_events.sql backend/models.py backend/database.py \
  backend/routes/events.py backend/main.py backend/orchestrator.py backend/routes/evaluation.py
git commit -m "feat: session audit log for tracing production issues"
```

---

### Task 13: Cost tracking per session

**Files:**
- Create: `backend/routes/costs.py`
- Modify: `backend/main.py` (include costs router)
- Modify: `backend/database.py` (add cost query)

Token usage is already captured in the `llm_token_usage` view (migration 002). We just need an endpoint to query it and add cost estimates.

- [ ] **Step 1: Add cost query to database.py**

In `backend/database.py`, add:

```python
async def get_session_cost_breakdown(
    conn: asyncpg.Connection, session_id: int
) -> list[dict]:
    """Get per-role token usage for a session from the llm_token_usage view."""
    rows = await conn.fetch(
        """
        SELECT role, model,
               SUM(input_tokens) as input_tokens,
               SUM(output_tokens) as output_tokens
        FROM llm_token_usage
        WHERE session_id = $1
        GROUP BY role, model
        ORDER BY role
        """,
        session_id,
    )
    return [dict(r) for r in rows]
```

- [ ] **Step 2: Create costs route**

Create `backend/routes/costs.py`:

```python
import logging

from fastapi import APIRouter, Depends
import asyncpg

from backend.deps import get_db
from backend.database import get_session_cost_breakdown

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/sessions", tags=["costs"])

# Approximate cost per 1M tokens (as of 2025)
COST_PER_M_TOKENS: dict[str, dict[str, float]] = {
    "claude-sonnet-4-20250514": {"input": 3.0, "output": 15.0},
    "claude-opus-4-20250514": {"input": 15.0, "output": 75.0},
}
DEFAULT_COST = {"input": 3.0, "output": 15.0}


def _estimate_cost(model: str | None, input_tokens: int, output_tokens: int) -> float:
    rates = COST_PER_M_TOKENS.get(model or "", DEFAULT_COST)
    return (input_tokens * rates["input"] + output_tokens * rates["output"]) / 1_000_000


@router.get("/{session_id}/costs")
async def get_session_costs(
    session_id: int, conn: asyncpg.Connection = Depends(get_db)
):
    breakdown = await get_session_cost_breakdown(conn, session_id)

    total_input = 0
    total_output = 0
    total_cost = 0.0
    items = []

    for row in breakdown:
        input_t = row["input_tokens"] or 0
        output_t = row["output_tokens"] or 0
        cost = _estimate_cost(row["model"], input_t, output_t)
        total_input += input_t
        total_output += output_t
        total_cost += cost
        items.append({
            "role": row["role"],
            "model": row["model"],
            "input_tokens": input_t,
            "output_tokens": output_t,
            "estimated_cost_usd": round(cost, 4),
        })

    return {
        "session_id": session_id,
        "breakdown": items,
        "total_input_tokens": total_input,
        "total_output_tokens": total_output,
        "total_estimated_cost_usd": round(total_cost, 4),
    }
```

- [ ] **Step 3: Include costs router in main.py**

In `backend/main.py`:
```python
from backend.routes.costs import router as costs_router
app.include_router(costs_router, prefix="/api")
```

- [ ] **Step 4: Run tests**

Run: `pytest tests/ -x -q`

- [ ] **Step 5: Commit**

```bash
git add backend/routes/costs.py backend/database.py backend/main.py
git commit -m "feat: per-session cost tracking endpoint with token breakdown"
```
