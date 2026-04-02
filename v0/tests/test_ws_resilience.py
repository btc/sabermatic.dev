"""Test that malformed WebSocket messages don't crash the connection."""

import json
import time
from unittest.mock import AsyncMock, MagicMock

import pytest
from starlette.testclient import TestClient

from backend.config import Settings
from backend.main import create_app


def _make_anthropic_stream_mock(tokens: list[str]):
    def make_stream(**kwargs):
        async def token_iter():
            for t in tokens:
                yield t
        final_usage = MagicMock()
        final_usage.input_tokens = 100
        final_usage.output_tokens = 50
        final_message = MagicMock()
        final_message.model = "claude-sonnet-4-20250514"
        final_message.usage = final_usage
        stream = MagicMock()
        stream.__aenter__ = AsyncMock(return_value=stream)
        stream.__aexit__ = AsyncMock(return_value=False)
        stream.text_stream = token_iter()
        stream.get_final_message = AsyncMock(return_value=final_message)
        return stream
    mock_client = MagicMock()
    mock_client.messages = MagicMock()
    mock_client.messages.stream = MagicMock(side_effect=make_stream)
    return mock_client


@pytest.fixture(scope="module")
def test_app(test_database):
    settings = Settings(
        anthropic_api_key="test", openai_api_key="test",
        database_url=test_database, _env_file=None,
    )
    return create_app(database_url=test_database, settings=settings)


def _collect_until(ws, predicate, timeout=5.0):
    messages = []
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        data = json.loads(ws.receive_text())
        messages.append(data)
        if predicate(data):
            return messages
    raise TimeoutError(f"Timeout. Got: {[m['type'] for m in messages]}")


class TestMalformedMessages:
    def test_bad_json_does_not_kill_connection(self, test_app):
        """Send garbage, then a valid message -- connection survives."""
        mock_anthropic = _make_anthropic_stream_mock(["Welcome."])
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            session_id = client.post("/api/sessions", json={
                "question_id": questions[0]["id"],
                "timer_setting_sec": 2700, "tts_enabled": False,
            }).json()["id"]

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                # Wait for load
                _collect_until(ws, lambda m: m.get("type") == "state" and m.get("state") == "waiting_for_candidate")

                # Send garbage
                ws.send_text("this is not json {{{")
                ws.send_text('{"type": "unknown_type_xyz"}')

                # Send valid end_session -- should still work
                ws.send_text(json.dumps({"type": "end_session"}))
                ended = _collect_until(ws, lambda m: m.get("type") == "session_ended")
                assert any(m["type"] == "session_ended" for m in ended)
