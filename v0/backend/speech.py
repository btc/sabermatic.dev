"""Wrappers around OpenAI Whisper and TTS APIs.

Both functions take the AsyncOpenAI client as an argument (dependency injection)
so they are easy to test and don't depend on global state.
"""

from io import BytesIO
from typing import AsyncIterator

from openai import AsyncOpenAI


async def transcribe_audio(
    client: AsyncOpenAI,
    audio_data: bytes,
    format: str = "webm",
    model: str = "whisper-1",
) -> str:
    """Transcribe audio bytes using OpenAI Whisper.

    Args:
        client: An AsyncOpenAI client instance.
        audio_data: Raw audio bytes to transcribe.
        format: Audio format extension (e.g., "webm", "mp3", "wav").
        model: Whisper model to use.

    Returns:
        The transcribed text.
    """
    filename = f"audio.{format}"
    buf = BytesIO(audio_data)

    response = await client.audio.transcriptions.create(
        model=model,
        file=(filename, buf),
    )

    return response.text


async def generate_tts(
    client: AsyncOpenAI,
    text: str,
    voice: str = "onyx",
    model: str = "tts-1",
) -> AsyncIterator[bytes]:
    """Generate text-to-speech audio using OpenAI TTS, yielding chunks.

    Uses ``with_streaming_response`` so audio bytes are streamed as they
    arrive from the API rather than buffered in memory first.

    Args:
        client: An AsyncOpenAI client instance.
        text: The text to convert to speech.
        voice: The voice to use (e.g., "onyx", "nova", "alloy").
        model: The TTS model to use.

    Yields:
        Chunks of audio bytes (MP3).
    """
    async with client.audio.speech.with_streaming_response.create(
        model=model,
        voice=voice,
        input=text,
    ) as response:
        async for chunk in response.iter_bytes():
            yield chunk
