import logging

from fastapi import APIRouter, BackgroundTasks, Depends, HTTPException, Request
import asyncpg

from backend.deps import get_db, get_settings
from backend.database import (
    get_session,
    get_session_messages,
    get_question,
    get_latest_evaluation,
    insert_evaluation,
    insert_message_annotation,
    update_session_status,
)
from backend.evaluator import Evaluator, EvaluatorConfig
from backend.models import AnnotationType, MessageAnnotationCreate

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/evaluate", tags=["evaluation"])


async def _run_evaluation(
    pool: asyncpg.Pool,
    anthropic_client,
    session_id: int,
    evaluator_model: str,
):
    """Background task: evaluate a session. Catches ALL exceptions."""
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

            # Save evaluation
            evaluation = await insert_evaluation(conn, eval_create)

            # Map message sequence -> message id for annotations
            msg_map = {m.sequence: m.id for m in messages}

            # Save annotations
            for ann in annotations_raw:
                msg_seq = ann.get("message_sequence")
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

            # Update session status to reviewed
            await update_session_status(conn, session_id, "reviewed")

    except Exception:
        logger.exception("Evaluation failed for session %d", session_id)
        try:
            async with pool.acquire() as conn:
                await update_session_status(
                    conn,
                    session_id,
                    "evaluation_failed",
                    status_detail="Evaluation error",
                )
        except Exception:
            logger.exception(
                "Failed to update session status after evaluation failure"
            )


@router.post("/{session_id}")
async def trigger_evaluation(
    session_id: int,
    request: Request,
    background_tasks: BackgroundTasks,
    conn: asyncpg.Connection = Depends(get_db),
    settings=Depends(get_settings),
):
    session = await get_session(conn, session_id)
    if session is None:
        raise HTTPException(status_code=404, detail="Session not found")

    # Check if already evaluated
    existing = await get_latest_evaluation(conn, session_id)
    if existing is not None:
        return {"status": "already_evaluated", "evaluation_id": existing.id}

    # Mark as evaluating
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
