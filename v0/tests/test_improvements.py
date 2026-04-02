"""Tests for 002_improvements migration: archive, tts, educator fields, token tracking."""

import json

import pytest

from backend.database import (
    archive_session,
    archive_sessions_bulk,
    get_dimension_averages,
    get_question_stats,
    get_session,
    get_session_token_usage,
    insert_evaluation,
    insert_message,
    insert_question,
    insert_session,
    list_sessions,
)
from backend.models import (
    Difficulty,
    Evaluation,
    EvaluationCreate,
    Message,
    MessageCreate,
    MessageRole,
    QuestionCreate,
    QuestionSource,
    Session,
    SessionCreate,
    SessionStatus,
)


# --- Helpers ---


async def _make_question(conn, **overrides) -> int:
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
    if question_id is None:
        question_id = await _make_question(conn)
    s = await insert_session(conn, SessionCreate(question_id=question_id, **overrides))
    return s.id


async def _make_evaluation(conn, session_id, **overrides) -> int:
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


# --- Model Default Tests ---


class TestModelDefaults:
    def test_session_create_tts_enabled_default(self):
        s = SessionCreate(question_id=1)
        assert s.tts_enabled is True

    def test_session_create_tts_enabled_false(self):
        s = SessionCreate(question_id=1, tts_enabled=False)
        assert s.tts_enabled is False

    def test_session_archived_default(self):
        s = Session(
            id=1,
            question_id=1,
            status=SessionStatus.active,
            timer_setting_sec=2700,
            interviewer_briefed=False,
            started_at="2025-01-01T00:00:00Z",
        )
        assert s.archived is False
        assert s.tts_enabled is True

    def test_message_create_raw_response_default(self):
        m = MessageCreate(
            session_id=1, sequence=0, role=MessageRole.interviewer, content="Hi"
        )
        assert m.raw_response is None

    def test_message_create_raw_response_set(self):
        m = MessageCreate(
            session_id=1,
            sequence=0,
            role=MessageRole.interviewer,
            content="Hi",
            raw_response={"model": "claude", "usage": {"input_tokens": 10}},
        )
        assert m.raw_response["model"] == "claude"

    def test_message_raw_response_default(self):
        m = Message(
            id=1,
            session_id=1,
            sequence=0,
            role=MessageRole.interviewer,
            content="Hi",
            timestamp="2025-01-01T00:00:00Z",
        )
        assert m.raw_response is None

    def test_evaluation_educator_fields_default(self):
        e = Evaluation(
            id=1,
            session_id=1,
            score_requirements=3,
            score_highlevel=3,
            score_deepdive=3,
            score_scalability=3,
            score_communication=3,
            score_overall=3,
            strengths=["good"],
            gaps=["improve"],
            advice="advice",
            raw_response={"model": "test"},
            evaluated_at="2025-01-01T00:00:00Z",
        )
        assert e.educator_model_answer is None
        assert e.educator_gap_deepdives is None
        assert e.educator_raw_response is None


# --- Archive Tests ---


class TestArchive:
    async def test_archive_session(self, db_conn):
        sid = await _make_session(db_conn)
        session = await get_session(db_conn, sid)
        assert session.archived is False

        await archive_session(db_conn, sid, True)
        session = await get_session(db_conn, sid)
        assert session.archived is True

        # Unarchive
        await archive_session(db_conn, sid, False)
        session = await get_session(db_conn, sid)
        assert session.archived is False

    async def test_archive_sessions_bulk(self, db_conn):
        sid1 = await _make_session(db_conn)
        sid2 = await _make_session(db_conn)
        sid3 = await _make_session(db_conn)

        await archive_sessions_bulk(db_conn, [sid1, sid2], True)

        s1 = await get_session(db_conn, sid1)
        s2 = await get_session(db_conn, sid2)
        s3 = await get_session(db_conn, sid3)
        assert s1.archived is True
        assert s2.archived is True
        assert s3.archived is False

        # Bulk unarchive
        await archive_sessions_bulk(db_conn, [sid1, sid2], False)
        s1 = await get_session(db_conn, sid1)
        s2 = await get_session(db_conn, sid2)
        assert s1.archived is False
        assert s2.archived is False

    async def test_list_sessions_excludes_archived(self, db_conn):
        sid1 = await _make_session(db_conn)
        sid2 = await _make_session(db_conn)
        await archive_session(db_conn, sid1, True)

        sessions = await list_sessions(db_conn)
        session_ids = {s.id for s in sessions}
        assert sid1 not in session_ids
        assert sid2 in session_ids

    async def test_list_sessions_include_archived(self, db_conn):
        sid1 = await _make_session(db_conn)
        sid2 = await _make_session(db_conn)
        await archive_session(db_conn, sid1, True)

        sessions = await list_sessions(db_conn, include_archived=True)
        session_ids = {s.id for s in sessions}
        assert sid1 in session_ids
        assert sid2 in session_ids


# --- TTS Enabled Tests ---


class TestTtsEnabled:
    async def test_session_tts_enabled_default(self, db_conn):
        sid = await _make_session(db_conn)
        session = await get_session(db_conn, sid)
        assert session.tts_enabled is True

    async def test_session_tts_enabled_false(self, db_conn):
        sid = await _make_session(db_conn, tts_enabled=False)
        session = await get_session(db_conn, sid)
        assert session.tts_enabled is False


# --- Message raw_response Tests ---


class TestMessageRawResponse:
    async def test_insert_message_without_raw_response(self, db_conn):
        sid = await _make_session(db_conn)
        m = await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=0,
                role=MessageRole.interviewer,
                content="Hello",
            ),
        )
        assert m.raw_response is None

    async def test_insert_message_with_raw_response(self, db_conn):
        sid = await _make_session(db_conn)
        raw = {
            "model": "claude-sonnet-4-20250514",
            "usage": {"input_tokens": 150, "output_tokens": 200},
        }
        m = await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=0,
                role=MessageRole.interviewer,
                content="Hello",
                raw_response=raw,
            ),
        )
        assert m.raw_response is not None
        assert m.raw_response["model"] == "claude-sonnet-4-20250514"
        assert m.raw_response["usage"]["input_tokens"] == 150
        assert m.raw_response["usage"]["output_tokens"] == 200


# --- Dimension Averages Excludes Archived ---


class TestDimensionAveragesArchived:
    async def test_excludes_archived_sessions(self, db_conn):
        sid1 = await _make_session(db_conn)
        sid2 = await _make_session(db_conn)
        await _make_evaluation(db_conn, sid1, score_overall=4, score_requirements=4,
                               score_highlevel=4, score_deepdive=4,
                               score_scalability=4, score_communication=4)
        await _make_evaluation(db_conn, sid2, score_overall=2, score_requirements=2,
                               score_highlevel=2, score_deepdive=2,
                               score_scalability=2, score_communication=2)

        # Before archiving, average is 3.0
        avgs = await get_dimension_averages(db_conn)
        assert avgs["overall"] == pytest.approx(3.0)

        # Archive sid2 (score 2), now average should be 4.0
        await archive_session(db_conn, sid2, True)
        avgs = await get_dimension_averages(db_conn)
        assert avgs["overall"] == pytest.approx(4.0)
        assert avgs["requirements"] == pytest.approx(4.0)

    async def test_all_archived_returns_none(self, db_conn):
        sid = await _make_session(db_conn)
        await _make_evaluation(db_conn, sid)
        await archive_session(db_conn, sid, True)
        avgs = await get_dimension_averages(db_conn)
        assert avgs is None


# --- Question Stats Excludes Archived ---


class TestQuestionStatsArchived:
    async def test_excludes_archived_sessions(self, db_conn):
        qid = await _make_question(db_conn, title="Stats Archived Q")
        sid1 = await _make_session(db_conn, question_id=qid)
        sid2 = await _make_session(db_conn, question_id=qid)
        await _make_evaluation(db_conn, sid1, score_overall=4)
        await _make_evaluation(db_conn, sid2, score_overall=2)

        await archive_session(db_conn, sid2, True)

        stats = await get_question_stats(db_conn)
        stat = next(s for s in stats if s["question_id"] == qid)
        assert stat["session_count"] == 1
        assert stat["avg_overall"] == pytest.approx(4.0)


# --- Token Usage View Tests ---


class TestTokenUsageView:
    async def test_token_usage_for_message(self, db_conn):
        sid = await _make_session(db_conn)
        raw = {
            "model": "claude-sonnet-4-20250514",
            "usage": {"input_tokens": 100, "output_tokens": 50},
        }
        await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=0,
                role=MessageRole.interviewer,
                content="Hello",
                raw_response=raw,
            ),
        )

        usage = await get_session_token_usage(db_conn, sid)
        assert len(usage) >= 1
        row = usage[0]
        assert row["role"] == "interviewer"
        assert row["model"] == "claude-sonnet-4-20250514"
        assert row["input_tokens"] == 100
        assert row["output_tokens"] == 50

    async def test_token_usage_excludes_candidate_messages(self, db_conn):
        sid = await _make_session(db_conn)
        # Candidate message with raw_response should NOT appear in view
        # (view only includes interviewer role)
        await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=0,
                role=MessageRole.candidate,
                content="My answer",
                raw_response={"model": "test", "usage": {"input_tokens": 10, "output_tokens": 5}},
            ),
        )
        usage = await get_session_token_usage(db_conn, sid)
        assert len(usage) == 0

    async def test_token_usage_no_raw_response(self, db_conn):
        sid = await _make_session(db_conn)
        await insert_message(
            db_conn,
            MessageCreate(
                session_id=sid,
                sequence=0,
                role=MessageRole.interviewer,
                content="Hello",
            ),
        )
        usage = await get_session_token_usage(db_conn, sid)
        assert len(usage) == 0


# --- Evaluation Educator Fields ---


class TestEvaluationEducatorFields:
    async def test_evaluation_educator_fields_null_by_default(self, db_conn):
        sid = await _make_session(db_conn)
        eid = await _make_evaluation(db_conn, sid)
        # Fetch back via raw query to verify DB nulls
        row = await db_conn.fetchrow("SELECT * FROM evaluations WHERE id = $1", eid)
        assert row["educator_model_answer"] is None
        assert row["educator_gap_deepdives"] is None
        assert row["educator_raw_response"] is None
