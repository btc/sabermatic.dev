"""Tests for backend.llm_parse — JSON extraction from LLM output."""

import pytest

from backend.llm_parse import LLMParseError, parse_llm_json


class TestStripMarkdownFences:
    def test_strips_json_fences(self):
        raw = '```json\n{"key": "value"}\n```'
        assert parse_llm_json(raw) == {"key": "value"}

    def test_strips_plain_fences(self):
        raw = '```\n{"key": "value"}\n```'
        assert parse_llm_json(raw) == {"key": "value"}

    def test_strips_fences_with_whitespace(self):
        raw = '  ```json\n  {"key": "value"}  \n  ```  '
        assert parse_llm_json(raw) == {"key": "value"}


class TestStripPreamble:
    def test_strips_preamble_text(self):
        raw = 'Here is the JSON:\n{"key": "value"}'
        assert parse_llm_json(raw) == {"key": "value"}

    def test_strips_preamble_and_postamble(self):
        raw = 'Sure! Here you go:\n{"key": "value"}\nHope that helps!'
        assert parse_llm_json(raw) == {"key": "value"}

    def test_strips_preamble_with_array(self):
        raw = 'The result is: [1, 2, 3]'
        assert parse_llm_json(raw) == [1, 2, 3]


class TestTrailingComma:
    def test_handles_trailing_comma_in_object(self):
        raw = '{"key": "value",}'
        result = parse_llm_json(raw)
        assert result == {"key": "value"}

    def test_handles_trailing_comma_in_array(self):
        raw = '["a", "b",]'
        result = parse_llm_json(raw)
        assert result == ["a", "b"]

    def test_handles_nested_trailing_commas(self):
        raw = '{"items": ["a", "b",], "count": 2,}'
        result = parse_llm_json(raw)
        assert result == {"items": ["a", "b"], "count": 2}


class TestNestedJson:
    def test_parses_nested_objects(self):
        raw = '```json\n{"outer": {"inner": "value"}}\n```'
        result = parse_llm_json(raw)
        assert result == {"outer": {"inner": "value"}}

    def test_parses_complex_structure(self):
        raw = """Here is the evaluation:
{
    "score": 4,
    "strengths": ["clear", "structured"],
    "gaps": ["scalability"]
}"""
        result = parse_llm_json(raw)
        assert result["score"] == 4
        assert len(result["strengths"]) == 2


class TestErrorHandling:
    def test_raises_on_truly_unparseable(self):
        with pytest.raises(LLMParseError) as exc:
            parse_llm_json("This is not JSON at all")
        assert "This is not JSON at all" in str(exc.value)

    def test_error_contains_raw_text(self):
        raw = "No JSON here, sorry!"
        with pytest.raises(LLMParseError) as exc:
            parse_llm_json(raw)
        assert exc.value.raw_text == raw

    def test_raises_on_empty_string(self):
        with pytest.raises(LLMParseError):
            parse_llm_json("")

    def test_raises_on_whitespace_only(self):
        with pytest.raises(LLMParseError):
            parse_llm_json("   \n\n  ")

    def test_raises_on_incomplete_json(self):
        with pytest.raises(LLMParseError):
            parse_llm_json('{"key": "value"')
