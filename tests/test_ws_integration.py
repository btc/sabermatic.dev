"""Integration tests for the WebSocket interview flow.

Uses Starlette's synchronous TestClient for both HTTP and WebSocket calls.
Real database, mocked external APIs (Anthropic, OpenAI).
"""

import json
import time
from unittest.mock import AsyncMock, MagicMock

import pytest
from starlette.testclient import TestClient

from backend.config import Settings
from backend.main import create_app


def _make_anthropic_stream_mock(tokens: list[str]):
    """Mock Anthropic client that streams tokens.

    Each call to ``messages.stream(...)`` returns a fresh async context
    manager whose ``text_stream`` yields *tokens* one at a time.  The
    ``get_final_message`` coroutine returns a stub with usage data.
    """
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
    """Full FastAPI app with real DB, mocked external APIs."""
    settings = Settings(
        anthropic_api_key="test",
        openai_api_key="test",
        database_url=test_database,
        _env_file=None,
    )
    return create_app(database_url=test_database, settings=settings)


def _create_session(client: TestClient, question_id: int) -> int:
    """Create a session via the REST API and return its id."""
    resp = client.post("/api/sessions", json={
        "question_id": question_id,
        "timer_setting_sec": 2700,
        "tts_enabled": False,
    })
    assert resp.status_code == 201, f"Session create failed ({resp.status_code}): {resp.text}"
    return resp.json()["id"]


def _collect_until(ws, predicate, timeout=10.0):
    """Read WS messages until *predicate(msg)* is True or timeout."""
    messages: list[dict] = []
    start = time.monotonic()
    while time.monotonic() - start < timeout:
        try:
            data = json.loads(ws.receive_text())
        except Exception:
            break
        messages.append(data)
        if predicate(data):
            return messages
    raise TimeoutError(
        f"Predicate not met after {timeout}s.  Got types: {[m.get('type') for m in messages]}"
    )


def _is_waiting(msg: dict) -> bool:
    return msg.get("type") == "state" and msg.get("state") == "waiting_for_candidate"


class TestWSLoadSession:
    def test_connect_load_receive_opening(self, test_app):
        mock_anthropic = _make_anthropic_stream_mock(
            ["Welcome", " to", " the", " interview."]
        )
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            assert len(questions) > 0, "No seeded questions in test DB"
            session_id = _create_session(client, questions[0]["id"])

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                messages = _collect_until(ws, _is_waiting)
                types = [m["type"] for m in messages]
                assert "session_loaded" in types
                assert "interviewer_text" in types
                loaded = next(m for m in messages if m["type"] == "session_loaded")
                assert loaded["session_id"] == session_id


class TestWSTextTurn:
    def test_text_input_round_trip(self, test_app):
        mock_anthropic = _make_anthropic_stream_mock(
            ["Sure", ", let's", " discuss."]
        )
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            session_id = _create_session(client, questions[0]["id"])

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                _collect_until(ws, _is_waiting)
                # Reset mock so the next call gets a fresh stream
                test_app.state.anthropic_client = _make_anthropic_stream_mock(
                    ["Sure", ", let's", " discuss."]
                )
                ws.send_text(json.dumps({
                    "type": "text_input",
                    "text": "I'd start by gathering requirements.",
                }))
                turn_messages = _collect_until(ws, _is_waiting)
                assert any(m["type"] == "interviewer_text" for m in turn_messages)


class TestWSEndSession:
    def test_end_session_finalizes_in_db(self, test_app):
        mock_anthropic = _make_anthropic_stream_mock(["Welcome."])
        with TestClient(test_app) as client:
            test_app.state.anthropic_client = mock_anthropic
            questions = client.get("/api/questions/").json()
            session_id = _create_session(client, questions[0]["id"])

            with client.websocket_connect(f"/ws/interview/{session_id}") as ws:
                _collect_until(ws, _is_waiting)
                ws.send_text(json.dumps({"type": "end_session"}))
                _collect_until(
                    ws, lambda m: m.get("type") == "session_ended"
                )

            resp = client.get(f"/api/sessions/{session_id}")
            assert resp.status_code == 200
            assert resp.json()["status"] == "completed"
