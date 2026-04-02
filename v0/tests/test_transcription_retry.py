"""Tests for transcription retry logic in the orchestrator.

When Whisper returns empty text, the orchestrator retries once after a 1-second
backoff.  If the retry also fails, a TranscriptionError is raised so the client
can prompt the user to type their response instead.
"""

import asyncio
from unittest.mock import AsyncMock, patch

import pytest

from backend.messages import WSMessage
from backend.orchestrator import InterviewOrchestrator

# Re-use helpers and fixtures from the main orchestrator test module.
from tests.test_orchestrator import (
    _make_mock_message,
    _setup_db_mocks,
    mock_deps,  # noqa: F401 — pytest fixture
)
from backend.models import MessageRole


class TestTranscriptionRetry:
    """Retry empty transcriptions once before raising an error."""

    @patch("backend.orchestrator.transcribe_audio")
    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.get_session")
    @patch("backend.orchestrator.get_question")
    async def test_retry_succeeds_on_second_attempt(
        self,
        mock_get_q,
        mock_get_sess,
        mock_insert_msg,
        mock_get_msgs,
        mock_update_status,
        mock_transcribe,
        mock_deps,  # noqa: F811
    ):
        """First transcription returns empty string, retry returns actual text.

        Verify the transcription is used (no error sent to client).
        """
        _setup_db_mocks(
            mock_get_q, mock_get_sess, mock_insert_msg,
            mock_get_msgs, mock_update_status, mock_deps,
        )
        mock_get_msgs.return_value = [
            _make_mock_message(1, 42, 1, MessageRole.interviewer, "Welcome to the interview."),
            _make_mock_message(2, 42, 2, MessageRole.candidate, "I would start with requirements."),
        ]

        # First call returns empty, second call succeeds
        mock_transcribe.side_effect = ["", "I would start with requirements."]

        orch = InterviewOrchestrator(mock_deps)
        results: list[dict] = []

        with patch("backend.orchestrator.asyncio.sleep", new_callable=AsyncMock) as mock_sleep:
            await orch.enqueue(WSMessage(type="load", session_id=42))
            await orch.enqueue(WSMessage(type="end_turn", audio_data=b"fake-audio"))
            await orch.enqueue(WSMessage(type="shutdown"))
            await orch.run(send=results.append)

            # asyncio.sleep(1) was called for the retry backoff
            mock_sleep.assert_awaited_once_with(1)

        types = [r["type"] for r in results]

        # The successful retry means no error was sent
        error_msgs = [r for r in results if r["type"] == "error"]
        assert not error_msgs, f"Expected no errors, got: {error_msgs}"

        # A transcription message was sent with the retried text
        assert "transcription" in types
        transcription_msg = next(r for r in results if r["type"] == "transcription")
        assert transcription_msg["text"] == "I would start with requirements."

        # transcribe_audio was called exactly twice (initial + retry)
        assert mock_transcribe.call_count == 2

    @patch("backend.orchestrator.transcribe_audio")
    @patch("backend.orchestrator.update_session_status")
    @patch("backend.orchestrator.get_session_messages")
    @patch("backend.orchestrator.insert_message")
    @patch("backend.orchestrator.get_session")
    @patch("backend.orchestrator.get_question")
    async def test_both_retries_fail_sends_error(
        self,
        mock_get_q,
        mock_get_sess,
        mock_insert_msg,
        mock_get_msgs,
        mock_update_status,
        mock_transcribe,
        mock_deps,  # noqa: F811
    ):
        """Both transcription attempts return empty. Verify a TranscriptionError
        -style error is sent to the client.
        """
        _setup_db_mocks(
            mock_get_q, mock_get_sess, mock_insert_msg,
            mock_get_msgs, mock_update_status, mock_deps,
        )
        # Both calls return empty
        mock_transcribe.side_effect = ["", ""]

        orch = InterviewOrchestrator(mock_deps)
        results: list[dict] = []

        with patch("backend.orchestrator.asyncio.sleep", new_callable=AsyncMock):
            await orch.enqueue(WSMessage(type="load", session_id=42))
            await orch.enqueue(WSMessage(type="end_turn", audio_data=b"fake-audio"))
            await orch.enqueue(WSMessage(type="shutdown"))
            await orch.run(send=results.append)

        types = [r["type"] for r in results]

        # An error was sent for the failed transcription
        error_msgs = [r for r in results if r["type"] == "error"]
        assert error_msgs, "Expected a transcription error"
        assert any("transcribe" in e["message"].lower() or "type" in e["message"].lower() for e in error_msgs)

        # The error_kind should match TranscriptionError's snake_case
        assert any(e.get("error_kind") == "transcription_error" for e in error_msgs)

        # No transcription message was sent (both attempts failed)
        assert "transcription" not in types

        # transcribe_audio was called exactly twice (initial + retry)
        assert mock_transcribe.call_count == 2
