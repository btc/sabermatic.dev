"""Tests for backend.models — Pydantic models and enums."""

import pytest
from pydantic import ValidationError

from backend.models import (
    AnnotationType,
    CoachReview,
    CoachReviewCreate,
    Difficulty,
    Evaluation,
    EvaluationCreate,
    Message,
    MessageAnnotation,
    MessageAnnotationCreate,
    MessageCreate,
    MessageRole,
    Question,
    QuestionCreate,
    QuestionSource,
    Session,
    SessionCreate,
    SessionStatus,
)


# --- Enum Tests ---


class TestEnums:
    def test_difficulty_values(self):
        assert Difficulty.medium == "medium"
        assert Difficulty.hard == "hard"
        assert set(Difficulty) == {Difficulty.medium, Difficulty.hard}

    def test_question_source_values(self):
        assert QuestionSource.seed == "seed"
        assert QuestionSource.custom == "custom"
        assert QuestionSource.coach_generated == "coach_generated"

    def test_session_status_values(self):
        expected = {"active", "completed", "evaluating", "reviewed", "evaluation_failed"}
        assert {s.value for s in SessionStatus} == expected

    def test_session_status_includes_evaluation_failed(self):
        assert SessionStatus.evaluation_failed == "evaluation_failed"

    def test_message_role_values(self):
        assert MessageRole.interviewer == "interviewer"
        assert MessageRole.candidate == "candidate"

    def test_annotation_type_values(self):
        expected = {"strength", "gap", "missed_opportunity", "note"}
        assert {a.value for a in AnnotationType} == expected

    def test_enums_are_str_subclass(self):
        """Enums should be usable as plain strings."""
        assert isinstance(Difficulty.medium, str)
        assert isinstance(SessionStatus.active, str)
        assert isinstance(MessageRole.interviewer, str)
        assert isinstance(AnnotationType.strength, str)
        assert isinstance(QuestionSource.seed, str)


# --- Question Tests ---


class TestQuestion:
    def test_question_create_minimal(self):
        q = QuestionCreate(
            title="URL Shortener",
            prompt="Design a URL shortener.",
            difficulty=Difficulty.medium,
        )
        assert q.title == "URL Shortener"
        assert q.tags == []
        assert q.hints is None
        assert q.source == QuestionSource.seed
        assert q.source_detail is None

    def test_question_create_full(self):
        q = QuestionCreate(
            title="News Feed",
            prompt="Design a news feed.",
            difficulty=Difficulty.hard,
            tags=["fanout", "caching"],
            hints={"areas": ["push vs pull"]},
            source=QuestionSource.custom,
            source_detail="user submitted",
        )
        assert q.difficulty == Difficulty.hard
        assert q.tags == ["fanout", "caching"]
        assert q.hints == {"areas": ["push vs pull"]}

    def test_question_create_rejects_invalid_difficulty(self):
        with pytest.raises(ValidationError):
            QuestionCreate(
                title="X",
                prompt="Y",
                difficulty="easy",
            )

    def test_question_create_accepts_string_enum(self):
        q = QuestionCreate(
            title="X",
            prompt="Y",
            difficulty="hard",
        )
        assert q.difficulty == Difficulty.hard


# --- Session Tests ---


class TestSession:
    def test_session_create_defaults(self):
        s = SessionCreate(question_id=1)
        assert s.status == SessionStatus.active
        assert s.timer_setting_sec == 2700
        assert s.interviewer_briefed is False
        assert s.ended_at is None
        assert s.duration_seconds is None
        assert s.turn_count is None
        assert s.audio_dir is None
        assert s.status_detail is None

    def test_session_create_rejects_invalid_status(self):
        with pytest.raises(ValidationError):
            SessionCreate(question_id=1, status="bogus")

    def test_session_create_accepts_all_statuses(self):
        for status in SessionStatus:
            s = SessionCreate(question_id=1, status=status)
            assert s.status == status


# --- Message Tests ---


class TestMessage:
    def test_message_create_minimal(self):
        m = MessageCreate(
            session_id=1,
            sequence=0,
            role=MessageRole.interviewer,
            content="Hello",
        )
        assert m.raw_content is None
        assert m.audio_path is None
        assert m.audio_duration_sec is None

    def test_message_create_rejects_invalid_role(self):
        with pytest.raises(ValidationError):
            MessageCreate(
                session_id=1,
                sequence=0,
                role="system",
                content="Hello",
            )


# --- Evaluation Tests ---


class TestEvaluation:
    def _valid_kwargs(self, **overrides):
        defaults = dict(
            session_id=1,
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
        return defaults

    def test_evaluation_create_valid(self):
        e = EvaluationCreate(**self._valid_kwargs())
        assert e.score_requirements == 3
        assert e.strengths == ["good"]

    def test_evaluation_create_rejects_score_zero(self):
        with pytest.raises(ValidationError, match="Score must be between 1 and 5"):
            EvaluationCreate(**self._valid_kwargs(score_requirements=0))

    def test_evaluation_create_rejects_score_six(self):
        with pytest.raises(ValidationError, match="Score must be between 1 and 5"):
            EvaluationCreate(**self._valid_kwargs(score_overall=6))

    def test_evaluation_create_rejects_negative_score(self):
        with pytest.raises(ValidationError, match="Score must be between 1 and 5"):
            EvaluationCreate(**self._valid_kwargs(score_deepdive=-1))

    def test_evaluation_all_score_fields_validated(self):
        score_fields = [
            "score_requirements",
            "score_highlevel",
            "score_deepdive",
            "score_scalability",
            "score_communication",
            "score_overall",
        ]
        for field in score_fields:
            with pytest.raises(ValidationError):
                EvaluationCreate(**self._valid_kwargs(**{field: 0}))
            with pytest.raises(ValidationError):
                EvaluationCreate(**self._valid_kwargs(**{field: 6}))

    def test_evaluation_boundary_scores(self):
        e1 = EvaluationCreate(**self._valid_kwargs(score_requirements=1))
        assert e1.score_requirements == 1
        e5 = EvaluationCreate(**self._valid_kwargs(score_requirements=5))
        assert e5.score_requirements == 5


# --- MessageAnnotation Tests ---


class TestMessageAnnotation:
    def test_annotation_create(self):
        a = MessageAnnotationCreate(
            evaluation_id=1,
            message_id=2,
            annotation_type=AnnotationType.strength,
            content="Good explanation",
        )
        assert a.annotation_type == AnnotationType.strength

    def test_annotation_rejects_invalid_type(self):
        with pytest.raises(ValidationError):
            MessageAnnotationCreate(
                evaluation_id=1,
                message_id=2,
                annotation_type="invalid_type",
                content="X",
            )


# --- CoachReview Tests ---


class TestCoachReview:
    def test_coach_review_create_defaults(self):
        c = CoachReviewCreate(
            recommendation="Practice more.",
            gap_analysis={"weak": ["scalability"]},
            raw_response={"model": "test"},
        )
        assert c.suggested_question_id is None
        assert c.sessions_analyzed == []
        assert c.created_at is None

    def test_coach_review_create_full(self):
        c = CoachReviewCreate(
            recommendation="Practice more.",
            gap_analysis={"weak": ["scalability"]},
            suggested_question_id=5,
            sessions_analyzed=[1, 2, 3],
            raw_response={"model": "test"},
        )
        assert c.sessions_analyzed == [1, 2, 3]
        assert c.suggested_question_id == 5
