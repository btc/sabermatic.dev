"""Helper to extract JSON from LLM output.

LLM responses often wrap JSON in markdown fences, include preamble text,
or have trailing commas. This module provides robust extraction.
"""

import json
import re
from typing import Any


class LLMParseError(Exception):
    """Raised when JSON cannot be extracted from LLM output."""

    def __init__(self, message: str, raw_text: str):
        super().__init__(message)
        self.raw_text = raw_text


def _strip_markdown_fences(text: str) -> str:
    """Remove markdown code fences (```json ... ``` or ``` ... ```)."""
    pattern = r"```(?:json)?\s*\n?(.*?)\n?\s*```"
    match = re.search(pattern, text, re.DOTALL)
    if match:
        return match.group(1).strip()
    return text


def _fix_trailing_commas(text: str) -> str:
    """Remove trailing commas before closing braces/brackets."""
    # Remove comma followed by optional whitespace then } or ]
    return re.sub(r",\s*([}\]])", r"\1", text)


def _extract_json_substring(text: str) -> str:
    """Find the outermost JSON object or array in the text."""
    # Find the first { or [
    obj_start = text.find("{")
    arr_start = text.find("[")

    if obj_start == -1 and arr_start == -1:
        raise LLMParseError(
            f"No JSON object or array found in text: {text}", raw_text=text
        )

    # Use whichever comes first
    if obj_start == -1:
        start = arr_start
        open_char, close_char = "[", "]"
    elif arr_start == -1:
        start = obj_start
        open_char, close_char = "{", "}"
    elif obj_start < arr_start:
        start = obj_start
        open_char, close_char = "{", "}"
    else:
        start = arr_start
        open_char, close_char = "[", "]"

    # Walk forward to find the matching close, tracking nesting and strings
    depth = 0
    in_string = False
    escape_next = False

    for i in range(start, len(text)):
        ch = text[i]

        if escape_next:
            escape_next = False
            continue

        if ch == "\\":
            if in_string:
                escape_next = True
            continue

        if ch == '"':
            in_string = not in_string
            continue

        if in_string:
            continue

        if ch == open_char:
            depth += 1
        elif ch == close_char:
            depth -= 1
            if depth == 0:
                return text[start : i + 1]

    # If we get here, brackets weren't balanced
    raise LLMParseError(
        f"Unbalanced JSON in text: {text}", raw_text=text
    )


def parse_llm_json(text: str) -> dict[str, Any] | list[Any]:
    """Extract JSON from LLM output that may include markdown fences or preamble.

    Handles:
    - Markdown code fences (```json ... ``` or ``` ... ```)
    - Preamble and postamble text around JSON
    - Trailing commas in objects and arrays

    Args:
        text: Raw LLM output that should contain JSON somewhere.

    Returns:
        Parsed JSON as a dict or list.

    Raises:
        LLMParseError: If no valid JSON can be extracted. The error includes
            the raw text for debugging.
    """
    original_text = text
    text = text.strip()

    if not text:
        raise LLMParseError("Empty input", raw_text=original_text)

    # Step 1: Strip markdown fences
    text = _strip_markdown_fences(text)

    # Step 2: Try direct parse first (fast path)
    try:
        return json.loads(text)
    except json.JSONDecodeError:
        pass

    # Step 3: Try with trailing comma fix
    try:
        return json.loads(_fix_trailing_commas(text))
    except json.JSONDecodeError:
        pass

    # Step 4: Extract JSON substring (handles preamble/postamble)
    try:
        json_str = _extract_json_substring(text)
    except LLMParseError:
        raise LLMParseError(
            f"Could not extract JSON from LLM output: {original_text}",
            raw_text=original_text,
        )

    # Step 5: Try parsing the extracted substring
    try:
        return json.loads(json_str)
    except json.JSONDecodeError:
        pass

    # Step 6: Try with trailing comma fix on the extracted substring
    try:
        return json.loads(_fix_trailing_commas(json_str))
    except json.JSONDecodeError:
        raise LLMParseError(
            f"Could not parse extracted JSON from LLM output: {original_text}",
            raw_text=original_text,
        )
