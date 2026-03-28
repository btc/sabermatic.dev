from fastapi import APIRouter, Depends
import asyncpg

from backend.deps import get_db

router = APIRouter(tags=["health"])


@router.get("/health")
async def health_check(conn: asyncpg.Connection = Depends(get_db)):
    result = await conn.fetchval("SELECT 1")
    return {"status": "ok", "db": result == 1}
