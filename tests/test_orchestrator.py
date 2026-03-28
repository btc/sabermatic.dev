"""Tests for backend.orchestrator — Interview orchestrator with sequential queue."""

import asyncio
from contextlib import asynccontextmanager
from datetime import datetime
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

from backend.config import Settings
from backend.messages import WSMessage, parse_ws_message
from backend.models import (
    Message,
    MessageRole,
    Question,
    Session,
    SessionStatus,
    Difficulty,
    QuestionSource,
)
from backend.orchestrator import InterviewOrchestrator, OrchestratorDeps
from backend.storage import SessionStorage


# ---------------------------------------------------------------------------
# Helpers for building mocks
# ---------------------------------------------------------------------------

def _make_mock_question() -> Question:
    """A fake question for tests."""
    return Question(
        id=1,
        title="URL Shortener",
        prompt="Design a URL shortening service like bit.ly.",
        difficulty=Difficulty.medium,
        tags=["system-design"],
        source=QuestionSource.seed,
        created_at=datetime(2025, 1, 1),
    )


def _make_mock_session(session_id: int = 42) -> Session:
    """A fake session returned by insert_session."""
    return Session(
        id=session_id,
        question_id=1,
        status=SessionStatus.active,
        timer_setting_sec=2700,
        interviewer_briefed=False,
        started_at=datetime(2025, 1, 1),
    )


def _make_mock_message(
    msg_id: int, session_id: int, sequence: int, role: MessageRole, content: str
) -> Message:
    return Message(
        id=msg_id,
        session_id=session_id,
        sequence=sequence,
        role=role,
        content=content,
        timestamp=datetime(2025, 1, 1),
    )


def _make_anthropic_stream_mock(tokens: list[str]):
    """Create a mock Anthropic client whose messages.stream yields tokens."""

    # Each call to .stream() needs a fresh async iterator, so use side_effect
    def make_stream(**kwargs):
        async def fresh_text_iter():
            for t in tokens:
                yield t

        stream = MagicMock()
        stream.__aenter__ = AsyncMock(return_value=stream)
        stream.__aexit__ = AsyncMock(return_value=False)
        stream.text_stream = fresh_text_iter()
        return stream

    mock_client = MagicMock()
    mock_client.messages = MagicMock()
    mock_client.messages.stream = MagicMock(side_effect=make_stream)
    return mock_client


def _make_openai_mock(transcription_text: str = "I would start by gathering requirements."):
    """Create a mock OpenAI client for both Whisper and TTS."""
    mock_client = AsyncMock()
    mock_client.audio = MagicMock()

    # Whisper transcription
    mock_client.audio.transcriptions = MagicMock()
    mock_client.audio.transcriptions.create = AsyncMock(
        return_value=MagicMock(text=transcription_text)
    )

    # TTS — return an async iterator of chunks
    mock_tts_response = AsyncMock()

    async def mock_iter_bytes(chunk_size=None):
        for chunk in [b"audio_chunk_1", b"audio_chunk_2"]:
            yield chunk

    mock_tts_response.iter_bytes = MagicMock(return_value=mock_iter_bytes())
    mock_client.audio.speech = MagicMock()
    mock_client.audio.speech.create = AsyncMock(return_value=mock_tts_response)

    return mock_client


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------

@pytest.fixture
def mock_deps():
    """Provide an OrchestratorDeps with all mocks wired up.

    The mock pool returns a mock connection via acquire(). Database functions
    are patched at the module level in individual tests.
    """
    mock_question = _make_mock_question()
    mock_session = _make_mock_session()

    # Mock connection and pool
    mock_conn = AsyncMock()
    mock_conn.execute = AsyncMock()
    mock_conn.fetchrow = AsyncMock()
    mock_conn.fetch = AsyncMock(return_value=[])

    mock_pool = MagicMock()

    @asynccontextmanager
    async def fake_acquire():
        yield mock_conn

    mock_pool.acquire = fake_acquire

    anthropic_client = _make_anthropic_stream_mock(
        ["Welcome", " to", " the", " interview."]
    )
    openai_client = _make_openai_mock()

    settings = Settings(
        anthropic_api_key="test-key",
        openai_api_key="test-key",
        interviewer_model="claude-sonnet-4-20250514",
        tts_voice="onyx",
    )

    storage = MagicMock(spec=SessionStorage)
    storage.create_session_dir.return_value = "/tmp/test_session"
    storage.save_audio_chunk.return_value = "/tmp/test_session/audio_out/chunk.mp3"

    deps = OrchestratorDeps(
        pool=mock_pool,
        anthropic_client=anthropic_client,
        openai_client=openai_client,
        settings=settings,
        storage=storage,
    )

    # Attach the mocks so tests can inspect or reconfigure them
    deps._mock_conn = mock_conn
    deps._mock_question = mock_question
    deps._mock_session = mock_session

    return deps


# ---------------------------------------------------------------------------
# Message parsing tests
# ---------------------------------------------------------------------------

class TestWSMessageParsing:
    def test_parse_start_message(self):
        raw = {
            "type": "start",
            "question_id": 1,
            "timer_sec": 2700,
            "tts_enabled": False,
            "briefed": False,
        }
        msg = parse_ws_message(raw)
        assert msg.type == "start"
        assert msg.question_id == 1
        assert msg.timer_sec == 2700
        assert msg.tts_enabled is False
        assert msg.briefed is False
        assert msg.audio_data is None

    def test_parse_end_turn_with_audio(self):
        import base64

        raw = {
            "type": "end_turn",
            "audio_data": base64.b64encode(b"fake-audio").decode(),
        }
        msg = parse_ws_message(raw)
        assert msg.type == "end_turn"
        assert msg.audio_data == b"fake-audio"

    def test_parse_text_input(self):
        raw = {"type": "text_input", "text": "Hello"}
        msg = parse_ws_message(raw)
        assert msg.type == "text_input"
        assert msg.text == "Hello"

    def test_parse_shutdown(self):
        raw = {"type": "shutdown"}
        msg = parse_ws_message(raw)
        assert msg.type == "shutdown"

    def test_parse_missing_type_raises(self):
        with pytest.raises(KeyError):
            parse_ws_message({})


# ---------------------------------------------------------------------------
# Orchestrator tests
# ---------------------------------------------------------------------------

# Common patch set for tests that go through the full start flow
_DB_PATCHES = [
    "backend.orchestrator.update_session_status",
    "backend.orchestrator.get_session_messages",
    "backend.orchestrator.insert_message",
    "backend.orchestrator.insert_session",
    "backend.orchestrator.get_question",
]


def _setup_db_mocks(mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps):
    """Wire up the standard DB mock returns."""
    mock_get_q.return_value = mock_deps._mock_question
    mock_insert_sess.return_value = mock_deps._mock_session
    mock_insert_msg.return_value = _make_mock_message(
        1, 42, 1, MessageRole.interviewer, "Welcome"
    )
    mock_get_msgs.return_value = []
    mock_update_status.return_value = mock_deps._mock_session


class TestOrchestratorStartAndEndTurn:
    """Full turn: start -> end_turn -> transcription + interviewer response."""

    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.insert_session")
    @patch("backend.orchestrator.get_question")
    async def test_start_then_end_turn_flow(
        self, mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps
    ):
        _setup_db_mocks(mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)
        # Return messages for context when getting interviewer response
        mock_get_msgs.return_value = [
            _make_mock_message(1, 42, 1, MessageRole.interviewer, "Welcome to the interview."),
            _make_mock_message(2, 42, 2, MessageRole.candidate, "I would start by gathering requirements."),
        ]

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=1,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        await orch.enqueue(
            WSMessage(type="end_turn", audio_data=b"fake-audio")
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        assert "interviewer_text" in types
        assert "transcription" in types

    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.insert_session")
    @patch("backend.orchestrator.get_question")
    async def test_start_sends_state_updates(
        self, mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps
    ):
        _setup_db_mocks(mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=1,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        assert "state" in types
        assert "interviewer_done" in types


class TestTextInput:
    """Text input bypasses Whisper, goes to Claude directly."""

    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.insert_session")
    @patch("backend.orchestrator.get_question")
    async def test_text_input_skips_whisper(
        self, mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps
    ):
        _setup_db_mocks(mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)
        mock_get_msgs.return_value = [
            _make_mock_message(1, 42, 1, MessageRole.interviewer, "Welcome"),
            _make_mock_message(2, 42, 2, MessageRole.candidate, "My text input"),
        ]

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=1,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        await orch.enqueue(
            WSMessage(type="text_input", text="I want to start with requirements.")
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        # Text input should NOT produce a transcription message
        assert "transcription" not in types
        # But should still get an interviewer response
        assert "interviewer_text" in types


class TestErrorHandling:
    """Error conditions send error messages but don't crash the loop."""

    async def test_end_turn_before_start_sends_error(self, mock_deps):
        """end_turn before start -> InvalidTransition -> error message."""
        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(type="end_turn", audio_data=b"fake-audio")
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        assert "error" in types
        # The loop should NOT have crashed — it processed shutdown cleanly
        error_msgs = [r for r in results if r["type"] == "error"]
        assert len(error_msgs) >= 1

    @patch("backend.orchestrator.transcribe_audio")
    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.insert_session")
    @patch("backend.orchestrator.get_question")
    async def test_whisper_empty_transcription_sends_error(
        self,
        mock_get_q,
        mock_insert_sess,
        mock_insert_msg,
        mock_get_msgs,
        mock_update_status,
        mock_transcribe,
        mock_deps,
    ):
        """Empty transcription -> error message, loop continues."""
        _setup_db_mocks(mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)
        mock_transcribe.return_value = ""  # Empty transcription

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=1,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        await orch.enqueue(
            WSMessage(type="end_turn", audio_data=b"fake-audio")
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        error_msgs = [r for r in results if r["type"] == "error"]
        assert any("transcribe" in e["message"].lower() for e in error_msgs)
        # Loop should have continued past the error (shutdown processed)
        # Also should NOT have a transcription message
        assert "transcription" not in types

    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.insert_session")
    @patch("backend.orchestrator.get_question")
    async def test_claude_stream_error_saves_partial(
        self, mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps
    ):
        """Claude errors mid-stream -> partial text saved, error sent, loop continues."""
        _setup_db_mocks(mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)
        mock_get_msgs.return_value = [
            _make_mock_message(1, 42, 1, MessageRole.interviewer, "Welcome"),
            _make_mock_message(2, 42, 2, MessageRole.candidate, "Let me think about this."),
        ]

        # Make the second stream call (for response after text_input) raise an error
        call_count = [0]

        def make_stream(**kwargs):
            call_count[0] += 1
            if call_count[0] == 1:
                # First call (opening) succeeds
                async def ok_iter():
                    for t in ["Welcome", " to", " the", " interview."]:
                        yield t

                stream = MagicMock()
                stream.__aenter__ = AsyncMock(return_value=stream)
                stream.__aexit__ = AsyncMock(return_value=False)
                stream.text_stream = ok_iter()
                return stream
            else:
                # Second call (response) yields partial then errors
                async def error_iter():
                    yield "Partial"
                    yield " response"
                    raise RuntimeError("API connection lost")

                stream = MagicMock()
                stream.__aenter__ = AsyncMock(return_value=stream)
                stream.__aexit__ = AsyncMock(return_value=False)
                stream.text_stream = error_iter()
                return stream

        mock_deps.anthropic_client.messages.stream = MagicMock(side_effect=make_stream)

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=1,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        await orch.enqueue(
            WSMessage(type="text_input", text="Let me think about this.")
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        error_msgs = [r for r in results if r["type"] == "error"]
        # Should have gotten an error about the interviewer
        assert any("interviewer" in e["message"].lower() or "api" in e["message"].lower() for e in error_msgs)
        # The partial text should have been streamed
        text_msgs = [r for r in results if r["type"] == "interviewer_text" and r.get("content")]
        partial_text = "".join(r["content"] for r in text_msgs)
        assert "Partial" in partial_text

        # insert_message should have been called to save partial
        assert mock_insert_msg.call_count >= 3  # opening + candidate + partial interviewer

    async def test_unknown_message_type_sends_error(self, mock_deps):
        """Unknown message type -> error, loop continues."""
        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(WSMessage(type="bogus"))
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        assert "error" in types
        error_msgs = [r for r in results if r["type"] == "error"]
        assert any("unknown" in e["message"].lower() for e in error_msgs)


class TestSequentialProcessing:
    """Two rapid end_turns never overlap — max 1 concurrent transcription."""

    @patch("backend.orchestrator.transcribe_audio")
    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.insert_session")
    @patch("backend.orchestrator.get_question")
    async def test_messages_are_sequential(
        self,
        mock_get_q,
        mock_insert_sess,
        mock_insert_msg,
        mock_get_msgs,
        mock_update_status,
        mock_transcribe,
        mock_deps,
    ):
        """Two rapid end_turns never overlap — they execute one at a time."""
        _setup_db_mocks(mock_get_q, mock_insert_sess, mock_insert_msg, mock_get_msgs, mock_update_status, mock_deps)
        mock_get_msgs.return_value = [
            _make_mock_message(1, 42, 1, MessageRole.interviewer, "Welcome"),
        ]

        max_concurrent = [0]
        current = [0]

        original_return = "I would start by gathering requirements."

        async def tracked_transcribe(client, audio_data, **kwargs):
            current[0] += 1
            max_concurrent[0] = max(max_concurrent[0], current[0])
            await asyncio.sleep(0.01)  # Small delay to detect overlap
            current[0] -= 1
            return original_return

        mock_transcribe.side_effect = tracked_transcribe

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=1,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        # Enqueue two end_turns rapidly
        await orch.enqueue(
            WSMessage(type="end_turn", audio_data=b"audio-1")
        )
        await orch.enqueue(
            WSMessage(type="end_turn", audio_data=b"audio-2")
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        # Max concurrency should be 1 — messages are sequential
        assert max_concurrent[0] == 1


class TestQuestionNotFound:
    """start with nonexistent question -> error, doesn't crash."""

    @patch("backend.orchestrator.get_question")
    async def test_start_with_missing_question(self, mock_get_q, mock_deps):
        mock_get_q.return_value = None

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=999,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        types = [r["type"] for r in results]
        assert "error" in types
        error_msgs = [r for r in results if r["type"] == "error"]
        assert any("not found" in e["message"].lower() for e in error_msgs)


class TestShutdownFinalization:
    """Shutdown while active -> session finalized in DB."""

    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.insert_session")
    @patch("backend.orchestrator.get_question")
    async def test_shutdown_finalizes_active_session(
        self, mock_get_q, mock_insert_sess, mock_insert_msg, mock_update_status, mock_deps
    ):
        mock_get_q.return_value = mock_deps._mock_question
        mock_insert_sess.return_value = mock_deps._mock_session
        mock_insert_msg.return_value = _make_mock_message(
            1, 42, 1, MessageRole.interviewer, "Welcome"
        )
        mock_update_status.return_value = mock_deps._mock_session

        orch = InterviewOrchestrator(mock_deps)
        results = []
        await orch.enqueue(
            WSMessage(
                type="start",
                question_id=1,
                timer_sec=2700,
                tts_enabled=False,
                briefed=False,
            )
        )
        await orch.enqueue(WSMessage(type="shutdown"))
        await orch.run(send=results.append)

        # update_session_status should have been called during finalization
        mock_update_status.assert_called_once()
        call_args = mock_update_status.call_args
        assert call_args[0][1] == 42  # session_id
        assert call_args[0][2] == "completed"  # status
