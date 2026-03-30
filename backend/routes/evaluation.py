from __future__ import annotations

import asyncio
import json
import logging
from datetime import datetime, timezone
from typing import Any

import anthropic
import asyncpg
from fastapi import APIRouter, BackgroundTasks, Depends, HTTPException, Request

from backend.config import Settings
from backend.database import (
    Conn,
    get_session,
    get_session_messages,
    get_question,
    get_latest_evaluation,
    insert_evaluation,
    insert_message_annotation,
    insert_session_event,
    update_session_status,
)
from backend.deps import get_db, get_settings
from backend.educator import Educator, EducatorConfig
from backend.evaluator import Evaluator, EvaluatorConfig
from backend.models import AnnotationType, MessageAnnotationCreate, SessionEventCreate

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/evaluate", tags=["evaluation"])


async def _run_evaluation(
    pool: asyncpg.Pool[asyncpg.Record],
    anthropic_client: anthropic.AsyncAnthropic,
    session_id: int,
    evaluator_model: str,
) -> None:
    """Background task: evaluate a session with one automatic retry."""
    for attempt in range(2):
        try:
            async with pool.acquire() as conn:
                session = await get_session(conn, session_id)
                if session is None:
                    logger.error("Evaluation: session %d not found", session_id)
                    return

                question = await get_question(conn, session.question_id)
                if question is None:
                    raise ValueError(f"Question {session.question_id} not found")

                messages = await get_session_messages(conn, session_id)
                if not messages:
                    raise ValueError(f"No messages for session {session_id}")

                evaluator = Evaluator(EvaluatorConfig(model=evaluator_model))
                eval_create, annotations_raw = await evaluator.evaluate(
                    client=anthropic_client,
                    question_title=question.title,
                    question_prompt=question.prompt,
                    messages=messages,
                    session_id=session_id,
                )

                # Atomic: evaluation + annotations + status update
                async with conn.transaction():
                    evaluation = await insert_evaluation(conn, eval_create)

                    # Map message sequence -> message id for annotations
                    msg_map = {m.sequence: m.id for m in messages}

                    for ann in annotations_raw:
                        msg_seq: int | None = ann.get("message_sequence")
                        if msg_seq is None:
                            continue
                        msg_id = msg_map.get(msg_seq)
                        if msg_id is None:
                            continue
                        ann_type_str = ann.get("type", "note")
                        try:
                            ann_type = AnnotationType(ann_type_str)
                        except ValueError:
                            ann_type = AnnotationType.note
                        await insert_message_annotation(
                            conn,
                            MessageAnnotationCreate(
                                evaluation_id=evaluation.id,
                                message_id=msg_id,
                                annotation_type=ann_type,
                                content=ann.get("content", ""),
                            ),
                        )

                    await update_session_status(conn, session_id, "reviewed")
                    await insert_session_event(conn, SessionEventCreate(
                        session_id=session_id, event="evaluation_completed",
                    ))
                return  # success

        except Exception:
            logger.exception(
                "Evaluation attempt %d failed for session %d",
                attempt + 1,
                session_id,
            )
            if attempt == 0:
                await asyncio.sleep(2)
                continue
            # Final failure after both attempts
            try:
                async with pool.acquire() as conn:
                    await update_session_status(
                        conn,
                        session_id,
                        "evaluation_failed",
                        status_detail="Evaluation failed after 2 attempts",
                    )
                    await insert_session_event(conn, SessionEventCreate(
                        session_id=session_id,
                        event="evaluation_failed",
                        detail=str(e),
                    ))
            except Exception:
                logger.exception(
                    "Failed to update session status after evaluation failure"
                )


@router.post("/{session_id}")
async def trigger_evaluation(
    session_id: int,
    request: Request,
    background_tasks: BackgroundTasks,
    conn: Conn = Depends(get_db),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    session = await get_session(conn, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session not found")

    # Allow retry when evaluation previously failed
    if session.status == "evaluation_failed":
        await update_session_status(
            conn, session_id, "evaluating", status_detail=""
        )
    elif session.status == "evaluating":
        # Check if stuck (evaluating for >5 minutes since session ended)
        if session.ended_at:
            age = (datetime.now(timezone.utc) - session.ended_at).total_seconds()
            if age > 300:
                await update_session_status(
                    conn, session_id, "evaluating", status_detail=""
                )
            else:
                return {"status": "already_evaluating", "session_id": session_id}
        else:
            return {"status": "already_evaluating", "session_id": session_id}
    else:
        # Normal path: check for existing evaluation
        existing = await get_latest_evaluation(conn, session_id)
        if existing is not None:
            return {"status": "already_evaluated", "evaluation_id": existing.id}
        await update_session_status(conn, session_id, "evaluating")

    # Use pool from app state for background task (DI connection closes after request)
    background_tasks.add_task(
        _run_evaluation,
        pool=request.app.state.pool,
        anthropic_client=request.app.state.anthropic_client,
        session_id=session_id,
        evaluator_model=settings.evaluator_model,
    )

    return {"status": "evaluating", "session_id": session_id}


async def _run_educator(
    pool: asyncpg.Pool[asyncpg.Record],
    anthropic_client: anthropic.AsyncAnthropic,
    session_id: int,
    educator_model: str,
) -> None:
    """Background task: run educator analysis. Catches ALL exceptions."""
    evaluation = None
    try:
        async with pool.acquire() as conn:
            session = await get_session(conn, session_id)
            if session is None:
                logger.error("Educator: session %d not found", session_id)
                return

            question = await get_question(conn, session.question_id)
            if question is None:
                raise ValueError(f"Question {session.question_id} not found")

            messages = await get_session_messages(conn, session_id)
            if not messages:
                raise ValueError(f"No messages for session {session_id}")

            evaluation = await get_latest_evaluation(conn, session_id)
            if not evaluation:
                logger.error("Educator: no evaluation for session %d", session_id)
                return

            # Build evaluation summary from scores + gaps
            eval_summary = (
                f"Scores: Requirements={evaluation.score_requirements} "
                f"HighLevel={evaluation.score_highlevel} "
                f"DeepDive={evaluation.score_deepdive} "
                f"Scalability={evaluation.score_scalability} "
                f"Communication={evaluation.score_communication} "
                f"Overall={evaluation.score_overall}"
            )
            eval_summary += f"\nGaps: {', '.join(evaluation.gaps)}"
            eval_summary += f"\nStrengths: {', '.join(evaluation.strengths)}"
            eval_summary += f"\nAdvice: {evaluation.advice}"

            educator = Educator(EducatorConfig(model=educator_model))
            transcript = educator.build_transcript_text(
                question.title, question.prompt, messages
            )

            model_answer, gap_deepdives, raw_response = await educator.educate(
                client=anthropic_client,
                question_title=question.title,
                question_prompt=question.prompt,
                transcript_text=transcript,
                evaluation_summary=eval_summary,
            )

            # Save to evaluation
            await conn.execute(
                """
                UPDATE evaluations
                SET educator_model_answer = $1,
                    educator_gap_deepdives = $2,
                    educator_raw_response = $3
                WHERE id = $4
                """,
                model_answer,
                gap_deepdives,
                json.dumps(raw_response),
                evaluation.id,
            )

    except Exception:
        logger.exception("Educator failed for session %d", session_id)
        # Record failure sentinel so frontend can show error + retry
        if evaluation is not None:
            try:
                async with pool.acquire() as conn:
                    await conn.execute(
                        """
                        UPDATE evaluations
                        SET educator_raw_response = $1
                        WHERE id = $2
                        """,
                        json.dumps({"error": True, "message": "Educator analysis failed"}),
                        evaluation.id,
                    )
            except Exception:
                logger.exception("Failed to record educator failure")


@router.post("/{session_id}/educate")
async def trigger_educator(
    session_id: int,
    request: Request,
    background_tasks: BackgroundTasks,
    conn: Conn = Depends(get_db),
    settings: Settings = Depends(get_settings),
) -> dict[str, Any]:
    """Trigger educator analysis in background."""
    session = await get_session(conn, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session not found")

    # Verify evaluation exists
    evaluation = await get_latest_evaluation(conn, session_id)
    if evaluation is None:
        raise HTTPException(
            status_code=404, detail="No evaluation found for this session"
        )

    # Already has content — no re-generation
    if evaluation.educator_model_answer is not None:
        return {
            "status": "already_generated",
            "evaluation_id": evaluation.id,
        }

    # Clear failure sentinel if retrying
    if (
        evaluation.educator_raw_response
        and isinstance(evaluation.educator_raw_response, dict)
        and evaluation.educator_raw_response.get("error")
    ):
        async with request.app.state.pool.acquire() as write_conn:
            await write_conn.execute(
                "UPDATE evaluations SET educator_raw_response = NULL WHERE id = $1",
                evaluation.id,
            )

    # Use pool from app state for background task (DI connection closes after request)
    background_tasks.add_task(
        _run_educator,
        pool=request.app.state.pool,
        anthropic_client=request.app.state.anthropic_client,
        session_id=session_id,
        educator_model=settings.educator_model,
    )

    return {"status": "educating", "session_id": session_id}


@router.get("/{session_id}/educator")
async def get_educator_content(
    session_id: int,
    conn: Conn = Depends(get_db),
) -> dict[str, Any]:
    """Get educator content for a session."""
    evaluation = await get_latest_evaluation(conn, session_id)
    if evaluation is None:
        raise HTTPException(
            status_code=404, detail="No evaluation found for this session"
        )

    if evaluation.educator_model_answer is None:
        # Check if there's a recorded failure
        if (
            evaluation.educator_raw_response
            and isinstance(evaluation.educator_raw_response, dict)
            and evaluation.educator_raw_response.get("error")
        ):
            return {
                "status": "failed",
                "message": evaluation.educator_raw_response.get(
                    "message", "Unknown error"
                ),
            }
        raise HTTPException(
            status_code=404, detail="Educator content not yet generated"
        )

    return {
        "status": "ok",
        "evaluation_id": evaluation.id,
        "model_answer": evaluation.educator_model_answer,
        "gap_deepdives": evaluation.educator_gap_deepdives,
    }
