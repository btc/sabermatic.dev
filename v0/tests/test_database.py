"""Tests for backend.database — CRUD operations against a real PostgreSQL database.

Uses the db_conn fixture from conftest.py which provides a connection inside
a transaction that gets rolled back after each test for isolation.
"""

import json

import asyncpg
import pytest

from backend.database import (
    get_dimension_averages,
    get_latest_coach_review,
    get_latest_evaluation,
    get_latest_evaluation_time,
    get_message_annotations,
    get_question,
    get_question_stats,
    get_session,
    get_session_messages,
    get_evaluations_for_sessions,
    insert_coach_review,
    insert_evaluation,
    insert_message,
    insert_message_annotation,
    insert_question,
    insert_session,
    list_questions,
    list_sessions,
    update_session_status,
)
from backend.models import (
    AnnotationType,
    CoachReviewCreate,
    Difficulty,
    EvaluationCreate,
    MessageCreate,
    MessageAnnotationCreate,
    MessageRole,
    QuestionCreate,
    QuestionSource,
    SessionCreate,
    SessionStatus,
)


# --- Helpers ---


async def _make_question(conn, **overrides) -> int:
    """Insert a question and return its id."""
    defaults = dict(
        title="Test Q",
        prompt="Design something.",
        difficulty=Difficulty.medium,
        tags=["test"],
        source=QuestionSource.seed,
    )
    defaults.update(overrides)
    q = await insert_question(conn, QuestionCreate(**defaults))
    return q.id


async def _make_session(conn, question_id=None, **overrides) -> int:
    """Insert a session and return its id."""
    if question_id is None:
        question_id = await _make_question(conn)
    s = await insert_session(conn, SessionCreate(question_id=question_id, **overrides))
    return s.id


async def _make_message(conn, session_id, sequence=0, **overrides) -> int:
    """Insert a message and return its id."""
    defaults = dict(
        session_id=session_id,
        sequence=sequence,
        role=MessageRole.interviewer,
        content="Hello",
    )
    defaults.update(overrides)
    m = await insert_message(conn, MessageCreate(**defaults))
    return m.id


async def _make_evaluation(conn, session_id, **overrides) -> int:
    """Insert an evaluation and return its id."""
    defaults = dict(
        session_id=session_id,
        score_requirements=3,
        score_highlevel=3,
        score_deepdive=3,
        score_scalability=3,
        score_communication=3,
        score_overall=3,
        strengths=["good"],
        gaps=["improve"],
        advice="Keep going.",
        raw_response={"model": "test"},
    )
    defaults.update(overrides)
    e = await insert_evaluation(conn, EvaluationCreate(**defaults))
    return e.id


# --- Question Tests ---


class TestQuestions:
    async def test_insert_and_get_question(self, db_conn):
        q = await insert_question(
            db_conn,
            QuestionCreate(
                title="URL Shortener",
                prompt="Design a URL shortener.",
                difficulty=Difficulty.medium,
                tags=["read-heavy", "hashing"],
                hints={"areas": ["collisions"]},
                source=QuestionSource.seed,
                source_detail="initial batch",
            ),
        )
        assert q.id is not None
        assert q.title == "URL Shortener"
        assert q.difficulty == Difficulty.medium
        assert q.tags == ["read-heavy", "hashing"]
        assert q.hints == {"areas": ["collisions"]}
        assert q.source == QuestionSource.seed
        assert q.source_detail == "initial batch"
        assert q.created_at is not None

        # Get by id
        fetched = await get_question(db_conn, q.id)
        assert fetched is not None
        assert fetched.id == q.id
        assert fetched.title == "URL Shortener"

    async def test_get_question_not_found(self, db_conn):
        assert await get_question(db_conn, 99999) is None

    async def test_list_questions(self, db_conn):
        await _make_question(db_conn, title="Q1")
        await _make_question(db_conn, title="Q2")
        questions = await list_questions(db_conn)
        assert len(questions) >= 2
        titles = [q.title for q in questions]
        assert "Q1" in titles
        assert "Q2" in titles

    async def test_question_null_hints(self, db_conn):
        q = await insert_question(
            db_conn,
            QuestionCreate(
                title="No Hints",
                prompt="Design something.",
                difficulty=Difficulty.hard,
            ),
        )
        assert q.hints is None
        fetched = await get_question(db_conn, q.id)
        assert fetched.hints is None


# --- Session Tests ---


class TestSessions:
    async def test_insert_and_get_session(self, db_conn):
        qid = await _make_question(db_conn)
        s = await insert_session(db_conn, SessionCreate(question_id=qid))
        assert s.id is not None
        assert s.question_id == qid
        assert s.status == SessionStatus.active
        assert s.timer_setting_sec == 2700
        assert s.interviewer_briefed is False
        assert s.started_at is not None

        fetched = await get_session(db_conn, s.id)
        assert fetched is not None
        assert fetched.id == s.id

    async def test_get_session_not_found(self, db_conn):
        assert await get_session(db_conn, 99999) is None

    async def test_list_sessions(self, db_conn):
        qid = await _make_question(db_conn)
        await insert_session(db_conn, SessionCreate(question_id=qid))
        await insert_session(db_conn, SessionCreate(question_id=qid))
        sessions = await list_sessions(db_conn)
        assert len(sessions) >= 2

    async def test_update_session_status(self, db_conn):
        sid = await _make_session(db_conn)
        updated = await update_session_status(
            db_conn,
            sid,
            "completed",
            duration_seconds=1200,
            turn_count=10,
        )
        assert updated is not None
        assert updated.status == SessionStatus.completed
        assert updated.duration_seconds == 1200
        assert updated.turn_count == 10

    async def test_update_session_status_with_detail(self, db_conn):
        sid = await _make_session(db_conn)
        updated = await update_session_status(
            db_conn,
            sid,
            "evaluation_failed",
            status_detail="LLM timeout",
        )
        assert updated.status == SessionStatus.evaluation_failed
        assert updated.status_detail == "LLM timeout"

    async def test_update_nonexistent_session(self, db_conn):
        result = await update_session_status(db_conn, 99999, "completed")
        assert result is None

    async def test_session_rejects_invalid_status(self, db_conn):
        qid = await _make_question(db_conn)
        with pytest.raises(asyncpg.exceptions.InvalidTextRepresentationError):
            await db_conn.execute(
                "INSERT INTO sessions (question_id, status) VALUES ($1, $2::session_status)",
                qid,
                "bogus",
            )


# --- Message Tests ---


class TestMessages:
    async def test_insert_and_get_messages(self, db_conn):
        sid = await _make_session(db_conn)
        m1 = await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=0,
                role=MessageRole.interviewer,
                content="Let's design a URL shortener.",
                raw_content="raw text",
            ),
        )
        m2 = await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=1,
                role=MessageRole.candidate,
                content="Sure, let me start with requirements.",
            ),
        )
        assert m1.id is not None
        assert m1.role == MessageRole.interviewer
        assert m1.raw_content == "raw text"
        assert m2.sequence == 1

        messages = await get_session_messages(db_conn, sid)
        assert len(messages) == 2
        assert messages[0].sequence == 0
        assert messages[1].sequence == 1

    async def test_message_sequence_unique_per_session(self, db_conn):
        sid = await _make_session(db_conn)
        await _make_message(db_conn, sid, sequence=0)
        with pytest.raises(asyncpg.exceptions.UniqueViolationError):
            await _make_message(db_conn, sid, sequence=0)

    async def test_message_with_audio(self, db_conn):
        sid = await _make_session(db_conn)
        m = await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=0,
                role=MessageRole.candidate,
                content="Answer",
                audio_path="/audio/001.wav",
                audio_duration_sec=12.5,
            ),
        )
        assert m.audio_path == "/audio/001.wav"
        assert m.audio_duration_sec == pytest.approx(12.5, abs=0.01)


# --- Evaluation Tests ---


class TestEvaluations:
    async def test_insert_and_get_evaluation(self, db_conn):
        sid = await _make_session(db_conn)
        e = await insert_evaluation(
            db_conn,
            EvaluationCreate(
                session_id=sid,
                score_requirements=4,
                score_highlevel=3,
                score_deepdive=2,
                score_scalability=5,
                score_communication=4,
                score_overall=3,
                strengths=["clear", "structured"],
                gaps=["scalability detail"],
                advice="Dig deeper on tradeoffs.",
                raw_response={"model": "claude", "tokens": 500},
            ),
        )
        assert e.id is not None
        assert e.score_requirements == 4
        assert e.strengths == ["clear", "structured"]
        assert e.gaps == ["scalability detail"]
        assert e.raw_response == {"model": "claude", "tokens": 500}

    async def test_get_latest_evaluation(self, db_conn):
        sid = await _make_session(db_conn)
        await _make_evaluation(db_conn, sid, score_overall=2)
        eid2 = await _make_evaluation(db_conn, sid, score_overall=4)

        latest = await get_latest_evaluation(db_conn, sid)
        assert latest is not None
        assert latest.id == eid2
        assert latest.score_overall == 4

    async def test_get_latest_evaluation_not_found(self, db_conn):
        assert await get_latest_evaluation(db_conn, 99999) is None

    async def test_get_evaluations_for_sessions(self, db_conn):
        sid1 = await _make_session(db_conn)
        sid2 = await _make_session(db_conn)
        await _make_evaluation(db_conn, sid1)
        await _make_evaluation(db_conn, sid2)

        evals = await get_evaluations_for_sessions(db_conn, [sid1, sid2])
        assert len(evals) == 2
        session_ids = {e.session_id for e in evals}
        assert sid1 in session_ids
        assert sid2 in session_ids

    async def test_evaluation_rejects_score_out_of_range(self, db_conn):
        sid = await _make_session(db_conn)
        with pytest.raises(asyncpg.exceptions.CheckViolationError):
            await db_conn.execute(
                """
                INSERT INTO evaluations (
                    session_id, score_requirements, score_highlevel, score_deepdive,
                    score_scalability, score_communication, score_overall,
                    strengths, gaps, advice, raw_response
                ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8::jsonb, $9::jsonb, $10, $11::jsonb)
                """,
                sid,
                6,  # score_requirements = 6 (invalid)
                3,
                3,
                3,
                3,
                3,
                json.dumps(["good"]),
                json.dumps(["bad"]),
                "advice",
                json.dumps({}),
            )


# --- Annotation Tests ---


class TestAnnotations:
    async def test_insert_and_get_annotations(self, db_conn):
        sid = await _make_session(db_conn)
        mid = await _make_message(db_conn, sid)
        eid = await _make_evaluation(db_conn, sid)

        a = await insert_message_annotation(
            db_conn,
            MessageAnnotationCreate(
                evaluation_id=eid,
                message_id=mid,
                annotation_type=AnnotationType.strength,
                content="Great explanation of trade-offs.",
            ),
        )
        assert a.id is not None
        assert a.annotation_type == AnnotationType.strength

        annotations = await get_message_annotations(db_conn, eid)
        assert len(annotations) == 1
        assert annotations[0].content == "Great explanation of trade-offs."

    async def test_annotation_unique_constraint(self, db_conn):
        sid = await _make_session(db_conn)
        mid = await _make_message(db_conn, sid)
        eid = await _make_evaluation(db_conn, sid)

        await insert_message_annotation(
            db_conn,
            MessageAnnotationCreate(
                evaluation_id=eid,
                message_id=mid,
                annotation_type=AnnotationType.gap,
                content="First gap.",
            ),
        )
        # Same eval + message + type = unique violation
        with pytest.raises(asyncpg.exceptions.UniqueViolationError):
            await insert_message_annotation(
                db_conn,
                MessageAnnotationCreate(
                    evaluation_id=eid,
                    message_id=mid,
                    annotation_type=AnnotationType.gap,
                    content="Second gap.",
                ),
            )

    async def test_multiple_annotation_types(self, db_conn):
        sid = await _make_session(db_conn)
        mid = await _make_message(db_conn, sid)
        eid = await _make_evaluation(db_conn, sid)

        for atype in AnnotationType:
            await insert_message_annotation(
                db_conn,
                MessageAnnotationCreate(
                    evaluation_id=eid,
                    message_id=mid,
                    annotation_type=atype,
                    content=f"Content for {atype.value}",
                ),
            )
        annotations = await get_message_annotations(db_conn, eid)
        assert len(annotations) == 4


# --- Coach Review Tests ---


class TestCoachReviews:
    async def test_insert_and_get_coach_review(self, db_conn):
        qid = await _make_question(db_conn)
        sid = await _make_session(db_conn, question_id=qid)

        c = await insert_coach_review(
            db_conn,
            CoachReviewCreate(
                recommendation="Focus on scalability patterns.",
                gap_analysis={"weak_areas": ["scalability", "deep dive"]},
                suggested_question_id=qid,
                sessions_analyzed=[sid],
                raw_response={"model": "claude"},
            ),
        )
        assert c.id is not None
        assert c.recommendation == "Focus on scalability patterns."
        assert c.gap_analysis == {"weak_areas": ["scalability", "deep dive"]}
        assert c.suggested_question_id == qid
        assert c.sessions_analyzed == [sid]

        latest = await get_latest_coach_review(db_conn)
        assert latest is not None
        assert latest.id == c.id

    async def test_get_latest_coach_review_empty(self, db_conn):
        # No coach reviews exist in a fresh transaction
        latest = await get_latest_coach_review(db_conn)
        assert latest is None

    async def test_coach_review_no_suggested_question(self, db_conn):
        c = await insert_coach_review(
            db_conn,
            CoachReviewCreate(
                recommendation="General advice.",
                gap_analysis={},
                raw_response={},
            ),
        )
        assert c.suggested_question_id is None
        assert c.sessions_analyzed == []


# --- Aggregate Tests ---


class TestAggregates:
    async def test_dimension_averages(self, db_conn):
        sid1 = await _make_session(db_conn)
        sid2 = await _make_session(db_conn)
        await _make_evaluation(
            db_conn,
            sid1,
            score_requirements=2,
            score_highlevel=4,
            score_deepdive=2,
            score_scalability=4,
            score_communication=2,
            score_overall=4,
        )
        await _make_evaluation(
            db_conn,
            sid2,
            score_requirements=4,
            score_highlevel=2,
            score_deepdive=4,
            score_scalability=2,
            score_communication=4,
            score_overall=2,
        )

        avgs = await get_dimension_averages(db_conn)
        assert avgs is not None
        assert avgs["requirements"] == pytest.approx(3.0)
        assert avgs["highlevel"] == pytest.approx(3.0)
        assert avgs["deepdive"] == pytest.approx(3.0)
        assert avgs["scalability"] == pytest.approx(3.0)
        assert avgs["communication"] == pytest.approx(3.0)
        assert avgs["overall"] == pytest.approx(3.0)

    async def test_dimension_averages_empty(self, db_conn):
        avgs = await get_dimension_averages(db_conn)
        assert avgs is None

    async def test_question_stats(self, db_conn):
        qid = await _make_question(db_conn, title="Stats Q")
        sid = await _make_session(db_conn, question_id=qid)
        await _make_evaluation(db_conn, sid, score_overall=4)

        stats = await get_question_stats(db_conn)
        stat = next(s for s in stats if s["question_id"] == qid)
        assert stat["title"] == "Stats Q"
        assert stat["session_count"] == 1
        assert stat["avg_overall"] == pytest.approx(4.0)

    async def test_question_stats_no_sessions(self, db_conn):
        qid = await _make_question(db_conn, title="No Sessions")
        stats = await get_question_stats(db_conn)
        stat = next(s for s in stats if s["question_id"] == qid)
        assert stat["session_count"] == 0
        assert stat["avg_overall"] is None

    async def test_latest_evaluation_time(self, db_conn):
        # Initially none
        t = await get_latest_evaluation_time(db_conn)
        assert t is None

        sid = await _make_session(db_conn)
        await _make_evaluation(db_conn, sid)

        t = await get_latest_evaluation_time(db_conn)
        assert t is not None

    async def test_get_evaluations_for_sessions_empty(self, db_conn):
        evals = await get_evaluations_for_sessions(db_conn, [])
        assert evals == []
