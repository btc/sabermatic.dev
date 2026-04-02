import logging

from fastapi import APIRouter, Request

logger = logging.getLogger(__name__)

router = APIRouter(tags=["health"])


@router.get("/health")
async def health_check(request: Request) -> dict[str, object]:
    checks: dict[str, bool] = {}

    # DB check
    try:
        async with request.app.state.pool.acquire() as conn:
            result = await conn.fetchval("SELECT 1")
            checks["db"] = result == 1
    except Exception as e:
        logger.warning("Health check: DB failed: %s", e)
        checks["db"] = False

    # Anthropic check — verify client is configured
    try:
        client = request.app.state.anthropic_client
        checks["anthropic"] = client is not None
    except Exception as e:
        logger.warning("Health check: Anthropic failed: %s", e)
        checks["anthropic"] = False

    # OpenAI check — verify client is configured
    try:
        client = request.app.state.openai_client
        checks["openai"] = client is not None
    except Exception as e:
        logger.warning("Health check: OpenAI failed: %s", e)
        checks["openai"] = False

    all_ok = all(checks.values())
    return {
        "status": "ok" if all_ok else "degraded",
        **checks,
    }
