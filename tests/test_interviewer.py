"""Tests for backend.interviewer — LLM interviewer module."""

from datetime import datetime
from unittest.mock import AsyncMock, MagicMock

import pytest

from backend.interviewer import Interviewer, InterviewerConfig
from backend.models import Message, MessageRole


class TestInterviewerConfig:
    def test_default_config(self):
        config = InterviewerConfig()
        assert config.model is not None
        assert config.max_tokens > 0

    def test_custom_config(self):
        config = InterviewerConfig(model="claude-sonnet-4-20250514", max_tokens=512)
        assert config.model == "claude-sonnet-4-20250514"
        assert config.max_tokens == 512


class TestBuildSystemPrompt:
    def test_system_prompt_includes_elapsed_time(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Rate Limiter",
            question_prompt="Design a rate limiting service.",
            timer_sec=2700,
            elapsed_sec=1200,
        )
        assert "1200" in prompt or "20 minutes" in prompt

    def test_system_prompt_includes_elapsed_seconds_explicitly(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Rate Limiter",
            question_prompt="Design a rate limiting service.",
            timer_sec=2700,
            elapsed_sec=1200,
        )
        # elapsed_sec must actually be interpolated, not just accepted as param
        assert "1200" in prompt

    def test_system_prompt_includes_remaining_time(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Rate Limiter",
            question_prompt="Design a rate limiting service.",
            timer_sec=2700,
            elapsed_sec=1200,
        )
        # 2700 - 1200 = 1500 seconds = 25 minutes remaining
        assert "25" in prompt

    def test_system_prompt_without_briefing_has_no_briefing_section(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="URL Shortener",
            question_prompt="Design a URL shortener.",
            timer_sec=2700,
            elapsed_sec=0,
            briefing=None,
        )
        assert "Candidate Briefing" not in prompt

    def test_system_prompt_with_briefing_includes_it(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="URL Shortener",
            question_prompt="...",
            timer_sec=2700,
            elapsed_sec=0,
            briefing="Weak on cache invalidation",
        )
        assert "Candidate Briefing" in prompt
        assert "cache invalidation" in prompt

    def test_system_prompt_includes_question_title(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Message Queue",
            question_prompt="Design a distributed message queue.",
            timer_sec=2700,
            elapsed_sec=0,
        )
        assert "Message Queue" in prompt

    def test_system_prompt_includes_question_prompt(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Message Queue",
            question_prompt="Design a distributed message queue.",
            timer_sec=2700,
            elapsed_sec=0,
        )
        assert "Design a distributed message queue." in prompt

    def test_system_prompt_contains_key_behavioral_instructions(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Rate Limiter",
            question_prompt="Design a rate limiting service.",
            timer_sec=2700,
            elapsed_sec=0,
        )
        # Check for key behavioral instructions from the spec
        prompt_lower = prompt.lower()
        assert "never" in prompt_lower  # never validate, never break character
        assert "short" in prompt_lower or "sentence" in prompt_lower  # keep responses short
        assert "silent" in prompt_lower or "driving" in prompt_lower  # stay silent when candidate drives

    def test_system_prompt_at_zero_elapsed(self):
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Cache",
            question_prompt="Design a cache.",
            timer_sec=2700,
            elapsed_sec=0,
        )
        # 0 seconds elapsed
        assert "0" in prompt
        # 45 minutes remaining
        assert "45" in prompt

    def test_system_prompt_time_phases(self):
        """Prompt should include phase-based instructions (first half, halfway, last 5 min)."""
        interviewer = Interviewer(config=InterviewerConfig())
        prompt = interviewer.build_system_prompt(
            question_title="Cache",
            question_prompt="Design a cache.",
            timer_sec=2700,
            elapsed_sec=600,
        )
        prompt_lower = prompt.lower()
        # Should mention time-phase concepts
        assert "half" in prompt_lower or "halfway" in prompt_lower
        assert "wrap" in prompt_lower or "last" in prompt_lower or "final" in prompt_lower


class TestBuildMessages:
    @pytest.fixture
    def interviewer(self):
        return Interviewer(config=InterviewerConfig())

    def test_build_messages_with_empty_history(self, interviewer):
        messages = interviewer.build_messages([])
        assert messages == []

    def test_build_messages_maps_roles_correctly(self, interviewer):
        """Interviewer messages -> 'assistant' role, Candidate messages -> 'user' role."""
        history = [
            Message(
                id=1, session_id=1, sequence=1, role=MessageRole.interviewer,
                content="Welcome to the interview.",
                timestamp=datetime(2025, 1, 1),
            ),
            Message(
                id=2, session_id=1, sequence=2, role=MessageRole.candidate,
                content="Thanks! I'd like to start with requirements.",
                timestamp=datetime(2025, 1, 1),
            ),
            Message(
                id=3, session_id=1, sequence=3, role=MessageRole.interviewer,
                content="Go ahead.",
                timestamp=datetime(2025, 1, 1),
            ),
        ]
        messages = interviewer.build_messages(history)

        assert len(messages) == 3
        assert messages[0] == {"role": "assistant", "content": "Welcome to the interview."}
        assert messages[1] == {"role": "user", "content": "Thanks! I'd like to start with requirements."}
        assert messages[2] == {"role": "assistant", "content": "Go ahead."}

    def test_build_messages_single_candidate_message(self, interviewer):
        history = [
            Message(
                id=1, session_id=1, sequence=1, role=MessageRole.candidate,
                content="I want to discuss the data model.",
                timestamp=datetime(2025, 1, 1),
            ),
        ]
        messages = interviewer.build_messages(history)
        assert messages == [{"role": "user", "content": "I want to discuss the data model."}]

    def test_build_messages_single_interviewer_message(self, interviewer):
        history = [
            Message(
                id=1, session_id=1, sequence=1, role=MessageRole.interviewer,
                content="Let's begin.",
                timestamp=datetime(2025, 1, 1),
            ),
        ]
        messages = interviewer.build_messages(history)
        assert messages == [{"role": "assistant", "content": "Let's begin."}]

    def test_build_messages_preserves_order(self, interviewer):
        history = [
            Message(
                id=1, session_id=1, sequence=1, role=MessageRole.candidate,
                content="First",
                timestamp=datetime(2025, 1, 1),
            ),
            Message(
                id=2, session_id=1, sequence=2, role=MessageRole.candidate,
                content="Second",
                timestamp=datetime(2025, 1, 1),
            ),
        ]
        messages = interviewer.build_messages(history)
        assert messages[0]["content"] == "First"
        assert messages[1]["content"] == "Second"


class TestGetResponseStream:
    @pytest.fixture
    def interviewer(self):
        return Interviewer(config=InterviewerConfig())

    async def test_get_response_stream_yields_tokens(self, interviewer):
        """get_response_stream should yield text tokens from the stream."""
        mock_stream_cm = AsyncMock()

        # Build a mock that works as an async context manager producing text events
        async def mock_text_iter():
            for token in ["Hello", " ", "world"]:
                yield token

        mock_stream = MagicMock()
        mock_stream.__aenter__ = AsyncMock(return_value=mock_stream)
        mock_stream.__aexit__ = AsyncMock(return_value=False)
        mock_stream.text_stream = mock_text_iter()

        mock_client = MagicMock()
        mock_client.messages = MagicMock()
        mock_client.messages.stream = MagicMock(return_value=mock_stream)

        tokens = []
        async for token in interviewer.get_response_stream(
            client=mock_client,
            system_prompt="You are an interviewer.",
            messages=[{"role": "user", "content": "Hi"}],
        ):
            tokens.append(token)

        assert tokens == ["Hello", " ", "world"]

    async def test_get_response_stream_passes_system_and_messages(self, interviewer):
        """Verify system prompt and messages are passed to the API."""
        async def mock_text_iter():
            yield "ok"

        mock_stream = MagicMock()
        mock_stream.__aenter__ = AsyncMock(return_value=mock_stream)
        mock_stream.__aexit__ = AsyncMock(return_value=False)
        mock_stream.text_stream = mock_text_iter()

        mock_client = MagicMock()
        mock_client.messages = MagicMock()
        mock_client.messages.stream = MagicMock(return_value=mock_stream)

        system_prompt = "You are an interviewer."
        messages = [{"role": "user", "content": "Hi"}]

        async for _ in interviewer.get_response_stream(
            client=mock_client,
            system_prompt=system_prompt,
            messages=messages,
        ):
            pass

        call_kwargs = mock_client.messages.stream.call_args.kwargs
        assert call_kwargs["system"] == system_prompt
        assert call_kwargs["messages"] == messages
        assert call_kwargs["model"] == interviewer.config.model
        assert call_kwargs["max_tokens"] == interviewer.config.max_tokens


class TestGetOpening:
    @pytest.fixture
    def interviewer(self):
        return Interviewer(config=InterviewerConfig())

    async def test_get_opening_yields_tokens(self, interviewer):
        """get_opening should stream the first interviewer message with no history."""
        async def mock_text_iter():
            for token in ["Welcome", " to", " the", " interview."]:
                yield token

        mock_stream = MagicMock()
        mock_stream.__aenter__ = AsyncMock(return_value=mock_stream)
        mock_stream.__aexit__ = AsyncMock(return_value=False)
        mock_stream.text_stream = mock_text_iter()

        mock_client = MagicMock()
        mock_client.messages = MagicMock()
        mock_client.messages.stream = MagicMock(return_value=mock_stream)

        tokens = []
        async for token in interviewer.get_opening(
            client=mock_client,
            system_prompt="You are an interviewer.",
        ):
            tokens.append(token)

        assert tokens == ["Welcome", " to", " the", " interview."]

    async def test_get_opening_passes_empty_user_message(self, interviewer):
        """get_opening should pass a single user message to start the conversation."""
        async def mock_text_iter():
            yield "ok"

        mock_stream = MagicMock()
        mock_stream.__aenter__ = AsyncMock(return_value=mock_stream)
        mock_stream.__aexit__ = AsyncMock(return_value=False)
        mock_stream.text_stream = mock_text_iter()

        mock_client = MagicMock()
        mock_client.messages = MagicMock()
        mock_client.messages.stream = MagicMock(return_value=mock_stream)

        async for _ in interviewer.get_opening(
            client=mock_client,
            system_prompt="You are an interviewer.",
        ):
            pass

        call_kwargs = mock_client.messages.stream.call_args.kwargs
        # Must include a user message (Anthropic API requires alternating roles starting with user)
        assert len(call_kwargs["messages"]) == 1
        assert call_kwargs["messages"][0]["role"] == "user"
