from __future__ import annotations

import logging

from fastapi import APIRouter, Depends

from backend.database import get_session_cost_breakdown, Conn
from backend.deps import get_db

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/sessions", tags=["costs"])

# Approximate cost per 1M tokens (as of 2025)
COST_PER_M_TOKENS: dict[str, dict[str, float]] = {
    "claude-sonnet-4-20250514": {"input": 3.0, "output": 15.0},
    "claude-opus-4-20250514": {"input": 15.0, "output": 75.0},
}
DEFAULT_COST: dict[str, float] = {"input": 3.0, "output": 15.0}


def _estimate_cost(model: str | None, input_tokens: int, output_tokens: int) -> float:
    rates = COST_PER_M_TOKENS.get(model or "", DEFAULT_COST)
    return (input_tokens * rates["input"] + output_tokens * rates["output"]) / 1_000_000


@router.get("/{session_id}/costs")
async def get_session_costs(
    session_id: int, conn: Conn = Depends(get_db)
) -> dict[str, object]:
    breakdown = await get_session_cost_breakdown(conn, session_id)

    total_input = 0
    total_output = 0
    total_cost = 0.0
    items: list[dict[str, object]] = []

    for row in breakdown:
        raw_input = row.get("input_tokens")
        raw_output = row.get("output_tokens")
        input_t = int(str(raw_input)) if raw_input is not None else 0
        output_t = int(str(raw_output)) if raw_output is not None else 0
        model = str(row.get("model") or "")
        cost = _estimate_cost(model or None, input_t, output_t)
        total_input += input_t
        total_output += output_t
        total_cost += cost
        items.append({
            "role": row.get("role"),
            "model": model,
            "input_tokens": input_t,
            "output_tokens": output_t,
            "estimated_cost_usd": round(cost, 4),
        })

    return {
        "session_id": session_id,
        "breakdown": items,
        "total_input_tokens": total_input,
        "total_output_tokens": total_output,
        "total_estimated_cost_usd": round(total_cost, 4),
    }
