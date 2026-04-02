from __future__ import annotations

from typing import Any

from fastapi import APIRouter, Depends, HTTPException, Query

from backend.database import (
    Conn,
    insert_session,
    list_sessions,
    get_session,
    get_session_messages,
    get_latest_evaluation,
    get_message_annotations,
    get_dimension_averages,
    get_question_stats,
    archive_session,
    archive_sessions_bulk,
)
from backend.deps import get_db
from backend.models import Session, SessionCreate, Message

router = APIRouter(prefix="/sessions", tags=["sessions"])


@router.post("", response_model=Session, status_code=201)
async def create_session(
    body: SessionCreate,
    conn: Conn = Depends(get_db),
) -> Session:
    return await insert_session(conn, body)


@router.get("/", response_model=list[Session])
async def list_all_sessions(
    include_archived: bool = Query(default=False),
    conn: Conn = Depends(get_db),
) -> list[Session]:
    return await list_sessions(conn, include_archived=include_archived)


@router.get("/stats")
async def session_stats(
    conn: Conn = Depends(get_db),
) -> dict[str, Any]:
    averages = await get_dimension_averages(conn)
    question_stats = await get_question_stats(conn)
    return {"averages": averages, "question_stats": question_stats}


@router.post("/archive-bulk")
async def archive_bulk(
    body: dict[str, Any],
    conn: Conn = Depends(get_db),
) -> dict[str, Any]:
    await archive_sessions_bulk(conn, body["session_ids"], archived=True)
    return {"status": "archived", "count": len(body["session_ids"])}


@router.post("/unarchive-bulk")
async def unarchive_bulk(
    body: dict[str, Any],
    conn: Conn = Depends(get_db),
) -> dict[str, Any]:
    await archive_sessions_bulk(conn, body["session_ids"], archived=False)
    return {"status": "unarchived", "count": len(body["session_ids"])}


@router.get("/{session_id}", response_model=Session)
async def get_one_session(
    session_id: int,
    conn: Conn = Depends(get_db),
) -> Session:
    s = await get_session(conn, session_id)
    if s is None:
        raise HTTPException(status_code=404, detail="Session not found")
    return s


@router.get("/{session_id}/messages", response_model=list[Message])
async def get_messages(
    session_id: int,
    conn: Conn = Depends(get_db),
) -> list[Message]:
    s = await get_session(conn, session_id)
    if s is None:
        raise HTTPException(status_code=404, detail="Session not found")
    return await get_session_messages(conn, session_id)


@router.get("/{session_id}/evaluation")
async def get_evaluation(
    session_id: int,
    conn: Conn = Depends(get_db),
) -> dict[str, Any]:
    s = await get_session(conn, session_id)
    if s is None:
        raise HTTPException(status_code=404, detail="Session not found")
    ev = await get_latest_evaluation(conn, session_id)
    if ev is None:
        raise HTTPException(status_code=404, detail="No evaluation found")
    annotations = await get_message_annotations(conn, ev.id)
    return {"evaluation": ev, "annotations": annotations}


@router.patch("/{session_id}/archive")
async def archive_session_endpoint(
    session_id: int,
    conn: Conn = Depends(get_db),
) -> dict[str, str]:
    await archive_session(conn, session_id, archived=True)
    return {"status": "archived"}


@router.patch("/{session_id}/unarchive")
async def unarchive_session_endpoint(
    session_id: int,
    conn: Conn = Depends(get_db),
) -> dict[str, str]:
    await archive_session(conn, session_id, archived=False)
    return {"status": "unarchived"}
