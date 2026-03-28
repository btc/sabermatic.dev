"""WebSocket message types for the interview orchestrator.

Provides a typed dataclass for all messages flowing through the orchestrator's
queue, plus a parser that converts raw JSON dicts from the WebSocket into
typed WSMessage instances.
"""

from __future__ import annotations

import base64
from dataclasses import dataclass


@dataclass
class WSMessage:
    """Typed representation of a WebSocket message.

    Only ``type`` is required. Other fields are populated depending on message type:
    - start: question_id, timer_sec, tts_enabled, briefed
    - end_turn: audio_data
    - text_input: text
    - edit_transcript: text
    - end_session: (no extra fields)
    - shutdown: (no extra fields)
    """

    type: str
    question_id: int | None = None
    timer_sec: int | None = None
    tts_enabled: bool | None = None
    briefed: bool | None = None
    audio_data: bytes | None = None
    text: str | None = None


def parse_ws_message(raw: dict) -> WSMessage:
    """Parse a raw JSON dict from the WebSocket into a typed WSMessage.

    Audio data arrives as base64-encoded strings and is decoded to bytes.
    All other fields are passed through directly.

    Args:
        raw: Dictionary parsed from the WebSocket JSON frame.

    Returns:
        A WSMessage with the appropriate fields populated.

    Raises:
        KeyError: If the required ``type`` field is missing.
    """
    return WSMessage(
        type=raw["type"],
        question_id=raw.get("question_id"),
        timer_sec=raw.get("timer_sec"),
        tts_enabled=raw.get("tts_enabled"),
        briefed=raw.get("briefed"),
        audio_data=base64.b64decode(raw["audio_data"]) if "audio_data" in raw else None,
        text=raw.get("text"),
    )
