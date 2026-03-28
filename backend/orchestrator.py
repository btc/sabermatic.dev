"""Interview orchestrator — sequential message queue processor.

Architecture: one asyncio.Queue, one consumer coroutine. Messages are
processed strictly one at a time, eliminating race conditions by
construction. The only concurrent operation is TTS playback, which runs
as a fire-and-forget asyncio.Task with a cancellation handle.

Usage::

    orch = InterviewOrchestrator(deps)
    await orch.enqueue(WSMessage(type="start", ...))
    await orch.run(send=websocket.send_json)
"""

from __future__ import annotations

import asyncio
import base64
import logging
from dataclasses import dataclass
from typing import Any, Callable

from backend.config import Settings
from backend.database import (
    get_latest_coach_review,
    get_question,
    get_session_messages,
    insert_message,
    insert_session,
    update_session_status,
)
from backend.interviewer import Interviewer, InterviewerConfig
from backend.messages import WSMessage
from backend.models import (
    MessageCreate,
    MessageRole,
    Question,
    SessionCreate,
    SessionStatus,
)
from backend.session_manager import InvalidTransition, SessionState, SessionStateMachine
from backend.speech import generate_tts, transcribe_audio
from backend.storage import SessionStorage

logger = logging.getLogger(__name__)


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
        self._tts_enabled: bool = True
        self._tts_task: asyncio.Task | None = None
        self._timer_sec: int = 2700
        self._briefing: str | None = None

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
                    "message": f"Invalid state transition: {e}",
                })
            except Exception as e:
                logger.exception("Unhandled error processing message %s", msg.type)
                await self._send(send, {"type": "error", "message": str(e)})

    async def _handle(self, msg: WSMessage, send: SendFn) -> None:
        """Dispatch a single message to the appropriate handler."""
        match msg.type:
            case "start":
                await self._do_start(msg, send)
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

    async def _do_start(self, msg: WSMessage, send: SendFn) -> None:
        """Create session, stream opening question, optionally start TTS."""
        self._tts_enabled = msg.tts_enabled if msg.tts_enabled is not None else True
        self._timer_sec = msg.timer_sec or 2700
        self._state.transition(SessionState.STARTING)

        # Look up question from DB
        async with self.deps.pool.acquire() as conn:
            self._question = await get_question(conn, msg.question_id)
            if not self._question:
                await self._send(send, {
                    "type": "error",
                    "message": "Question not found",
                })
                # Reset to IDLE so session can be retried
                self._state = SessionStateMachine()
                return

            # Create session
            session = await insert_session(
                conn,
                SessionCreate(
                    question_id=msg.question_id,
                    timer_setting_sec=self._timer_sec,
                    interviewer_briefed=msg.briefed or False,
                ),
            )
            self._session_id = session.id
            self._session_dir = self.deps.storage.create_session_dir(session.id)

            # Get briefing if requested
            self._briefing = None
            if msg.briefed:
                review = await get_latest_coach_review(conn)
                if review:
                    self._briefing = review.recommendation

        # Build system prompt
        self._system_prompt = self._interviewer.build_system_prompt(
            question_title=self._question.title,
            question_prompt=self._question.prompt,
            timer_sec=self._timer_sec,
            elapsed_sec=0,
            briefing=self._briefing,
        )

        self._state.transition(SessionState.INTERVIEWER_SPEAKING)

        # Stream opening message
        full_response = await self._stream_interviewer_opening(send)

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

    async def _do_end_turn(self, msg: WSMessage, send: SendFn) -> None:
        """Transcribe audio, then get interviewer response. Sequential."""
        self.cancel_tts()
        self._state.transition(SessionState.CANDIDATE_SPEAKING)
        self._state.transition(SessionState.PROCESSING)
        await self._send(send, {"type": "state", "state": "processing"})

        # 1. Transcribe
        transcript = await transcribe_audio(
            self.deps.openai_client, msg.audio_data
        )
        if not transcript or not transcript.strip():
            await self._send(send, {
                "type": "error",
                "message": "Could not transcribe audio. Try again or type instead.",
            })
            # Recover: go back to waiting for candidate
            # PROCESSING -> INTERVIEWER_SPEAKING -> WAITING_FOR_CANDIDATE
            self._state.transition(SessionState.INTERVIEWER_SPEAKING)
            self._state.transition(SessionState.WAITING_FOR_CANDIDATE)
            await self._send(send, {
                "type": "state",
                "state": self._state.state.value,
            })
            return

        await self._send(send, {"type": "transcription", "text": transcript})

        # 2. Save candidate message
        self._sequence += 1
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
        self.cancel_tts()
        self._state.transition(SessionState.CANDIDATE_SPEAKING)
        self._state.transition(SessionState.PROCESSING)
        await self._send(send, {"type": "state", "state": "processing"})

        # Save candidate message (no raw_content since it was typed)
        self._sequence += 1
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

        # Rebuild system prompt with current elapsed time
        elapsed = self._state.elapsed_seconds or 0
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
        stream_errored = False
        try:
            async for token in self._interviewer.get_response_stream(
                self.deps.anthropic_client,
                self._system_prompt,
                api_messages,
            ):
                full_response += token
                await self._send(send, {
                    "type": "interviewer_text",
                    "content": token,
                    "done": False,
                })
        except Exception as e:
            stream_errored = True
            # Close the text stream for the client
            await self._send(send, {
                "type": "interviewer_text",
                "content": "",
                "done": True,
            })
            await self._send(send, {
                "type": "error",
                "message": f"Interviewer error: {e}",
            })

        if not stream_errored:
            await self._send(send, {
                "type": "interviewer_text",
                "content": "",
                "done": True,
            })

        # Save interviewer message
        self._sequence += 1
        async with self.deps.pool.acquire() as conn:
            await insert_message(
                conn,
                MessageCreate(
                    session_id=self._session_id,
                    sequence=self._sequence,
                    role=MessageRole.interviewer,
                    content=full_response or "(error - no response)",
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
        await self._send(send, {
            "type": "timer",
            "elapsed_seconds": self._state.elapsed_seconds or 0,
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
                self._sequence,
            )

    async def _do_end_session(self, msg: WSMessage, send: SendFn) -> None:
        """End the session: finalize in DB, notify client."""
        self.cancel_tts()
        self._state.transition(SessionState.ENDING)
        self._state.transition(SessionState.ENDED)

        async with self.deps.pool.acquire() as conn:
            await update_session_status(
                conn,
                self._session_id,
                SessionStatus.completed.value,
                duration_seconds=self._state.elapsed_seconds,
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

    async def _stream_interviewer_opening(self, send: SendFn) -> str:
        """Get and stream the opening question. Returns full text."""
        full = ""
        async for token in self._interviewer.get_opening(
            self.deps.anthropic_client, self._system_prompt
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
        return full

    async def _stream_tts(self, text: str, send: SendFn) -> None:
        """Stream TTS audio. Fire-and-forget, cancellable."""
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
        except asyncio.CancelledError:
            pass  # Expected on interrupt
        except Exception:
            logger.exception("TTS streaming error")
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

            async with self.deps.pool.acquire() as conn:
                await update_session_status(
                    conn,
                    self._session_id,
                    SessionStatus.completed.value,
                    duration_seconds=self._state.elapsed_seconds,
                    turn_count=self._state.turn_count,
                    audio_dir=self._session_dir,
                )
