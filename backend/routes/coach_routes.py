import logging

from fastapi import APIRouter, BackgroundTasks, Depends, HTTPException, Request
import asyncpg

from backend.deps import get_db, get_settings
from backend.database import (
    get_latest_coach_review,
    get_latest_evaluation_time,
    list_sessions,
    list_questions,
    get_evaluations_for_sessions,
    insert_coach_review,
    insert_question,
)
from backend.coach import Coach, CoachConfig
from backend.models import CoachReview, SessionStatus

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/coach", tags=["coach"])


@router.get("/latest", response_model=CoachReview)
async def get_latest_review(conn: asyncpg.Connection = Depends(get_db)):
    review = await get_latest_coach_review(conn)
    if review is None:
        raise HTTPException(status_code=404, detail="No coach reviews yet")
    return review


async def _run_coach_analysis(
    pool: asyncpg.Pool,
    anthropic_client,
    coach_model: str,
):
    """Background task: run coach analysis. Catches ALL exceptions."""
    async with pool.acquire() as conn:
        locked = await conn.fetchval("SELECT pg_try_advisory_lock(1)")
        if not locked:
            logger.info("Coach: analysis already running, skipping")
            return

    try:
        async with pool.acquire() as conn:
            sessions = await list_sessions(conn)
            reviewed_sessions = [
                s for s in sessions if s.status == SessionStatus.reviewed
            ]
            session_ids = [s.id for s in reviewed_sessions]

            if not session_ids:
                logger.info("Coach: no reviewed sessions to analyze")
                return

            evaluations = await get_evaluations_for_sessions(conn, session_ids)
            questions = await list_questions(conn)

            coach = Coach(CoachConfig(model=coach_model))
            review_create, question_create = await coach.analyze(
                client=anthropic_client,
                sessions=reviewed_sessions,
                evaluations=evaluations,
                questions=questions,
                session_ids=session_ids,
            )

            # If coach generated a question, insert it first to get the ID
            if question_create is not None:
                new_q = await insert_question(conn, question_create)
                review_create.suggested_question_id = new_q.id

            await insert_coach_review(conn, review_create)

    except Exception:
        logger.exception("Coach analysis failed")
    finally:
        try:
            async with pool.acquire() as conn:
                await conn.execute("SELECT pg_advisory_unlock(1)")
        except Exception:
            pass


@router.post("/analyze")
async def trigger_coach_analysis(
    request: Request,
    background_tasks: BackgroundTasks,
    conn: asyncpg.Connection = Depends(get_db),
    settings=Depends(get_settings),
):
    # Check if analysis is up to date
    latest_review = await get_latest_coach_review(conn)
    latest_eval_time = await get_latest_evaluation_time(conn)

    if (
        latest_review is not None
        and latest_eval_time is not None
        and latest_review.created_at >= latest_eval_time
    ):
        return {"status": "up_to_date"}

    background_tasks.add_task(
        _run_coach_analysis,
        pool=request.app.state.pool,
        anthropic_client=request.app.state.anthropic_client,
        coach_model=settings.coach_model,
    )

    return {"status": "analyzing"}
