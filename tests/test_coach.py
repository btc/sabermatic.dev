"""Tests for backend.coach — strategic coaching module."""

from datetime import datetime
from unittest.mock import MagicMock

import pytest

from backend.coach import Coach, CoachConfig, COACH_TOOL
from backend.models import (
    CoachReviewCreate,
    Difficulty,
    Evaluation,
    Question,
    QuestionCreate,
    QuestionSource,
    Session,
    SessionStatus,
)


# --- Helpers ---


def _make_question(
    qid: int,
    title: str = "Test Q",
    tags: list[str] | None = None,
    difficulty: Difficulty = Difficulty.medium,
) -> Question:
    return Question(
        id=qid,
        title=title,
        prompt=f"Design a {title}",
        difficulty=difficulty,
        tags=tags or ["distributed-systems"],
        source=QuestionSource.seed,
        created_at=datetime(2025, 6, 1),
    )


def _make_session(
    sid: int,
    question_id: int,
    status: SessionStatus = SessionStatus.reviewed,
    duration_seconds: int = 2700,
) -> Session:
    return Session(
        id=sid,
        question_id=question_id,
        status=status,
        timer_setting_sec=2700,
        interviewer_briefed=True,
        started_at=datetime(2025, 6, 1, 12, 0, 0),
        ended_at=datetime(2025, 6, 1, 12, 45, 0),
        duration_seconds=duration_seconds,
    )


def _make_evaluation(
    eid: int,
    session_id: int,
    score_overall: int = 3,
    score_requirements: int = 3,
    score_highlevel: int = 3,
    score_deepdive: int = 3,
    score_scalability: int = 3,
    score_communication: int = 3,
) -> Evaluation:
    return Evaluation(
        id=eid,
        session_id=session_id,
        score_requirements=score_requirements,
        score_highlevel=score_highlevel,
        score_deepdive=score_deepdive,
        score_scalability=score_scalability,
        score_communication=score_communication,
        score_overall=score_overall,
        strengths=["Good requirements"],
        gaps=["Missed caching"],
        advice="Focus on scalability.",
        raw_response={},
        evaluated_at=datetime(2025, 6, 1, 13, 0, 0),
    )


def _valid_coach_tool_input() -> dict:
    """A well-formed submit_analysis tool input."""
    return {
        "recommendation": "Focus on distributed caching patterns next.",
        "gap_analysis": {
            "weakest_dimension": "scalability",
            "improving_dimensions": ["requirements", "communication"],
            "topic_gaps": ["caching", "message queues"],
            "thinking_patterns": [
                "Tends to skip requirements gathering under pressure"
            ],
        },
        "generated_question": None,
    }


# --- Fixtures ---


@pytest.fixture
def coach():
    return Coach(config=CoachConfig())


# --- Config Tests ---


class TestCoachConfig:
    def test_default_config(self):
        config = CoachConfig()
        assert config.model is not None
        assert config.max_tokens > 0

    def test_custom_config(self):
        config = CoachConfig(model="claude-sonnet-4-20250514", max_tokens=8192)
        assert config.model == "claude-sonnet-4-20250514"
        assert config.max_tokens == 8192


# --- Tool Schema Tests ---


class TestCoachToolSchema:
    def test_coach_tool_schema(self, coach):
        """Verify tool schema structure."""
        tools = coach.get_tool_schema()
        assert len(tools) == 1
        assert tools[0]["name"] == "submit_analysis"

        props = tools[0]["input_schema"]["properties"]
        assert "recommendation" in props
        assert "gap_analysis" in props
        assert "generated_question" in props

        required = tools[0]["input_schema"]["required"]
        assert "recommendation" in required
        assert "gap_analysis" in required

    def test_coach_tool_gap_analysis_structure(self, coach):
        """Gap analysis should have the expected sub-fields."""
        tools = coach.get_tool_schema()
        gap_props = tools[0]["input_schema"]["properties"]["gap_analysis"]["properties"]
        assert "weakest_dimension" in gap_props
        assert "improving_dimensions" in gap_props
        assert "topic_gaps" in gap_props
        assert "thinking_patterns" in gap_props

    def test_coach_tool_generated_question_structure(self, coach):
        """Generated question should have title, prompt, difficulty, tags."""
        tools = coach.get_tool_schema()
        gq_props = tools[0]["input_schema"]["properties"]["generated_question"]["properties"]
        assert "title" in gq_props
        assert "prompt" in gq_props
        assert "difficulty" in gq_props
        assert "tags" in gq_props


# --- Build History Summary Tests ---


class TestBuildHistorySummary:
    def test_build_history_summary_maps_questions_correctly(self, coach):
        """Verify the coach resolves session->question correctly.

        The mapping must go: evaluation.session_id -> session.question_id -> question
        NOT: question_map.get(ev.session_id) — that was the bug in v1.
        """
        # Question 10 was attempted in session 100
        question = _make_question(10, title="URL Shortener", tags=["url", "hashing"])
        session = _make_session(100, question_id=10)
        evaluation = _make_evaluation(1, session_id=100, score_overall=4)

        summary = coach.build_history_summary(
            sessions=[session],
            evaluations=[evaluation],
            questions=[question],
        )

        # The summary must mention the question title (proves correct mapping)
        assert "URL Shortener" in summary
        # The summary should include the score
        assert "4" in summary

    def test_build_history_summary_only_includes_attempted_tags(self, coach):
        """Only questions that have reviewed sessions should contribute tags."""
        # Question 10 has a reviewed session -> its tags appear
        q_attempted = _make_question(10, title="Cache", tags=["caching", "distributed"])
        s_reviewed = _make_session(100, question_id=10, status=SessionStatus.reviewed)
        ev = _make_evaluation(1, session_id=100)

        # Question 20 has no sessions -> its tags do NOT appear
        q_unattempted = _make_question(20, title="Queue", tags=["messaging", "async"])

        summary = coach.build_history_summary(
            sessions=[s_reviewed],
            evaluations=[ev],
            questions=[q_attempted, q_unattempted],
        )

        # Attempted tags should appear
        assert "caching" in summary.lower() or "distributed" in summary.lower()
        # Unattempted tags should NOT appear in the attempted/covered section
        # We check the "Topics covered" or similar section doesn't include messaging
        # The unattempted question's tags should not be in the "covered" section
        lines = summary.lower().split("\n")
        covered_section = False
        for line in lines:
            if "covered" in line or "attempted" in line:
                covered_section = True
            if covered_section and ("not covered" in line or "gap" in line or "available" in line):
                covered_section = False
            if covered_section:
                assert "messaging" not in line
                assert "async" not in line

    def test_build_history_summary_empty(self, coach):
        """No sessions -> suggests starting with medium difficulty."""
        summary = coach.build_history_summary(
            sessions=[],
            evaluations=[],
            questions=[],
        )

        assert "medium" in summary.lower() or "no" in summary.lower()
        # Should indicate there's no history
        assert len(summary) > 0

    def test_build_history_summary_multiple_sessions(self, coach):
        """Multiple sessions with different questions produce a comprehensive summary."""
        q1 = _make_question(10, title="URL Shortener", tags=["hashing"])
        q2 = _make_question(20, title="Chat System", tags=["websocket", "messaging"])
        s1 = _make_session(100, question_id=10)
        s2 = _make_session(200, question_id=20)
        ev1 = _make_evaluation(1, session_id=100, score_overall=3, score_scalability=2)
        ev2 = _make_evaluation(2, session_id=200, score_overall=4, score_scalability=4)

        summary = coach.build_history_summary(
            sessions=[s1, s2],
            evaluations=[ev1, ev2],
            questions=[q1, q2],
        )

        assert "URL Shortener" in summary
        assert "Chat System" in summary


# --- Parse Response Tests ---


class TestParseResponse:
    def test_parse_coach_response_valid(self, coach):
        """Well-formed tool_use response -> CoachReviewCreate."""
        raw = _valid_coach_tool_input()
        raw_response = {"tool_input": raw, "model": "test"}

        review, question = coach.parse_response(
            raw=raw,
            session_ids=[100, 200],
            raw_response=raw_response,
        )

        assert isinstance(review, CoachReviewCreate)
        assert review.recommendation == "Focus on distributed caching patterns next."
        assert review.gap_analysis["weakest_dimension"] == "scalability"
        assert review.sessions_analyzed == [100, 200]
        assert review.raw_response == raw_response
        assert question is None

    def test_parse_coach_response_with_generated_question(self, coach):
        """Response with generated_question creates QuestionCreate."""
        raw = _valid_coach_tool_input()
        raw["generated_question"] = {
            "title": "Design a Distributed Cache",
            "prompt": "Design a distributed caching system like Memcached or Redis.",
            "difficulty": "hard",
            "tags": ["caching", "distributed-systems"],
        }
        raw_response = {"tool_input": raw, "model": "test"}

        review, question = coach.parse_response(
            raw=raw,
            session_ids=[100],
            raw_response=raw_response,
        )

        assert isinstance(review, CoachReviewCreate)
        assert isinstance(question, QuestionCreate)
        assert question.title == "Design a Distributed Cache"
        assert question.difficulty == Difficulty.hard
        assert question.tags == ["caching", "distributed-systems"]
        assert question.source == QuestionSource.coach_generated
        assert question.source_detail == review.recommendation

    def test_parse_coach_response_no_generated_question(self, coach):
        """Response without generated_question returns None for question."""
        raw = _valid_coach_tool_input()
        raw["generated_question"] = None
        raw_response = {}

        review, question = coach.parse_response(
            raw=raw,
            session_ids=[100],
            raw_response=raw_response,
        )

        assert isinstance(review, CoachReviewCreate)
        assert question is None

    def test_parse_coach_response_missing_recommendation(self, coach):
        """Missing recommendation should raise KeyError."""
        raw = _valid_coach_tool_input()
        del raw["recommendation"]

        with pytest.raises(KeyError, match="recommendation"):
            coach.parse_response(raw=raw, session_ids=[], raw_response={})

    def test_parse_coach_response_missing_gap_analysis(self, coach):
        """Missing gap_analysis should raise KeyError."""
        raw = _valid_coach_tool_input()
        del raw["gap_analysis"]

        with pytest.raises(KeyError, match="gap_analysis"):
            coach.parse_response(raw=raw, session_ids=[], raw_response={})


# --- System Prompt Tests ---


class TestBuildSystemPrompt:
    def test_system_prompt_contains_coach_responsibilities(self, coach):
        """System prompt must include all coaching responsibilities from design spec."""
        prompt = coach.build_system_prompt()
        prompt_lower = prompt.lower()

        # Dimension analysis
        assert "dimension" in prompt_lower
        # Topic coverage
        assert "topic" in prompt_lower
        # Thinking pattern recognition
        assert "pattern" in prompt_lower or "thinking" in prompt_lower
        # Scenario generation
        assert "scenario" in prompt_lower or "question" in prompt_lower
        # Progressive difficulty
        assert "difficulty" in prompt_lower
        # Metacognitive coaching
        assert "metacognitive" in prompt_lower or "self-aware" in prompt_lower or "reflection" in prompt_lower


# --- Analyze Integration Test ---


class TestAnalyze:
    async def test_analyze_calls_api_with_correct_params(self, coach):
        """analyze() should call the Anthropic API with tools and tool_choice."""
        raw = _valid_coach_tool_input()

        mock_tool_block = MagicMock()
        mock_tool_block.type = "tool_use"
        mock_tool_block.input = raw

        mock_response = MagicMock()
        mock_response.content = [mock_tool_block]
        mock_response.model_dump.return_value = {
            "content": [{"type": "tool_use", "input": raw}]
        }

        mock_client = MagicMock()
        mock_client.messages.create.return_value = mock_response

        question = _make_question(10, title="URL Shortener")
        session = _make_session(100, question_id=10)
        evaluation = _make_evaluation(1, session_id=100)

        review, generated_q = await coach.analyze(
            client=mock_client,
            sessions=[session],
            evaluations=[evaluation],
            questions=[question],
            session_ids=[100],
        )

        assert isinstance(review, CoachReviewCreate)
        assert review.sessions_analyzed == [100]

        # Verify API was called with tools and tool_choice
        call_kwargs = mock_client.messages.create.call_args.kwargs
        assert "tools" in call_kwargs
        assert call_kwargs["tools"][0]["name"] == "submit_analysis"
        assert call_kwargs["tool_choice"] == {
            "type": "tool",
            "name": "submit_analysis",
        }

    async def test_analyze_returns_generated_question(self, coach):
        """analyze() should return generated question when present."""
        raw = _valid_coach_tool_input()
        raw["generated_question"] = {
            "title": "Design a Rate Limiter",
            "prompt": "Design a distributed rate limiting system.",
            "difficulty": "medium",
            "tags": ["rate-limiting"],
        }

        mock_tool_block = MagicMock()
        mock_tool_block.type = "tool_use"
        mock_tool_block.input = raw

        mock_response = MagicMock()
        mock_response.content = [mock_tool_block]
        mock_response.model_dump.return_value = {
            "content": [{"type": "tool_use", "input": raw}]
        }

        mock_client = MagicMock()
        mock_client.messages.create.return_value = mock_response

        question = _make_question(10, title="URL Shortener")
        session = _make_session(100, question_id=10)
        evaluation = _make_evaluation(1, session_id=100)

        review, generated_q = await coach.analyze(
            client=mock_client,
            sessions=[session],
            evaluations=[evaluation],
            questions=[question],
            session_ids=[100],
        )

        assert isinstance(generated_q, QuestionCreate)
        assert generated_q.title == "Design a Rate Limiter"
        assert generated_q.source == QuestionSource.coach_generated
