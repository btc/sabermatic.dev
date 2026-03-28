"""Tests for backend.storage.SessionStorage — filesystem operations."""

import json
import os

import pytest

from backend.storage import SessionStorage


@pytest.fixture
def storage(tmp_path) -> SessionStorage:
    """SessionStorage rooted in a temp directory."""
    return SessionStorage(base_dir=str(tmp_path))


class TestCreateSessionDir:
    def test_creates_audio_in_subdir(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        assert os.path.isdir(os.path.join(session_dir, "audio_in"))

    def test_creates_audio_out_subdir(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        assert os.path.isdir(os.path.join(session_dir, "audio_out"))

    def test_dir_name_is_zero_padded(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(7)
        assert os.path.basename(session_dir) == "session_007"

    def test_dir_name_for_large_id(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(42)
        assert os.path.basename(session_dir) == "session_042"

    def test_returns_absolute_path(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        assert os.path.isabs(session_dir)

    def test_idempotent_on_second_call(self, storage: SessionStorage) -> None:
        """Calling twice should not raise."""
        storage.create_session_dir(1)
        session_dir = storage.create_session_dir(1)
        assert os.path.isdir(session_dir)


class TestSaveAudioChunk:
    def test_creates_file_in_audio_in(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_audio_chunk(session_dir, "audio_in", turn=0, chunk_index=0, data=b"audio")
        assert os.path.isfile(path)
        assert "audio_in" in path

    def test_creates_file_in_audio_out(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_audio_chunk(session_dir, "audio_out", turn=0, chunk_index=0, data=b"audio")
        assert os.path.isfile(path)
        assert "audio_out" in path

    def test_filename_format(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_audio_chunk(session_dir, "audio_in", turn=3, chunk_index=1, data=b"x")
        assert os.path.basename(path) == "turn_003_chunk_001.webm"

    def test_chunk_index_zero_padded(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_audio_chunk(session_dir, "audio_in", turn=0, chunk_index=12, data=b"x")
        assert os.path.basename(path) == "turn_000_chunk_012.webm"

    def test_custom_format(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_audio_chunk(session_dir, "audio_in", turn=1, chunk_index=0, data=b"x", format="wav")
        assert path.endswith(".wav")

    def test_data_written_correctly(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        payload = b"\x00\x01\x02\x03"
        path = storage.save_audio_chunk(session_dir, "audio_in", turn=0, chunk_index=0, data=payload)
        with open(path, "rb") as f:
            assert f.read() == payload

    def test_multiple_chunks_same_turn_do_not_overwrite(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path0 = storage.save_audio_chunk(session_dir, "audio_in", turn=0, chunk_index=0, data=b"chunk0")
        path1 = storage.save_audio_chunk(session_dir, "audio_in", turn=0, chunk_index=1, data=b"chunk1")
        assert path0 != path1
        assert os.path.isfile(path0)
        assert os.path.isfile(path1)
        with open(path0, "rb") as f:
            assert f.read() == b"chunk0"
        with open(path1, "rb") as f:
            assert f.read() == b"chunk1"

    def test_multiple_turns_do_not_overwrite(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path0 = storage.save_audio_chunk(session_dir, "audio_in", turn=0, chunk_index=0, data=b"turn0")
        path1 = storage.save_audio_chunk(session_dir, "audio_in", turn=1, chunk_index=0, data=b"turn1")
        assert path0 != path1


class TestSaveTranscript:
    def test_creates_transcript_file(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_transcript(session_dir, {"messages": []})
        assert os.path.isfile(path)

    def test_writes_valid_json(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        data = {"messages": [{"role": "interviewer", "content": "Hello"}]}
        path = storage.save_transcript(session_dir, data)
        with open(path) as f:
            loaded = json.load(f)
        assert loaded == data

    def test_returns_path_string(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_transcript(session_dir, {})
        assert isinstance(path, str)

    def test_file_located_in_session_dir(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_transcript(session_dir, {})
        assert os.path.dirname(path) == session_dir


class TestSaveEvaluationJson:
    def test_creates_evaluation_file(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_evaluation_json(session_dir, {"score": 4})
        assert os.path.isfile(path)

    def test_writes_valid_json(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        data = {"score_overall": 3, "strengths": ["good design"]}
        path = storage.save_evaluation_json(session_dir, data)
        with open(path) as f:
            loaded = json.load(f)
        assert loaded == data

    def test_returns_path_string(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_evaluation_json(session_dir, {})
        assert isinstance(path, str)

    def test_file_located_in_session_dir(self, storage: SessionStorage) -> None:
        session_dir = storage.create_session_dir(1)
        path = storage.save_evaluation_json(session_dir, {})
        assert os.path.dirname(path) == session_dir


class TestEnsureTraceDir:
    def test_creates_directory(self, storage: SessionStorage) -> None:
        path = storage.ensure_trace_dir("2026-03-28")
        assert os.path.isdir(path)

    def test_date_in_path(self, storage: SessionStorage) -> None:
        path = storage.ensure_trace_dir("2026-03-28")
        assert "2026-03-28" in path

    def test_returns_path_string(self, storage: SessionStorage) -> None:
        path = storage.ensure_trace_dir("2026-01-01")
        assert isinstance(path, str)

    def test_path_under_traces_dir(self, storage: SessionStorage) -> None:
        path = storage.ensure_trace_dir("2026-03-28")
        assert path.startswith(storage.traces_dir)

    def test_idempotent(self, storage: SessionStorage) -> None:
        storage.ensure_trace_dir("2026-03-28")
        path = storage.ensure_trace_dir("2026-03-28")
        assert os.path.isdir(path)
