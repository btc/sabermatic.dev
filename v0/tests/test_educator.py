"""Tests for backend.educator — LLM educator module."""

from datetime import datetime
from unittest.mock import AsyncMock, MagicMock

import pytest

from backend.educator import Educator, EducatorConfig
from backend.models import Message, MessageRole


# --- Fixtures ---


@pytest.fixture
def educator():
    return Educator(config=EducatorConfig())


def _make_message(seq: int, role: MessageRole, content: str) -> Message:
    return Message(
        id=seq,
        session_id=1,
        sequence=seq,
        role=role,
        content=content,
        timestamp=datetime(2025, 6, 1, 12, 0, seq),
    )


# --- Config Tests ---


class TestEducatorConfig:
    def test_educator_config_defaults(self):
        config = EducatorConfig()
        assert config.model == "claude-opus-4-6"
        assert config.max_tokens == 8000

    def test_custom_config(self):
        config = EducatorConfig(model="claude-sonnet-4-20250514", max_tokens=4096)
        assert config.model == "claude-sonnet-4-20250514"
        assert config.max_tokens == 4096


# --- Tool Schema Tests ---


class TestEducatorToolSchema:
    def test_educator_tool_schema(self, educator):
        """Verify the educator defines a tool schema for structured output."""
        tools = educator.get_tool_schema()
        assert tools[0]["name"] == "submit_education"
        assert "model_answer" in tools[0]["input_schema"]["properties"]
        assert "gap_deepdives" in tools[0]["input_schema"]["properties"]

    def test_tool_schema_required_fields(self, educator):
        """Schema must require model_answer and gap_deepdives."""
        tools = educator.get_tool_schema()
        required = tools[0]["input_schema"]["required"]
        assert "model_answer" in required
        assert "gap_deepdives" in required


# --- Build Prompt Tests ---


class TestBuildPrompt:
    def test_educator_builds_prompt_with_gaps(self, educator):
        prompt = educator.build_prompt(
            question_title="URL Shortener",
            question_prompt="Design a URL shortener.",
            transcript_text="...",
            evaluation_summary="Gaps: no cache invalidation discussion",
        )
        assert "URL Shortener" in prompt
        assert "cache invalidation" in prompt

    def test_prompt_includes_question_context(self, educator):
        prompt = educator.build_prompt(
            question_title="Distributed Cache",
            question_prompt="Design a distributed cache like Redis.",
            transcript_text="Interviewer: Design a cache.",
            evaluation_summary="Scores: Overall=3",
        )
        assert "Distributed Cache" in prompt
        assert "Design a distributed cache like Redis." in prompt

    def test_prompt_includes_transcript(self, educator):
        transcript = "Interviewer: Hello\nCandidate: Hi"
        prompt = educator.build_prompt(
            question_title="Test",
            question_prompt="Test prompt.",
            transcript_text=transcript,
            evaluation_summary="Scores: Overall=3",
        )
        assert transcript in prompt

    def test_prompt_includes_evaluation_summary(self, educator):
        eval_summary = "Gaps: missing sharding strategy, no monitoring"
        prompt = educator.build_prompt(
            question_title="Test",
            question_prompt="Test prompt.",
            transcript_text="...",
            evaluation_summary=eval_summary,
        )
        assert eval_summary in prompt

    def test_prompt_contains_teaching_instructions(self, educator):
        """System prompt must contain teaching-oriented instructions."""
        prompt = educator.build_prompt(
            question_title="Test",
            question_prompt="Test.",
            transcript_text="...",
            evaluation_summary="...",
        )
        prompt_lower = prompt.lower()
        assert "teach" in prompt_lower
        assert "model answer" in prompt_lower
        assert "gap" in prompt_lower


# --- Build Transcript Text Tests ---


class TestBuildTranscriptText:
    def test_build_transcript_text(self, educator):
        """Verify transcript formatting for educator."""
        messages = [
            _make_message(1, MessageRole.interviewer, "Design a URL shortener."),
            _make_message(2, MessageRole.candidate, "Let me start with requirements."),
            _make_message(3, MessageRole.interviewer, "Go ahead."),
            _make_message(4, MessageRole.candidate, "We need to handle 100M URLs per day."),
        ]

        text = educator.build_transcript_text(
            question_title="URL Shortener",
            question_prompt="Design a URL shortening service like bit.ly.",
            messages=messages,
        )

        assert "URL Shortener" in text
        assert "Design a URL shortening service like bit.ly." in text
        assert "Design a URL shortener." in text
        assert "Let me start with requirements." in text
        assert "Interviewer" in text
        assert "Candidate" in text

    def test_build_transcript_text_includes_sequence_numbers(self, educator):
        """Transcript should include message sequence numbers."""
        messages = [
            _make_message(1, MessageRole.interviewer, "Hello."),
            _make_message(2, MessageRole.candidate, "Hi."),
        ]
        text = educator.build_transcript_text(
            question_title="Test",
            question_prompt="Test prompt.",
            messages=messages,
        )
        assert "[1]" in text
        assert "[2]" in text

    def test_build_transcript_text_empty_messages(self, educator):
        """Empty message list should still produce header with question info."""
        text = educator.build_transcript_text(
            question_title="Test",
            question_prompt="Test prompt.",
            messages=[],
        )
        assert "Test" in text
        assert "Test prompt." in text


# --- Educate Integration Test ---


class TestEducate:
    async def test_educate_calls_api_with_correct_params(self, educator):
        """educate() should call the Anthropic API with tools and tool_choice."""
        tool_input = {
            "model_answer": "# Model Answer\nUse a hash-based approach...",
            "gap_deepdives": "# Gap: Cache Invalidation\nThree approaches...",
        }

        mock_tool_block = MagicMock()
        mock_tool_block.type = "tool_use"
        mock_tool_block.input = tool_input

        mock_response = MagicMock()
        mock_response.content = [mock_tool_block]
        mock_response.model_dump.return_value = {
            "content": [{"type": "tool_use", "input": tool_input}]
        }

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(return_value=mock_response)

        model_answer, gap_deepdives, raw_response = await educator.educate(
            client=mock_client,
            question_title="URL Shortener",
            question_prompt="Design a URL shortener.",
            transcript_text="Interviewer: Design a URL shortener.",
            evaluation_summary="Gaps: no caching discussion",
        )

        assert model_answer == tool_input["model_answer"]
        assert gap_deepdives == tool_input["gap_deepdives"]
        assert isinstance(raw_response, dict)

        # Verify API was called with tools and tool_choice
        call_kwargs = mock_client.messages.create.call_args.kwargs
        assert "tools" in call_kwargs
        assert call_kwargs["tools"][0]["name"] == "submit_education"
        assert call_kwargs["tool_choice"] == {
            "type": "tool",
            "name": "submit_education",
        }

    async def test_educate_uses_configured_model(self):
        """educate() should use the model from config."""
        educator = Educator(EducatorConfig(model="claude-sonnet-4-20250514"))

        tool_input = {
            "model_answer": "answer",
            "gap_deepdives": "deepdives",
        }

        mock_tool_block = MagicMock()
        mock_tool_block.type = "tool_use"
        mock_tool_block.input = tool_input

        mock_response = MagicMock()
        mock_response.content = [mock_tool_block]
        mock_response.model_dump.return_value = {
            "content": [{"type": "tool_use", "input": tool_input}]
        }

        mock_client = MagicMock()
        mock_client.messages.create = AsyncMock(return_value=mock_response)

        await educator.educate(
            client=mock_client,
            question_title="Test",
            question_prompt="Test.",
            transcript_text="...",
            evaluation_summary="...",
        )

        call_kwargs = mock_client.messages.create.call_args.kwargs
        assert call_kwargs["model"] == "claude-sonnet-4-20250514"
        assert call_kwargs["max_tokens"] == 8000
