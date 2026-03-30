"""Interview orchestrator — sequential message queue processor.

Architecture: one asyncio.Queue, one consumer coroutine. Messages are
processed strictly one at a time, eliminating race conditions by
construction. The only concurrent operation is TTS playback, which runs
as a fire-and-forget asyncio.Task with a cancellation handle.

Usage::

    orch = InterviewOrchestrator(deps)
    await orch.enqueue(WSMessage(type="load", session_id=42))
    await orch.run(send=websocket.send_json)
"""

from __future__ import annotations

import asyncio
import base64
import logging
from dataclasses import dataclass
from datetime import datetime, timezone
from typing import Any, Callable

from backend.config import Settings
from backend.errors import InterviewError, LLMError, SessionLoadError, TranscriptionError
from backend.database import (
    get_latest_coach_review,
    get_question,
    get_session,
    get_session_messages,
    insert_message,
    update_session_status,
)
from backend.interviewer import Interviewer, InterviewerConfig
from backend.messages import WSMessage
from backend.models import (
    MessageCreate,
    MessageRole,
    Question,
    SessionStatus,
)
from backend.session_manager import InvalidTransition, SessionState, SessionStateMachine
from backend.speech import generate_tts, transcribe_audio
from backend.storage import SessionStorage
from backend.tracing import get_tracer

logger = logging.getLogger(__name__)
tracer = get_tracer("drill.orchestrator")


@dataclass
class OrchestratorDeps:
    """All external dependencies, injectable for testing."""

    pool: Any  # asyncpg.Pool
    anthropic_client: Any  # anthropic.AsyncAnthropic
    openai_client: Any  # openai.AsyncOpenAI
    settings: Settings
    storage: SessionStorage


# Type alias for the send callback (sync or async).
SendFn = Callable[[dict], Any]


class InterviewOrchestrator:
    """Sequential message processor for one interview session.

    One asyncio.Queue, one consumer. Messages processed one at a time.
    TTS is the only concurrent operation (fire-and-forget with cancel).
    """

    def __init__(self, deps: OrchestratorDeps) -> None:
        self.deps = deps
        self._queue: asyncio.Queue[WSMessage] = asyncio.Queue()
        self._state = SessionStateMachine()
        self._interviewer = Interviewer(
            InterviewerConfig(model=deps.settings.interviewer_model)
        )
        self._session_id: int | None = None
        self._session_dir: str | None = None
        self._question: Question | None = None
        self._system_prompt: str = ""
        self._sequence: int = 0
        self._last_candidate_sequence: int = 0
        self._tts_enabled: bool = True
        self._tts_task: asyncio.Task | None = None
        self._timer_sec: int = 2700
        self._briefing: str | None = None
        self._started_at: datetime | None = None

    async def enqueue(self, msg: WSMessage) -> None:
        """Put a message on the processing queue."""
        await self._queue.put(msg)

    def cancel_tts(self) -> None:
        """Cancel any in-flight TTS task. Safe to call from outside the queue."""
        if self._tts_task and not self._tts_task.done():
            self._tts_task.cancel()

    async def _send(self, send: SendFn, payload: dict) -> None:
        """Call the send function, handling both sync and async callbacks."""
        result = send(payload)
        if asyncio.iscoroutine(result):
            await result

    async def run(self, send: SendFn) -> None:
        """Main loop. Processes one message at a time until shutdown.

        Args:
            send: Callback to emit messages to the client. May be sync or async.
        """
        while True:
            msg = await self._queue.get()
            if msg.type == "shutdown":
                self.cancel_tts()
                await self._finalize_if_active(send)
                break
            try:
                await self._handle(msg, send)
            except InvalidTransition as e:
                await self._send(send, {
                    "type": "error",
                    "error_kind": "invalid_transition",
                    "message": str(e),
                })
            except InterviewError as e:
                logger.warning("Interview error processing %s: %s", msg.type, e)
                await self._send(send, e.to_ws_payload())
                # Recover state if stuck mid-turn
                if self._session_id and self._state.state in (
                    SessionState.PROCESSING,
                    SessionState.CANDIDATE_SPEAKING,
                    SessionState.INTERVIEWER_SPEAKING,
                ):
                    try:
                        self._state.state = SessionState.WAITING_FOR_CANDIDATE
                        await self._send(send, {
                            "type": "state",
                            "state": SessionState.WAITING_FOR_CANDIDATE.value,
                        })
                    except Exception:
                        pass
                # Save partial LLM response if available
                if isinstance(e, LLMError) and e.partial_response:
                    self._sequence += 1
                    async with self.deps.pool.acquire() as conn:
                        await insert_message(
                            conn,
                            MessageCreate(
                                session_id=self._session_id,
                                sequence=self._sequence,
                                role=MessageRole.interviewer,
                                content=e.partial_response + " (error - incomplete)",
                            ),
                        )
            except Exception as e:
                logger.exception("Unhandled error processing message %s", msg.type)
                await self._send(send, {
                    "type": "error",
                    "error_kind": "internal_error",
                    "message": str(e),
                })
                # Recover state: if we're stuck mid-processing, force back to
                # WAITING_FOR_CANDIDATE so the user can continue.
                if self._session_id and self._state.state in (
                    SessionState.PROCESSING,
                    SessionState.CANDIDATE_SPEAKING,
                    SessionState.INTERVIEWER_SPEAKING,
                ):
                    try:
                        self._state.state = SessionState.WAITING_FOR_CANDIDATE
                        await self._send(send, {
                            "type": "state",
                            "state": SessionState.WAITING_FOR_CANDIDATE.value,
                        })
                    except Exception:
                        pass

    async def _handle(self, msg: WSMessage, send: SendFn) -> None:
        """Dispatch a single message to the appropriate handler."""
        match msg.type:
            case "load":
                await self._do_load(msg, send)
            case "end_turn":
                await self._do_end_turn(msg, send)
            case "text_input":
                await self._do_text_input(msg, send)
            case "edit_transcript":
                await self._do_edit_transcript(msg, send)
            case "end_session":
                await self._do_end_session(msg, send)
            case _:
                await self._send(send, {
                    "type": "error",
                    "message": f"Unknown message type: {msg.type}",
                })

    # ------------------------------------------------------------------
    # Handlers
    # ------------------------------------------------------------------

    async def _do_load(self, msg: WSMessage, send: SendFn) -> None:
        """Load session from DB. If no messages, generate opening; otherwise replay history."""
        with tracer.start_as_current_span("interview.load", attributes={
            "session_id": msg.session_id or 0,
        }):
            await self._do_load_inner(msg, send)

    async def _do_load_inner(self, msg: WSMessage, send: SendFn) -> None:
        # Load session and related data from DB
        async with self.deps.pool.acquire() as conn:
            session = await get_session(conn, msg.session_id)
            if not session:
                raise SessionLoadError("Session not found", session_id=msg.session_id)

            self._question = await get_question(conn, session.question_id)
            if not self._question:
                raise SessionLoadError("Question not found", session_id=msg.session_id)

            messages = await get_session_messages(conn, session.id)

            # Get briefing if the session was created with interviewer_briefed
            self._briefing = None
            if session.interviewer_briefed:
                review = await get_latest_coach_review(conn)
                if review:
                    self._briefing = review.recommendation

        # Set all instance state from the loaded session
        self._session_id = session.id
        self._timer_sec = session.timer_setting_sec
        self._tts_enabled = session.tts_enabled
        self._session_dir = self.deps.storage.create_session_dir(session.id)
        self._started_at = session.started_at

        # Restore sequence counters from existing messages
        if messages:
            self._sequence = max(m.sequence for m in messages)
            candidate_msgs = [m for m in messages if m.role == MessageRole.candidate]
            if candidate_msgs:
                self._last_candidate_sequence = max(m.sequence for m in candidate_msgs)

        # Compute elapsed time from DB started_at
        now = datetime.now(timezone.utc)
        started = self._started_at
        if started.tzinfo is None:
            started = started.replace(tzinfo=timezone.utc)
        elapsed = int((now - started).total_seconds())

        # Build system prompt
        self._system_prompt = self._interviewer.build_system_prompt(
            question_title=self._question.title,
            question_prompt=self._question.prompt,
            timer_sec=self._timer_sec,
            elapsed_sec=elapsed,
            briefing=self._briefing,
        )

        # Send session_loaded metadata to client
        await self._send(send, {
            "type": "session_loaded",
            "session_id": session.id,
            "started_at": session.started_at.isoformat(),
            "timer_sec": self._timer_sec,
            "tts_enabled": self._tts_enabled,
        })

        if not messages:
            # New session — generate opening question
            self._state.transition(SessionState.STARTING)
            self._state.transition(SessionState.INTERVIEWER_SPEAKING)

            # Stream opening message
            full_response, opening_usage = await self._stream_interviewer_opening(send)

            # Save interviewer message to DB
            self._sequence += 1
            async with self.deps.pool.acquire() as conn:
                await insert_message(
                    conn,
                    MessageCreate(
                        session_id=self._session_id,
                        sequence=self._sequence,
                        role=MessageRole.interviewer,
                        content=full_response,
                        raw_response=opening_usage,
                    ),
                )

            # TTS (fire-and-forget)
            if self._tts_enabled:
                self._tts_task = asyncio.create_task(
                    self._stream_tts(full_response, send)
                )
            else:
                await self._send(send, {"type": "interviewer_done"})

            self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
            await self._send(send, {
                "type": "state",
                "state": self._state.state.value,
            })
        else:
            # Existing session — send message history and resume
            self._state.transition(SessionState.STARTING)
            self._state.transition(SessionState.INTERVIEWER_SPEAKING)

            for m in messages:
                await self._send(send, {
                    "type": "message_history",
                    "sequence": m.sequence,
                    "role": m.role.value if hasattr(m.role, "value") else m.role,
                    "content": m.content,
                    "timestamp": m.timestamp.isoformat(),
                })

            self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
            await self._send(send, {
                "type": "state",
                "state": self._state.state.value,
            })

    async def _do_end_turn(self, msg: WSMessage, send: SendFn) -> None:
        """Transcribe audio, then get interviewer response. Sequential."""
        with tracer.start_as_current_span("interview.turn", attributes={
            "session_id": self._session_id or 0,
            "turn": self._state.turn_count + 1,
            "input_type": "voice",
        }):
            await self._do_end_turn_inner(msg, send)

    async def _do_end_turn_inner(self, msg: WSMessage, send: SendFn) -> None:
        self.cancel_tts()
        if self._tts_task:
            try:
                await asyncio.wait_for(self._tts_task, timeout=0.1)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                pass
            self._tts_task = None
        self._state.transition(SessionState.CANDIDATE_SPEAKING)
        self._state.transition(SessionState.PROCESSING)
        await self._send(send, {"type": "state", "state": "processing"})

        # Save incoming audio to disk
        if msg.audio_data and self._session_dir:
            self.deps.storage.save_audio_chunk(
                self._session_dir, "audio_in", self._sequence + 1, 0, msg.audio_data, "webm"
            )

        # 1. Transcribe (with one retry on empty result)
        with tracer.start_as_current_span("speech.transcribe", attributes={
            "audio_size_bytes": len(msg.audio_data) if msg.audio_data else 0,
        }):
            transcript = await transcribe_audio(
                self.deps.openai_client, msg.audio_data
            )
            if not transcript or not transcript.strip():
                # Retry once after 1 second
                logger.info("Empty transcription for session %d, retrying...", self._session_id)
                await asyncio.sleep(1)
                transcript = await transcribe_audio(
                    self.deps.openai_client, msg.audio_data
                )
        if not transcript or not transcript.strip():
            raise TranscriptionError(
                "Could not transcribe audio after retry. Please type your response instead.",
                audio_size=len(msg.audio_data) if msg.audio_data else 0,
            )

        await self._send(send, {"type": "transcription", "text": transcript})

        # 2. Save candidate message
        self._sequence += 1
        self._last_candidate_sequence = self._sequence
        async with self.deps.pool.acquire() as conn:
            await insert_message(
                conn,
                MessageCreate(
                    session_id=self._session_id,
                    sequence=self._sequence,
                    role=MessageRole.candidate,
                    content=transcript,
                    raw_content=transcript,
                ),
            )

        # 3. Get interviewer response
        await self._respond_as_interviewer(send)

    async def _do_text_input(self, msg: WSMessage, send: SendFn) -> None:
        """Same as end_turn but skip Whisper — text goes straight to Claude."""
        with tracer.start_as_current_span("interview.turn", attributes={
            "session_id": self._session_id or 0,
            "turn": self._state.turn_count + 1,
            "input_type": "text",
        }):
            await self._do_text_input_inner(msg, send)

    async def _do_text_input_inner(self, msg: WSMessage, send: SendFn) -> None:
        self.cancel_tts()
        if self._tts_task:
            try:
                await asyncio.wait_for(self._tts_task, timeout=0.1)
            except (asyncio.TimeoutError, asyncio.CancelledError):
                pass
            self._tts_task = None
        self._state.transition(SessionState.CANDIDATE_SPEAKING)
        self._state.transition(SessionState.PROCESSING)
        await self._send(send, {"type": "state", "state": "processing"})

        # Save candidate message (no raw_content since it was typed)
        self._sequence += 1
        self._last_candidate_sequence = self._sequence
        async with self.deps.pool.acquire() as conn:
            await insert_message(
                conn,
                MessageCreate(
                    session_id=self._session_id,
                    sequence=self._sequence,
                    role=MessageRole.candidate,
                    content=msg.text,
                ),
            )

        await self._respond_as_interviewer(send)

    async def _respond_as_interviewer(self, send: SendFn) -> None:
        """Get interviewer response, stream it, save it, optionally TTS."""
        self._state.transition(SessionState.INTERVIEWER_SPEAKING)
        await self._send(send, {
            "type": "state",
            "state": self._state.state.value,
        })

        # Rebuild system prompt with current elapsed time from DB started_at
        elapsed = 0
        if self._started_at is not None:
            now = datetime.now(timezone.utc)
            started = self._started_at
            if started.tzinfo is None:
                started = started.replace(tzinfo=timezone.utc)
            elapsed = int((now - started).total_seconds())
        self._system_prompt = self._interviewer.build_system_prompt(
            question_title=self._question.title,
            question_prompt=self._question.prompt,
            timer_sec=self._timer_sec,
            elapsed_sec=elapsed,
            briefing=self._briefing,
        )

        # Get conversation history for context
        async with self.deps.pool.acquire() as conn:
            history = await get_session_messages(conn, self._session_id)

        # Build messages in Anthropic format
        api_messages = self._interviewer.build_messages(history)

        # Stream response
        full_response = ""
        usage_data: list = []
        llm_span = tracer.start_span("llm.interviewer", attributes={
            "session_id": self._session_id or 0,
            "history_length": len(api_messages),
        })
        try:
            async for token in self._interviewer.get_response_stream(
                self.deps.anthropic_client,
                self._system_prompt,
                api_messages,
                usage_out=usage_data,
            ):
                full_response += token
                await self._send(send, {
                    "type": "interviewer_text",
                    "content": token,
                    "done": False,
                })
        except Exception as e:
            llm_span.set_attribute("error", True)
            llm_span.set_attribute("error.message", str(e))
            llm_span.set_attribute("error.type", type(e).__name__)
            await self._send(send, {
                "type": "interviewer_text",
                "content": "",
                "done": True,
            })
            raise LLMError(
                f"Interviewer error: {e}",
                partial_response=full_response or None,
            )
        finally:
            llm_span.set_attribute("response_length", len(full_response))
            llm_span.end()

        await self._send(send, {
            "type": "interviewer_text",
            "content": "",
            "done": True,
        })

        # Save interviewer message
        raw_resp = usage_data[0] if usage_data else None
        self._sequence += 1
        async with self.deps.pool.acquire() as conn:
            await insert_message(
                conn,
                MessageCreate(
                    session_id=self._session_id,
                    sequence=self._sequence,
                    role=MessageRole.interviewer,
                    content=full_response,
                    raw_response=raw_resp,
                ),
            )

        # TTS
        if self._tts_enabled and full_response:
            self._tts_task = asyncio.create_task(
                self._stream_tts(full_response, send)
            )
        else:
            await self._send(send, {"type": "interviewer_done"})

        self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
        await self._send(send, {
            "type": "state",
            "state": self._state.state.value,
        })
        timer_elapsed = 0
        if self._started_at is not None:
            now = datetime.now(timezone.utc)
            started = self._started_at
            if started.tzinfo is None:
                started = started.replace(tzinfo=timezone.utc)
            timer_elapsed = int((now - started).total_seconds())
        await self._send(send, {
            "type": "timer",
            "elapsed_seconds": timer_elapsed,
        })

    async def _do_edit_transcript(self, msg: WSMessage, send: SendFn) -> None:
        """Update the last candidate message content in the database."""
        if not self._session_id:
            await self._send(send, {
                "type": "error",
                "message": "No active session",
            })
            return

        async with self.deps.pool.acquire() as conn:
            await conn.execute(
                "UPDATE messages SET content = $1 WHERE session_id = $2 AND sequence = $3",
                msg.text,
                self._session_id,
                self._last_candidate_sequence,
            )

    async def _do_end_session(self, msg: WSMessage, send: SendFn) -> None:
        """End the session: finalize in DB, notify client."""
        with tracer.start_as_current_span("interview.end_session", attributes={
            "session_id": self._session_id or 0,
            "duration_sec": self._state.elapsed_seconds or 0,
            "turn_count": self._state.turn_count,
        }):
            await self._do_end_session_inner(msg, send)

    async def _do_end_session_inner(self, msg: WSMessage, send: SendFn) -> None:
        self.cancel_tts()
        self._state.transition(SessionState.ENDING)
        self._state.transition(SessionState.ENDED)

        duration = None
        if self._started_at is not None:
            now = datetime.now(timezone.utc)
            started = self._started_at
            if started.tzinfo is None:
                started = started.replace(tzinfo=timezone.utc)
            duration = int((now - started).total_seconds())

        async with self.deps.pool.acquire() as conn:
            await update_session_status(
                conn,
                self._session_id,
                SessionStatus.completed.value,
                duration_seconds=duration,
                turn_count=self._state.turn_count,
                audio_dir=self._session_dir,
            )

        await self._send(send, {
            "type": "session_ended",
            "session_id": self._session_id,
        })

    # ------------------------------------------------------------------
    # Streaming helpers
    # ------------------------------------------------------------------

    async def _stream_interviewer_opening(self, send: SendFn) -> tuple[str, dict | None]:
        """Get and stream the opening question. Returns (full text, usage dict or None)."""
        full = ""
        usage_data: list = []
        async for token in self._interviewer.get_opening(
            self.deps.anthropic_client, self._system_prompt, usage_out=usage_data
        ):
            full += token
            await self._send(send, {
                "type": "interviewer_text",
                "content": token,
                "done": False,
            })
        await self._send(send, {
            "type": "interviewer_text",
            "content": "",
            "done": True,
        })
        return full, (usage_data[0] if usage_data else None)

    async def _stream_tts(self, text: str, send: SendFn) -> None:
        """Stream TTS audio. Fire-and-forget, cancellable."""
        tts_span = tracer.start_span("speech.tts", attributes={
            "text_length": len(text),
        })
        try:
            chunk_index = 0
            async for chunk in generate_tts(
                self.deps.openai_client,
                text,
                voice=self.deps.settings.tts_voice,
            ):
                if self._session_dir:
                    self.deps.storage.save_audio_chunk(
                        self._session_dir,
                        "audio_out",
                        self._sequence,
                        chunk_index,
                        chunk,
                        "mp3",
                    )
                    chunk_index += 1
                await self._send(send, {
                    "type": "interviewer_audio",
                    "data": base64.b64encode(chunk).decode(),
                })
            await self._send(send, {"type": "interviewer_done"})
            tts_span.set_attribute("chunks", chunk_index)
            tts_span.end()
        except asyncio.CancelledError:
            tts_span.set_attribute("cancelled", True)
            tts_span.end()
        except Exception as e:
            logger.warning("TTS failed for session %d: %s", self._session_id, e)
            tts_span.set_attribute("error", True)
            tts_span.set_attribute("error.message", str(e))
            tts_span.set_attribute("error.type", type(e).__name__)
            tts_span.end()
            await self._send(send, {
                "type": "tts_error",
                "message": "Audio unavailable, continuing with text.",
            })
            await self._send(send, {"type": "interviewer_done"})  # Graceful degradation

    async def _finalize_if_active(self, send: SendFn) -> None:
        """Save session if it was active when shutdown/disconnect happened."""
        if self._session_id and self._state.is_active:
            # Transition to terminal state
            try:
                self._state.transition(SessionState.ENDING)
                self._state.transition(SessionState.ENDED)
            except InvalidTransition:
                pass  # Best-effort during shutdown

            duration = None
            if self._started_at is not None:
                now = datetime.now(timezone.utc)
                started = self._started_at
                if started.tzinfo is None:
                    started = started.replace(tzinfo=timezone.utc)
                duration = int((now - started).total_seconds())

            async with self.deps.pool.acquire() as conn:
                await update_session_status(
                    conn,
                    self._session_id,
                    SessionStatus.completed.value,
                    duration_seconds=duration,
                    turn_count=self._state.turn_count,
                    audio_dir=self._session_dir,
                )
