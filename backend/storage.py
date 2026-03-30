"""Filesystem operations for audio, transcripts, and traces."""

import json
import os


class SessionStorage:
    """Manages the on-disk layout for a drill session."""

    def __init__(self, base_dir: str = "./data") -> None:
        self.base_dir = os.path.abspath(base_dir)
        self.sessions_dir = os.path.join(self.base_dir, "sessions")
        self.traces_dir = os.path.join(self.base_dir, "traces")

    def create_session_dir(self, session_id: int) -> str:
        """Create directory structure: session_NNN/audio_in/, audio_out/.

        Returns the absolute path to the session directory.
        """
        session_dir = os.path.join(self.sessions_dir, f"session_{session_id:03d}")
        os.makedirs(os.path.join(session_dir, "audio_in"), exist_ok=True)
        os.makedirs(os.path.join(session_dir, "audio_out"), exist_ok=True)
        return session_dir

    def save_audio_chunk(
        self,
        session_dir: str,
        direction: str,
        turn: int,
        chunk_index: int,
        data: bytes,
        format: str = "webm",
    ) -> str:
        """Save an audio chunk.

        Filename: turn_003_chunk_001.webm
        Returns the absolute path to the saved file.
        """
        filename = f"turn_{turn:03d}_chunk_{chunk_index:03d}.{format}"
        path = os.path.join(session_dir, direction, filename)
        with open(path, "wb") as f:
            f.write(data)
        return path

    def save_transcript(self, session_dir: str, transcript: dict[str, object]) -> str:
        """Save a transcript dict as JSON.

        Returns the absolute path to the saved file.
        """
        path = os.path.join(session_dir, "transcript.json")
        with open(path, "w", encoding="utf-8") as f:
            json.dump(transcript, f, indent=2)
        return path

    def save_evaluation_json(self, session_dir: str, evaluation: dict[str, object]) -> str:
        """Save a raw evaluation response as JSON.

        Returns the absolute path to the saved file.
        """
        path = os.path.join(session_dir, "evaluation.json")
        with open(path, "w", encoding="utf-8") as f:
            json.dump(evaluation, f, indent=2)
        return path

    def ensure_trace_dir(self, date_str: str) -> str:
        """Create a date-based trace directory under traces/.

        Returns the absolute path to the directory.
        """
        path = os.path.join(self.traces_dir, date_str)
        os.makedirs(path, exist_ok=True)
        return path
