"""Tests for REST API routes.

Uses httpx AsyncClient with ASGITransport for testing FastAPI endpoints.
The asgi-lifespan LifespanManager ensures the FastAPI lifespan (pool creation,
client setup) runs before tests and cleans up after.
"""

import pytest
import pytest_asyncio
from asgi_lifespan import LifespanManager
from httpx import AsyncClient, ASGITransport

from backend.config import Settings
from backend.main import create_app


@pytest_asyncio.fixture
async def app(test_database):
    settings = Settings(
        anthropic_api_key="test",
        openai_api_key="test",
        database_url=test_database,
        _env_file=None,
    )
    application = create_app(database_url=test_database, settings=settings)
    async with LifespanManager(application) as manager:
        yield manager.app


@pytest_asyncio.fixture
async def client(app):
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as c:
        yield c


@pytest.mark.asyncio
async def test_health(client: AsyncClient):
    resp = await client.get("/api/health")
    assert resp.status_code == 200
    data = resp.json()
    assert data["status"] == "ok"
    assert data["db"] is True


@pytest.mark.asyncio
async def test_list_questions(client: AsyncClient):
    resp = await client.get("/api/questions/")
    assert resp.status_code == 200
    data = resp.json()
    assert isinstance(data, list)


@pytest.mark.asyncio
async def test_list_sessions(client: AsyncClient):
    resp = await client.get("/api/sessions/")
    assert resp.status_code == 200
    data = resp.json()
    assert isinstance(data, list)


@pytest.mark.asyncio
async def test_coach_latest_404(client: AsyncClient):
    resp = await client.get("/api/coach/latest")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_sessions_stats(client: AsyncClient):
    resp = await client.get("/api/sessions/stats")
    assert resp.status_code == 200
    data = resp.json()
    assert "averages" in data
    assert "question_stats" in data


@pytest.mark.asyncio
async def test_create_question(client: AsyncClient):
    body = {
        "title": "Test Question",
        "prompt": "Design a test system.",
        "difficulty": "medium",
        "tags": ["test"],
        "source": "custom",
    }
    resp = await client.post("/api/questions/", json=body)
    assert resp.status_code == 201
    data = resp.json()
    assert data["title"] == "Test Question"
    assert data["id"] is not None


@pytest.mark.asyncio
async def test_get_question_not_found(client: AsyncClient):
    resp = await client.get("/api/questions/99999")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_get_session_not_found(client: AsyncClient):
    resp = await client.get("/api/sessions/99999")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_get_session_messages_not_found(client: AsyncClient):
    resp = await client.get("/api/sessions/99999/messages")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_get_session_evaluation_not_found(client: AsyncClient):
    resp = await client.get("/api/sessions/99999/evaluation")
    assert resp.status_code == 404


@pytest.mark.asyncio
async def test_create_and_get_question(client: AsyncClient):
    body = {
        "title": "Round Trip Test",
        "prompt": "Design a round trip system.",
        "difficulty": "hard",
        "tags": ["roundtrip", "test"],
        "source": "custom",
    }
    create_resp = await client.post("/api/questions/", json=body)
    assert create_resp.status_code == 201
    created = create_resp.json()

    get_resp = await client.get(f"/api/questions/{created['id']}")
    assert get_resp.status_code == 200
    fetched = get_resp.json()
    assert fetched["title"] == "Round Trip Test"
    assert fetched["difficulty"] == "hard"
    assert set(fetched["tags"]) == {"roundtrip", "test"}
