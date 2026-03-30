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


# --- Helpers ---


async def _create_question(client: AsyncClient) -> int:
    resp = await client.post(
        "/api/questions/",
        json={
            "title": "Archive Test Q",
            "prompt": "Design something.",
            "difficulty": "medium",
            "tags": [],
            "source": "custom",
        },
    )
    assert resp.status_code == 201
    return resp.json()["id"]


async def _create_session(client: AsyncClient, question_id: int) -> int:
    resp = await client.post(
        "/api/sessions",
        json={"question_id": question_id, "status": "completed"},
    )
    assert resp.status_code == 201
    return resp.json()["id"]


# --- Archive / Unarchive ---


@pytest.mark.asyncio
async def test_archive_session(client: AsyncClient):
    qid = await _create_question(client)
    sid = await _create_session(client, qid)

    resp = await client.patch(f"/api/sessions/{sid}/archive")
    assert resp.status_code == 200
    assert resp.json() == {"status": "archived"}

    # Archived session should not appear in default listing
    list_resp = await client.get("/api/sessions/")
    session_ids = [s["id"] for s in list_resp.json()]
    assert sid not in session_ids

    # But it should appear when include_archived=true
    list_all_resp = await client.get("/api/sessions/?include_archived=true")
    session_ids_all = [s["id"] for s in list_all_resp.json()]
    assert sid in session_ids_all


@pytest.mark.asyncio
async def test_unarchive_session(client: AsyncClient):
    qid = await _create_question(client)
    sid = await _create_session(client, qid)

    # Archive first
    await client.patch(f"/api/sessions/{sid}/archive")

    # Confirm it's hidden
    list_resp = await client.get("/api/sessions/")
    assert sid not in [s["id"] for s in list_resp.json()]

    # Unarchive
    resp = await client.patch(f"/api/sessions/{sid}/unarchive")
    assert resp.status_code == 200
    assert resp.json() == {"status": "unarchived"}

    # Now it should appear again
    list_resp2 = await client.get("/api/sessions/")
    assert sid in [s["id"] for s in list_resp2.json()]


@pytest.mark.asyncio
async def test_bulk_archive(client: AsyncClient):
    qid = await _create_question(client)
    sid1 = await _create_session(client, qid)
    sid2 = await _create_session(client, qid)
    sid3 = await _create_session(client, qid)

    resp = await client.post(
        "/api/sessions/archive-bulk",
        json={"session_ids": [sid1, sid2]},
    )
    assert resp.status_code == 200
    data = resp.json()
    assert data["status"] == "archived"
    assert data["count"] == 2

    # sid1 and sid2 hidden, sid3 visible
    list_resp = await client.get("/api/sessions/")
    visible_ids = [s["id"] for s in list_resp.json()]
    assert sid1 not in visible_ids
    assert sid2 not in visible_ids
    assert sid3 in visible_ids


@pytest.mark.asyncio
async def test_list_sessions_include_archived_default_false(client: AsyncClient):
    qid = await _create_question(client)
    sid = await _create_session(client, qid)

    await client.patch(f"/api/sessions/{sid}/archive")

    resp = await client.get("/api/sessions/")
    assert resp.status_code == 200
    visible = [s["id"] for s in resp.json()]
    assert sid not in visible


@pytest.mark.asyncio
async def test_list_sessions_include_archived_true(client: AsyncClient):
    qid = await _create_question(client)
    sid = await _create_session(client, qid)

    await client.patch(f"/api/sessions/{sid}/archive")

    resp = await client.get("/api/sessions/?include_archived=true")
    assert resp.status_code == 200
    all_ids = [s["id"] for s in resp.json()]
    assert sid in all_ids


# --- Coach force param ---


@pytest.mark.asyncio
async def test_coach_analyze_force_triggers_analysis(client: AsyncClient):
    """With force=true the endpoint should always return 'analyzing', bypassing debounce."""
    resp = await client.post("/api/coach/analyze?force=true")
    assert resp.status_code == 200
    assert resp.json()["status"] == "analyzing"


@pytest.mark.asyncio
async def test_coach_analyze_no_force_up_to_date(client: AsyncClient):
    """Without force, when there are no evaluations, it should trigger analysis (no up_to_date)."""
    resp = await client.post("/api/coach/analyze")
    assert resp.status_code == 200
    # No evaluations exist yet, so it will trigger analysis
    assert resp.json()["status"] == "analyzing"
