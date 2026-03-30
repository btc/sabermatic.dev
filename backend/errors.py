"""Structured error types for the interview orchestrator.

Each error carries domain-specific context so the orchestrator can make
informed recovery decisions and the client gets actionable messages.
"""


class InterviewError(Exception):
    """Base class for all interview domain errors."""

    def to_ws_payload(self) -> dict[str, str]:
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
