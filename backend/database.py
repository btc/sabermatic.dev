"""Database query functions for drill application.

All functions take an asyncpg.Connection as first argument — no global pool.
Row-to-model conversion uses explicit mapping. JSON fields use json.dumps
for inserts and json.loads for reads.
"""

import json
from datetime import datetime
from typing import Optional

import asyncpg

from backend.models import (
    CoachReview,
    CoachReviewCreate,
    Evaluation,
    EvaluationCreate,
    Message,
    MessageAnnotation,
    MessageAnnotationCreate,
    MessageCreate,
    Question,
    QuestionCreate,
    Session,
    SessionCreate,
)


# --- Row-to-model helpers ---


def _row_to_question(row: asyncpg.Record) -> Question:
    return Question(
        id=row["id"],
        title=row["title"],
        prompt=row["prompt"],
        difficulty=row["difficulty"],
        tags=list(row["tags"]),
        hints=json.loads(row["hints"]) if row["hints"] is not None else None,
        source=row["source"],
        source_detail=row["source_detail"],
        created_at=row["created_at"],
    )


def _row_to_session(row: asyncpg.Record) -> Session:
    return Session(
        id=row["id"],
        question_id=row["question_id"],
        status=row["status"],
        status_detail=row["status_detail"],
        timer_setting_sec=row["timer_setting_sec"],
        interviewer_briefed=row["interviewer_briefed"],
        archived=row["archived"],
        tts_enabled=row["tts_enabled"],
        started_at=row["started_at"],
        ended_at=row["ended_at"],
        duration_seconds=row["duration_seconds"],
        turn_count=row["turn_count"],
        audio_dir=row["audio_dir"],
    )


def _row_to_message(row: asyncpg.Record) -> Message:
    return Message(
        id=row["id"],
        session_id=row["session_id"],
        sequence=row["sequence"],
        role=row["role"],
        content=row["content"],
        raw_content=row["raw_content"],
        raw_response=json.loads(row["raw_response"]) if row["raw_response"] else None,
        timestamp=row["timestamp"],
        audio_path=row["audio_path"],
        audio_duration_sec=row["audio_duration_sec"],
    )


def _row_to_evaluation(row: asyncpg.Record) -> Evaluation:
    return Evaluation(
        id=row["id"],
        session_id=row["session_id"],
        score_requirements=row["score_requirements"],
        score_highlevel=row["score_highlevel"],
        score_deepdive=row["score_deepdive"],
        score_scalability=row["score_scalability"],
        score_communication=row["score_communication"],
        score_overall=row["score_overall"],
        strengths=json.loads(row["strengths"]),
        gaps=json.loads(row["gaps"]),
        advice=row["advice"],
        raw_response=json.loads(row["raw_response"]),
        educator_model_answer=row["educator_model_answer"],
        educator_gap_deepdives=row["educator_gap_deepdives"],
        educator_raw_response=json.loads(row["educator_raw_response"]) if row["educator_raw_response"] else None,
        evaluated_at=row["evaluated_at"],
    )


def _row_to_annotation(row: asyncpg.Record) -> MessageAnnotation:
    return MessageAnnotation(
        id=row["id"],
        evaluation_id=row["evaluation_id"],
        message_id=row["message_id"],
        annotation_type=row["annotation_type"],
        content=row["content"],
    )


def _row_to_coach_review(row: asyncpg.Record) -> CoachReview:
    return CoachReview(
        id=row["id"],
        recommendation=row["recommendation"],
        gap_analysis=json.loads(row["gap_analysis"]),
        suggested_question_id=row["suggested_question_id"],
        sessions_analyzed=list(row["sessions_analyzed"]),
        raw_response=json.loads(row["raw_response"]),
        created_at=row["created_at"],
    )


# --- Questions ---


async def insert_question(
    conn: asyncpg.Connection, q: QuestionCreate
) -> Question:
    row = await conn.fetchrow(
        """
        INSERT INTO questions (title, prompt, difficulty, tags, hints, source, source_detail)
        VALUES ($1, $2, $3::difficulty, $4, $5::jsonb, $6::question_source, $7)
        RETURNING *
        """,
        q.title,
        q.prompt,
        q.difficulty.value,
        q.tags,
        json.dumps(q.hints) if q.hints is not None else None,
        q.source.value,
        q.source_detail,
    )
    return _row_to_question(row)


async def get_question(
    conn: asyncpg.Connection, question_id: int
) -> Optional[Question]:
    row = await conn.fetchrow("SELECT * FROM questions WHERE id = $1", question_id)
    if row is None:
        return None
    return _row_to_question(row)


async def list_questions(conn: asyncpg.Connection) -> list[Question]:
    rows = await conn.fetch("SELECT * FROM questions ORDER BY id")
    return [_row_to_question(r) for r in rows]


# --- Sessions ---


async def insert_session(
    conn: asyncpg.Connection, s: SessionCreate
) -> Session:
    row = await conn.fetchrow(
        """
        INSERT INTO sessions (
            question_id, status, timer_setting_sec, interviewer_briefed,
            tts_enabled,
            started_at, ended_at, duration_seconds, turn_count, audio_dir, status_detail
        )
        VALUES (
            $1, $2::session_status, $3, $4,
            $5,
            COALESCE($6, now()), $7, $8, $9, $10, $11
        )
        RETURNING *
        """,
        s.question_id,
        s.status.value,
        s.timer_setting_sec,
        s.interviewer_briefed,
        s.tts_enabled,
        s.started_at,
        s.ended_at,
        s.duration_seconds,
        s.turn_count,
        s.audio_dir,
        s.status_detail,
    )
    return _row_to_session(row)


async def get_session(
    conn: asyncpg.Connection, session_id: int
) -> Optional[Session]:
    row = await conn.fetchrow("SELECT * FROM sessions WHERE id = $1", session_id)
    if row is None:
        return None
    return _row_to_session(row)


async def list_sessions(
    conn: asyncpg.Connection, include_archived: bool = False
) -> list[Session]:
    if include_archived:
        rows = await conn.fetch("SELECT * FROM sessions ORDER BY started_at DESC")
    else:
        rows = await conn.fetch(
            "SELECT * FROM sessions WHERE archived = false ORDER BY started_at DESC"
        )
    return [_row_to_session(r) for r in rows]


async def update_session_status(
    conn: asyncpg.Connection,
    session_id: int,
    status: str,
    *,
    ended_at: Optional[datetime] = None,
    duration_seconds: Optional[int] = None,
    turn_count: Optional[int] = None,
    audio_dir: Optional[str] = None,
    status_detail: Optional[str] = None,
) -> Optional[Session]:
    row = await conn.fetchrow(
        """
        UPDATE sessions
        SET status = $2::session_status,
            ended_at = COALESCE($3, ended_at),
            duration_seconds = COALESCE($4, duration_seconds),
            turn_count = COALESCE($5, turn_count),
            audio_dir = COALESCE($6, audio_dir),
            status_detail = COALESCE($7, status_detail)
        WHERE id = $1
        RETURNING *
        """,
        session_id,
        status,
        ended_at,
        duration_seconds,
        turn_count,
        audio_dir,
        status_detail,
    )
    if row is None:
        return None
    return _row_to_session(row)


async def archive_session(
    conn: asyncpg.Connection, session_id: int, archived: bool
) -> None:
    await conn.execute(
        "UPDATE sessions SET archived = $1 WHERE id = $2", archived, session_id
    )


async def archive_sessions_bulk(
    conn: asyncpg.Connection, session_ids: list[int], archived: bool
) -> None:
    await conn.execute(
        "UPDATE sessions SET archived = $1 WHERE id = ANY($2)", archived, session_ids
    )


async def get_session_token_usage(
    conn: asyncpg.Connection, session_id: int
) -> list[dict]:
    rows = await conn.fetch(
        "SELECT * FROM llm_token_usage WHERE session_id = $1", session_id
    )
    return [dict(r) for r in rows]


# --- Messages ---


async def insert_message(
    conn: asyncpg.Connection, m: MessageCreate
) -> Message:
    row = await conn.fetchrow(
        """
        INSERT INTO messages (
            session_id, sequence, role, content, raw_content,
            raw_response,
            timestamp, audio_path, audio_duration_sec
        )
        VALUES ($1, $2, $3::message_role, $4, $5, $6::jsonb, COALESCE($7, now()), $8, $9)
        RETURNING *
        """,
        m.session_id,
        m.sequence,
        m.role.value,
        m.content,
        m.raw_content,
        json.dumps(m.raw_response) if m.raw_response is not None else None,
        m.timestamp,
        m.audio_path,
        m.audio_duration_sec,
    )
    return _row_to_message(row)


async def get_session_messages(
    conn: asyncpg.Connection, session_id: int
) -> list[Message]:
    rows = await conn.fetch(
        "SELECT * FROM messages WHERE session_id = $1 ORDER BY sequence",
        session_id,
    )
    return [_row_to_message(r) for r in rows]


# --- Evaluations ---


async def insert_evaluation(
    conn: asyncpg.Connection, e: EvaluationCreate
) -> Evaluation:
    row = await conn.fetchrow(
        """
        INSERT INTO evaluations (
            session_id, score_requirements, score_highlevel, score_deepdive,
            score_scalability, score_communication, score_overall,
            strengths, gaps, advice, raw_response, evaluated_at
        )
        VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11::jsonb, COALESCE($12, now()))
        RETURNING *
        """,
        e.session_id,
        e.score_requirements,
        e.score_highlevel,
        e.score_deepdive,
        e.score_scalability,
        e.score_communication,
        e.score_overall,
        json.dumps(e.strengths),
        json.dumps(e.gaps),
        e.advice,
        json.dumps(e.raw_response),
        e.evaluated_at,
    )
    return _row_to_evaluation(row)


async def get_latest_evaluation(
    conn: asyncpg.Connection, session_id: int
) -> Optional[Evaluation]:
    row = await conn.fetchrow(
        """
        SELECT * FROM evaluations
        WHERE session_id = $1
        ORDER BY evaluated_at DESC, id DESC
        LIMIT 1
        """,
        session_id,
    )
    if row is None:
        return None
    return _row_to_evaluation(row)


async def get_evaluations_for_sessions(
    conn: asyncpg.Connection, session_ids: list[int]
) -> list[Evaluation]:
    rows = await conn.fetch(
        """
        SELECT * FROM evaluations
        WHERE session_id = ANY($1)
        ORDER BY evaluated_at DESC
        """,
        session_ids,
    )
    return [_row_to_evaluation(r) for r in rows]


# --- Annotations ---


async def insert_message_annotation(
    conn: asyncpg.Connection, a: MessageAnnotationCreate
) -> MessageAnnotation:
    row = await conn.fetchrow(
        """
        INSERT INTO message_annotations (evaluation_id, message_id, annotation_type, content)
        VALUES ($1, $2, $3::annotation_type, $4)
        RETURNING *
        """,
        a.evaluation_id,
        a.message_id,
        a.annotation_type.value,
        a.content,
    )
    return _row_to_annotation(row)


async def get_message_annotations(
    conn: asyncpg.Connection, evaluation_id: int
) -> list[MessageAnnotation]:
    rows = await conn.fetch(
        "SELECT * FROM message_annotations WHERE evaluation_id = $1 ORDER BY id",
        evaluation_id,
    )
    return [_row_to_annotation(r) for r in rows]


# --- Coach Reviews ---


async def insert_coach_review(
    conn: asyncpg.Connection, c: CoachReviewCreate
) -> CoachReview:
    row = await conn.fetchrow(
        """
        INSERT INTO coach_reviews (
            recommendation, gap_analysis, suggested_question_id,
            sessions_analyzed, raw_response, created_at
        )
        VALUES ($1, $2::jsonb, $3, $4, $5::jsonb, COALESCE($6, now()))
        RETURNING *
        """,
        c.recommendation,
        json.dumps(c.gap_analysis),
        c.suggested_question_id,
        c.sessions_analyzed,
        json.dumps(c.raw_response),
        c.created_at,
    )
    return _row_to_coach_review(row)


async def get_latest_coach_review(
    conn: asyncpg.Connection,
) -> Optional[CoachReview]:
    row = await conn.fetchrow(
        "SELECT * FROM coach_reviews ORDER BY created_at DESC LIMIT 1"
    )
    if row is None:
        return None
    return _row_to_coach_review(row)


# --- Aggregates ---


async def get_dimension_averages(
    conn: asyncpg.Connection,
) -> Optional[dict[str, float]]:
    """Average score across all evaluations for non-archived sessions."""
    row = await conn.fetchrow(
        """
        SELECT
            AVG(e.score_requirements)  AS avg_requirements,
            AVG(e.score_highlevel)     AS avg_highlevel,
            AVG(e.score_deepdive)      AS avg_deepdive,
            AVG(e.score_scalability)   AS avg_scalability,
            AVG(e.score_communication) AS avg_communication,
            AVG(e.score_overall)       AS avg_overall
        FROM evaluations e
        JOIN sessions s ON s.id = e.session_id
        WHERE s.archived = false
        """
    )
    if row is None or row["avg_requirements"] is None:
        return None
    return {
        "requirements": float(row["avg_requirements"]),
        "highlevel": float(row["avg_highlevel"]),
        "deepdive": float(row["avg_deepdive"]),
        "scalability": float(row["avg_scalability"]),
        "communication": float(row["avg_communication"]),
        "overall": float(row["avg_overall"]),
    }


async def get_question_stats(
    conn: asyncpg.Connection,
) -> list[dict]:
    """Per-question session count and average overall score (excluding archived)."""
    rows = await conn.fetch(
        """
        SELECT
            q.id AS question_id,
            q.title,
            COUNT(DISTINCT s.id) AS session_count,
            AVG(e.score_overall) AS avg_overall
        FROM questions q
        LEFT JOIN sessions s ON s.question_id = q.id AND s.archived = false
        LEFT JOIN evaluations e ON e.session_id = s.id
        GROUP BY q.id, q.title
        ORDER BY q.id
        """
    )
    return [
        {
            "question_id": r["question_id"],
            "title": r["title"],
            "session_count": r["session_count"],
            "avg_overall": float(r["avg_overall"]) if r["avg_overall"] is not None else None,
        }
        for r in rows
    ]


async def get_latest_evaluation_time(
    conn: asyncpg.Connection,
) -> Optional[datetime]:
    """Return the most recent evaluated_at timestamp, for coach debounce."""
    return await conn.fetchval(
        "SELECT MAX(evaluated_at) FROM evaluations"
    )
