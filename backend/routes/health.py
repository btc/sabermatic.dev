from __future__ import annotations

from typing import Any

from fastapi import APIRouter, Depends

from backend.database import Conn
from backend.deps import get_db

router = APIRouter(tags=["health"])


@router.get("/health")
async def health_check(
    conn: Conn = Depends(get_db),
) -> dict[str, Any]:
    result = await conn.fetchval("SELECT 1")
    return {"status": "ok", "db": result == 1}
