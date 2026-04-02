"""Tests for backend.speech — Whisper transcription and TTS wrappers."""

from io import BytesIO
from unittest.mock import AsyncMock, MagicMock

import pytest

from backend.speech import transcribe_audio, generate_tts


class TestTranscribeAudio:
    @pytest.fixture
    def mock_client(self):
        client = AsyncMock()
        client.audio = MagicMock()
        client.audio.transcriptions = MagicMock()
        client.audio.transcriptions.create = AsyncMock()
        return client

    async def test_returns_transcription_text(self, mock_client):
        mock_client.audio.transcriptions.create.return_value = MagicMock(text="Hello world")
        result = await transcribe_audio(mock_client, b"fake audio data")
        assert result == "Hello world"

    async def test_passes_correct_model(self, mock_client):
        mock_client.audio.transcriptions.create.return_value = MagicMock(text="hi")
        await transcribe_audio(mock_client, b"audio", model="whisper-1")
        call_kwargs = mock_client.audio.transcriptions.create.call_args
        assert call_kwargs.kwargs["model"] == "whisper-1"

    async def test_passes_custom_model(self, mock_client):
        mock_client.audio.transcriptions.create.return_value = MagicMock(text="hi")
        await transcribe_audio(mock_client, b"audio", model="whisper-2")
        call_kwargs = mock_client.audio.transcriptions.create.call_args
        assert call_kwargs.kwargs["model"] == "whisper-2"

    async def test_uses_webm_extension_by_default(self, mock_client):
        mock_client.audio.transcriptions.create.return_value = MagicMock(text="hi")
        await transcribe_audio(mock_client, b"audio")
        call_kwargs = mock_client.audio.transcriptions.create.call_args
        file_arg = call_kwargs.kwargs["file"]
        # The file tuple should have the correct filename extension
        assert file_arg[0] == "audio.webm"

    async def test_uses_custom_format_extension(self, mock_client):
        mock_client.audio.transcriptions.create.return_value = MagicMock(text="hi")
        await transcribe_audio(mock_client, b"audio", format="mp3")
        call_kwargs = mock_client.audio.transcriptions.create.call_args
        file_arg = call_kwargs.kwargs["file"]
        assert file_arg[0] == "audio.mp3"

    async def test_passes_audio_data_in_buffer(self, mock_client):
        mock_client.audio.transcriptions.create.return_value = MagicMock(text="hi")
        audio_bytes = b"real audio content here"
        await transcribe_audio(mock_client, audio_bytes)
        call_kwargs = mock_client.audio.transcriptions.create.call_args
        file_arg = call_kwargs.kwargs["file"]
        # file_arg is a tuple (filename, buffer); the buffer should contain our data
        buf = file_arg[1]
        if isinstance(buf, BytesIO):
            buf.seek(0)
            assert buf.read() == audio_bytes
        else:
            assert buf == audio_bytes

    async def test_propagates_api_error(self, mock_client):
        mock_client.audio.transcriptions.create.side_effect = Exception("API error")
        with pytest.raises(Exception, match="API error"):
            await transcribe_audio(mock_client, b"audio")


class TestGenerateTTS:
    @pytest.fixture
    def mock_client(self):
        """Build a mock AsyncOpenAI whose audio.speech.with_streaming_response.create
        returns an async context-manager that yields a response with iter_bytes."""
        client = AsyncMock()
        client.audio = MagicMock()
        client.audio.speech = MagicMock()
        client.audio.speech.with_streaming_response = MagicMock()
        client.audio.speech.with_streaming_response.create = MagicMock()
        return client

    def _make_streaming_response(self, chunks: list[bytes]):
        """Create a mock response usable as ``async with ... as response``
        where ``response.iter_bytes()`` yields the given chunks."""
        response = MagicMock()

        async def _iter_bytes(chunk_size=None):
            for chunk in chunks:
                yield chunk

        response.iter_bytes = _iter_bytes

        # Build an async context manager that yields the response
        ctx = AsyncMock()
        ctx.__aenter__ = AsyncMock(return_value=response)
        ctx.__aexit__ = AsyncMock(return_value=False)
        return ctx

    def _setup_mock(self, mock_client, chunks: list[bytes]):
        """Wire the mock client so with_streaming_response.create returns
        the context manager for the given chunks."""
        ctx = self._make_streaming_response(chunks)
        mock_client.audio.speech.with_streaming_response.create.return_value = ctx
        return ctx

    async def test_yields_audio_chunks(self, mock_client):
        chunks = [b"chunk1", b"chunk2", b"chunk3"]
        self._setup_mock(mock_client, chunks)

        collected = []
        async for chunk in generate_tts(mock_client, "Hello world"):
            collected.append(chunk)

        assert collected == chunks

    async def test_passes_correct_parameters(self, mock_client):
        self._setup_mock(mock_client, [b"data"])

        async for _ in generate_tts(mock_client, "Hello", voice="nova", model="tts-1-hd"):
            pass

        call_kwargs = mock_client.audio.speech.with_streaming_response.create.call_args.kwargs
        assert call_kwargs["model"] == "tts-1-hd"
        assert call_kwargs["voice"] == "nova"
        assert call_kwargs["input"] == "Hello"

    async def test_uses_default_voice_and_model(self, mock_client):
        self._setup_mock(mock_client, [b"data"])

        async for _ in generate_tts(mock_client, "Test"):
            pass

        call_kwargs = mock_client.audio.speech.with_streaming_response.create.call_args.kwargs
        assert call_kwargs["model"] == "tts-1"
        assert call_kwargs["voice"] == "onyx"

    async def test_handles_empty_response(self, mock_client):
        self._setup_mock(mock_client, [])

        collected = []
        async for chunk in generate_tts(mock_client, "Hello"):
            collected.append(chunk)

        assert collected == []

    async def test_propagates_api_error(self, mock_client):
        mock_client.audio.speech.with_streaming_response.create.side_effect = Exception("TTS API error")
        with pytest.raises(Exception, match="TTS API error"):
            async for _ in generate_tts(mock_client, "Hello"):
                pass

    async def test_uses_streaming_response(self, mock_client):
        """Verify that the create call goes through with_streaming_response."""
        self._setup_mock(mock_client, [b"data"])

        async for _ in generate_tts(mock_client, "Test"):
            pass

        mock_client.audio.speech.with_streaming_response.create.assert_called_once()
