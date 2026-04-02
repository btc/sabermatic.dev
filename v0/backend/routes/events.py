from __future__ import annotations

from fastapi import APIRouter, Depends

from backend.database import get_session_events, Conn
from backend.deps import get_db
from backend.models import SessionEvent

router = APIRouter(prefix="/sessions", tags=["events"])


@router.get("/{session_id}/events", response_model=list[SessionEvent])
async def list_session_events(
    session_id: int, conn: Conn = Depends(get_db)
) -> list[SessionEvent]:
    return await get_session_events(conn, session_id)
