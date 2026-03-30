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
