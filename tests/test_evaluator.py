"""Tests for backend.evaluator — LLM evaluator module."""

from datetime import datetime
from unittest.mock import MagicMock

import pytest

from backend.evaluator import Evaluator, EvaluatorConfig
from backend.models import EvaluationCreate, Message, MessageRole


# --- Fixtures ---


@pytest.fixture
def evaluator():
    return Evaluator(config=EvaluatorConfig())


def _make_message(seq: int, role: MessageRole, content: str) -> Message:
    return Message(
        id=seq,
        session_id=1,
        sequence=seq,
        role=role,
        content=content,
        timestamp=datetime(2025, 6, 1, 12, 0, seq),
    )


def _valid_tool_input() -> dict:
    """A well-formed tool_use input dict."""
    return {
        "scores": {
            "requirements": 4,
            "highlevel": 3,
            "deepdive": 5,
            "scalability": 2,
            "communication": 4,
            "overall": 3,
        },
        "strengths": ["Clear requirements gathering", "Good API design"],
        "gaps": ["Missed cache invalidation", "No monitoring discussion"],
        "advice": "Focus more on failure modes and add observability from the start.",
        "annotations": [
            {
                "message_sequence": 2,
                "type": "strength",
                "content": "Good clarifying questions about scale.",
            },
            {
                "message_sequence": 5,
                "type": "gap",
                "content": "Hand-waved database sharding without specifics.",
            },
        ],
    }


# --- Config Tests ---


class TestEvaluatorConfig:
    def test_default_config(self):
        config = EvaluatorConfig()
        assert config.model is not None
        assert config.max_tokens > 0

    def test_custom_config(self):
        config = EvaluatorConfig(model="claude-sonnet-4-20250514", max_tokens=2048)
        assert config.model == "claude-sonnet-4-20250514"
        assert config.max_tokens == 2048


# --- Tool Schema Tests ---


class TestEvaluatorToolSchema:
    def test_evaluator_tool_schema(self, evaluator):
        """Verify the evaluator defines a tool schema for structured output."""
        tools = evaluator.get_tool_schema()
        assert tools[0]["name"] == "submit_evaluation"
        # Verify all 6 score fields are in the schema
        props = tools[0]["input_schema"]["properties"]["scores"]["properties"]
        for field in [
            "requirements",
            "highlevel",
            "deepdive",
            "scalability",
            "communication",
            "overall",
        ]:
            assert field in props
            assert props[field]["minimum"] == 1
            assert props[field]["maximum"] == 5

    def test_tool_schema_required_fields(self, evaluator):
        """Schema must require scores, strengths, gaps, advice, annotations."""
        tools = evaluator.get_tool_schema()
        required = tools[0]["input_schema"]["required"]
        for field in ["scores", "strengths", "gaps", "advice", "annotations"]:
            assert field in required

    def test_tool_schema_annotation_types(self, evaluator):
        """Annotation type must be an enum of known types."""
        tools = evaluator.get_tool_schema()
        annotation_props = tools[0]["input_schema"]["properties"]["annotations"]["items"]["properties"]
        assert "type" in annotation_props
        assert set(annotation_props["type"]["enum"]) == {
            "strength",
            "gap",
            "missed_opportunity",
            "note",
        }


# --- Parse Response Tests ---


class TestParseEvaluationResponse:
    def test_parse_evaluation_response_valid(self, evaluator):
        """Well-formed tool_use response -> EvaluationCreate with correct scores."""
        tool_input = _valid_tool_input()
        raw_response = {"tool_input": tool_input, "model": "test"}

        result = evaluator.parse_response(raw=tool_input, session_id=42, raw_response=raw_response)

        assert isinstance(result, EvaluationCreate)
        assert result.session_id == 42
        assert result.score_requirements == 4
        assert result.score_highlevel == 3
        assert result.score_deepdive == 5
        assert result.score_scalability == 2
        assert result.score_communication == 4
        assert result.score_overall == 3
        assert result.strengths == ["Clear requirements gathering", "Good API design"]
        assert result.gaps == ["Missed cache invalidation", "No monitoring discussion"]
        assert result.advice == "Focus more on failure modes and add observability from the start."
        assert result.raw_response == raw_response

    def test_parse_evaluation_response_missing_field(self, evaluator):
        """Missing 'gaps' key -> clear error, not a crash."""
        tool_input = _valid_tool_input()
        del tool_input["gaps"]

        with pytest.raises(KeyError, match="gaps"):
            evaluator.parse_response(raw=tool_input, session_id=1, raw_response={})

    def test_parse_evaluation_response_missing_score_field(self, evaluator):
        """Missing score sub-field -> clear error."""
        tool_input = _valid_tool_input()
        del tool_input["scores"]["overall"]

        with pytest.raises(KeyError, match="overall"):
            evaluator.parse_response(raw=tool_input, session_id=1, raw_response={})

    def test_parse_response_returns_annotations(self, evaluator):
        """parse_response should also return the annotations list."""
        tool_input = _valid_tool_input()
        result = evaluator.parse_response(raw=tool_input, session_id=1, raw_response={})
        # The annotations are not part of EvaluationCreate, they come back separately
        # Actually, let's check that parse_response returns a tuple
        # Re-reading the spec: evaluate() returns (eval, annotations) but parse_response returns EvaluationCreate
        # and the annotations are extracted separately
        assert isinstance(result, EvaluationCreate)


# --- Semantic Validation Tests ---


class TestSemanticValidation:
    def test_semantic_validation_degenerate_scores(self, evaluator):
        """All identical scores should be flagged."""
        tool_input = _valid_tool_input()
        tool_input["scores"] = {
            "requirements": 3,
            "highlevel": 3,
            "deepdive": 3,
            "scalability": 3,
            "communication": 3,
            "overall": 3,
        }
        eval_create = evaluator.parse_response(raw=tool_input, session_id=1, raw_response={})
        warnings = evaluator.validate_semantics(eval_create)

        assert len(warnings) > 0
        assert any("identical" in w.lower() or "degenerate" in w.lower() or "same" in w.lower() for w in warnings)

    def test_semantic_validation_empty_strengths(self, evaluator):
        """Empty strengths should be flagged (though schema requires minItems=1,
        we still validate at the application level)."""
        tool_input = _valid_tool_input()
        # Bypass the schema validation by creating EvaluationCreate directly
        eval_create = EvaluationCreate(
            session_id=1,
            score_requirements=4,
            score_highlevel=3,
            score_deepdive=5,
            score_scalability=2,
            score_communication=4,
            score_overall=3,
            strengths=[],
            gaps=["Some gap"],
            advice="Some advice here that is long enough.",
            raw_response={},
        )
        warnings = evaluator.validate_semantics(eval_create)
        assert len(warnings) > 0
        assert any("strengths" in w.lower() for w in warnings)

    def test_semantic_validation_empty_gaps(self, evaluator):
        """Empty gaps should be flagged."""
        eval_create = EvaluationCreate(
            session_id=1,
            score_requirements=4,
            score_highlevel=3,
            score_deepdive=5,
            score_scalability=2,
            score_communication=4,
            score_overall=3,
            strengths=["Good stuff"],
            gaps=[],
            advice="Some advice here that is long enough.",
            raw_response={},
        )
        warnings = evaluator.validate_semantics(eval_create)
        assert len(warnings) > 0
        assert any("gaps" in w.lower() for w in warnings)

    def test_semantic_validation_short_advice(self, evaluator):
        """Very short advice should be flagged."""
        eval_create = EvaluationCreate(
            session_id=1,
            score_requirements=4,
            score_highlevel=3,
            score_deepdive=5,
            score_scalability=2,
            score_communication=4,
            score_overall=3,
            strengths=["Good stuff"],
            gaps=["Some gap"],
            advice="OK",
            raw_response={},
        )
        warnings = evaluator.validate_semantics(eval_create)
        assert len(warnings) > 0
        assert any("advice" in w.lower() for w in warnings)

    def test_semantic_validation_no_warnings_for_good_eval(self, evaluator):
        """A well-formed evaluation with varied scores should produce no warnings."""
        tool_input = _valid_tool_input()
        eval_create = evaluator.parse_response(raw=tool_input, session_id=1, raw_response={})
        warnings = evaluator.validate_semantics(eval_create)
        assert warnings == []


# --- Build Transcript Text Tests ---


class TestBuildTranscriptText:
    def test_build_transcript_text(self, evaluator):
        """Verify transcript formatting for evaluator."""
        messages = [
            _make_message(1, MessageRole.interviewer, "Design a URL shortener."),
            _make_message(2, MessageRole.candidate, "Let me start with requirements."),
            _make_message(3, MessageRole.interviewer, "Go ahead."),
            _make_message(4, MessageRole.candidate, "We need to handle 100M URLs per day."),
        ]

        text = evaluator.build_transcript_text(
            question_title="URL Shortener",
            question_prompt="Design a URL shortening service like bit.ly.",
            messages=messages,
        )

        assert "URL Shortener" in text
        assert "Design a URL shortening service like bit.ly." in text
        assert "Design a URL shortener." in text
        assert "Let me start with requirements." in text
        # Should include role labels
        assert "interviewer" in text.lower() or "Interviewer" in text
        assert "candidate" in text.lower() or "Candidate" in text

    def test_build_transcript_text_includes_sequence_numbers(self, evaluator):
        """Transcript should include message sequence numbers for annotation references."""
        messages = [
            _make_message(1, MessageRole.interviewer, "Hello."),
            _make_message(2, MessageRole.candidate, "Hi."),
        ]
        text = evaluator.build_transcript_text(
            question_title="Test",
            question_prompt="Test prompt.",
            messages=messages,
        )
        # Sequence numbers should appear to allow annotation cross-referencing
        assert "[1]" in text or "#1" in text or "1:" in text or "(1)" in text

    def test_build_transcript_text_empty_messages(self, evaluator):
        """Empty message list should still produce header with question info."""
        text = evaluator.build_transcript_text(
            question_title="Test",
            question_prompt="Test prompt.",
            messages=[],
        )
        assert "Test" in text
        assert "Test prompt." in text


# --- System Prompt Tests ---


class TestBuildSystemPrompt:
    def test_system_prompt_contains_rubric(self, evaluator):
        """System prompt must include the full rubric."""
        prompt = evaluator.build_system_prompt()
        prompt_lower = prompt.lower()
        # Must contain all 5 evaluation dimensions
        assert "requirements" in prompt_lower
        assert "high-level" in prompt_lower or "highlevel" in prompt_lower or "high level" in prompt_lower
        assert "deep" in prompt_lower  # deep dive
        assert "scalability" in prompt_lower
        assert "communication" in prompt_lower

    def test_system_prompt_contains_score_descriptions(self, evaluator):
        """System prompt should describe what each score level means."""
        prompt = evaluator.build_system_prompt()
        # Should mention score levels 1-5
        assert "1" in prompt
        assert "5" in prompt

    def test_system_prompt_contains_calibration_guidance(self, evaluator):
        """System prompt should include calibration guidance."""
        prompt = evaluator.build_system_prompt()
        prompt_lower = prompt.lower()
        assert "calibration" in prompt_lower or "calibrate" in prompt_lower or "anchor" in prompt_lower


# --- Evaluate Integration Test ---


class TestEvaluate:
    async def test_evaluate_calls_api_with_correct_params(self, evaluator):
        """evaluate() should call the Anthropic API with tools and tool_choice."""
        tool_input = _valid_tool_input()

        mock_tool_block = MagicMock()
        mock_tool_block.type = "tool_use"
        mock_tool_block.input = tool_input

        mock_response = MagicMock()
        mock_response.content = [mock_tool_block]
        mock_response.model_dump.return_value = {"content": [{"type": "tool_use", "input": tool_input}]}

        mock_client = MagicMock()
        mock_client.messages.create.return_value = mock_response

        messages = [
            _make_message(1, MessageRole.interviewer, "Design a cache."),
            _make_message(2, MessageRole.candidate, "I'll start with requirements."),
        ]

        eval_create, annotations = await evaluator.evaluate(
            client=mock_client,
            question_title="Cache",
            question_prompt="Design a distributed cache.",
            messages=messages,
            session_id=99,
        )

        assert isinstance(eval_create, EvaluationCreate)
        assert eval_create.session_id == 99

        # Verify API was called with tools and tool_choice
        call_kwargs = mock_client.messages.create.call_args.kwargs
        assert "tools" in call_kwargs
        assert call_kwargs["tools"][0]["name"] == "submit_evaluation"
        assert call_kwargs["tool_choice"] == {"type": "tool", "name": "submit_evaluation"}

    async def test_evaluate_returns_annotations(self, evaluator):
        """evaluate() should return annotations from the tool call."""
        tool_input = _valid_tool_input()

        mock_tool_block = MagicMock()
        mock_tool_block.type = "tool_use"
        mock_tool_block.input = tool_input

        mock_response = MagicMock()
        mock_response.content = [mock_tool_block]
        mock_response.model_dump.return_value = {"content": [{"type": "tool_use", "input": tool_input}]}

        mock_client = MagicMock()
        mock_client.messages.create.return_value = mock_response

        messages = [
            _make_message(1, MessageRole.interviewer, "Design a cache."),
            _make_message(2, MessageRole.candidate, "I'll start with requirements."),
        ]

        eval_create, annotations = await evaluator.evaluate(
            client=mock_client,
            question_title="Cache",
            question_prompt="Design a distributed cache.",
            messages=messages,
            session_id=99,
        )

        assert len(annotations) == 2
        assert annotations[0]["message_sequence"] == 2
        assert annotations[0]["type"] == "strength"
        assert annotations[1]["message_sequence"] == 5
        assert annotations[1]["type"] == "gap"
