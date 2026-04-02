"""Educator module — produces deep technical educational content after an evaluation.

The educator uses Claude's tool_use feature to produce structured output:
a model answer tailored to the specific question and deep-dives on each
knowledge gap identified by the evaluator.
"""

from typing import Any, cast

import anthropic
from anthropic.types import MessageParam, ToolParam, ToolChoiceToolParam
from pydantic import BaseModel

from backend.models import Message, MessageRole


# --- Tool Schema ---

EDUCATOR_TOOL: dict[str, Any] = {
    "name": "submit_education",
    "description": "Submit the educational analysis with model answer and gap deep-dives.",
    "input_schema": {
        "type": "object",
        "required": ["model_answer", "gap_deepdives"],
        "properties": {
            "model_answer": {
                "type": "string",
                "description": "Markdown: what a strong answer to this specific problem looks like",
            },
            "gap_deepdives": {
                "type": "string",
                "description": "Markdown: detailed technical explanation for each gap identified by the evaluator",
            },
        },
    },
}


# --- Config ---


class EducatorConfig(BaseModel):
    """Configuration for the educator LLM."""

    model: str = "claude-opus-4-6"
    max_tokens: int = 8000


# --- Educator ---


class Educator:
    """Produces deep technical educational content after an evaluation.

    Uses Claude's tool_use feature to produce structured output with a
    model answer and gap deep-dives tailored to the specific interview.
    """

    def __init__(self, config: EducatorConfig | None = None) -> None:
        self.config = config or EducatorConfig()

    def get_tool_schema(self) -> list[dict[str, Any]]:
        """Return the tool schema list for the education tool.

        Returns:
            A list containing the submit_education tool definition.
        """
        return [EDUCATOR_TOOL]

    def build_prompt(
        self,
        question_title: str,
        question_prompt: str,
        transcript_text: str,
        evaluation_summary: str,
    ) -> str:
        """Build the educator system prompt.

        Args:
            question_title: The title of the design question.
            question_prompt: The full interview-style question text.
            transcript_text: The formatted interview transcript.
            evaluation_summary: Summary of the evaluator's assessment.

        Returns:
            The complete system prompt string with teaching instructions.
        """
        return f"""\
You are an expert system design educator with deep production experience at top tech companies. You have designed and operated large-scale distributed systems.

You are given:
1. A system design interview question
2. The full interview transcript
3. The evaluator's assessment (scores, gaps, strengths)

Your job is to TEACH, not judge. The evaluator already judged. You provide the knowledge the candidate needs.

## Model Answer

Write what a strong answer to THIS SPECIFIC problem looks like. Not a generic textbook answer — a concrete, production-aware design tailored to the exact question as framed in the interview.

Include:
- Concrete architecture with specific technology choices and WHY each was chosen
- Data model with actual schemas, key structures, and access patterns
- Key algorithms, protocols, or techniques with enough detail to implement
- Explicit tradeoffs: what you're giving up and what you're gaining
- What separates a good answer from an exceptional one at each phase

Write as if explaining to a strong engineer who needs to build this. Be specific enough that they could start implementing.

## Gap Deep-Dives

For each gap identified by the evaluator, provide a detailed technical education:

- What the candidate should have known, explained clearly
- How this works in practice at real companies (name companies and systems where relevant)
- Concrete implementation details — not "go research cache invalidation" but "here are the three main approaches: write-through (used by DynamoDB), write-behind (used by most ORMs with batch flush), and TTL-based expiration (Redis default). For this problem, write-through is best because..."
- Code snippets, schema examples, or algorithm pseudocode where helpful
- Common mistakes and how to avoid them

Be the senior engineer who sits down with the candidate after the interview and says "here's what you need to know."

Format everything in Markdown. Use headers, code blocks, and tables where they aid clarity.

## Interview Context

**Question:** {question_title}

{question_prompt}

## Evaluator Assessment

{evaluation_summary}

## Interview Transcript

{transcript_text}"""

    def build_transcript_text(
        self,
        question_title: str,
        question_prompt: str,
        messages: list[Message],
    ) -> str:
        """Format the interview transcript for the educator.

        Args:
            question_title: The title of the design question.
            question_prompt: The full interview-style question text.
            messages: List of Message objects from the interview.

        Returns:
            Formatted transcript string with question context and
            numbered messages with role labels.
        """
        lines = [
            f"# Interview Transcript: {question_title}",
            "",
            "## Question",
            "",
            question_prompt,
            "",
            "## Transcript",
            "",
        ]

        role_labels = {
            MessageRole.interviewer: "Interviewer",
            MessageRole.candidate: "Candidate",
        }

        for msg in messages:
            label = role_labels.get(msg.role, msg.role.value)
            lines.append(f"[{msg.sequence}] {label}: {msg.content}")
            lines.append("")

        return "\n".join(lines)

    async def educate(
        self,
        client: anthropic.AsyncAnthropic,
        question_title: str,
        question_prompt: str,
        transcript_text: str,
        evaluation_summary: str,
    ) -> tuple[str, str, dict[str, Any]]:
        """Run educator analysis.

        Builds the prompt, calls the Anthropic API with the education tool,
        and extracts the model answer and gap deep-dives.

        Args:
            client: An Anthropic client instance.
            question_title: The title of the design question.
            question_prompt: The full interview-style question text.
            transcript_text: The formatted interview transcript.
            evaluation_summary: Summary of the evaluator's assessment.

        Returns:
            A tuple of (model_answer_md, gap_deepdives_md, raw_response).
        """
        system_prompt = self.build_prompt(
            question_title=question_title,
            question_prompt=question_prompt,
            transcript_text=transcript_text,
            evaluation_summary=evaluation_summary,
        )

        response = await client.messages.create(
            model=self.config.model,
            max_tokens=self.config.max_tokens,
            system=system_prompt,
            messages=cast(list[MessageParam], [{"role": "user", "content": "Please analyze the interview and provide your educational content using the submit_education tool."}]),
            tools=cast(list[ToolParam], self.get_tool_schema()),
            tool_choice=cast(ToolChoiceToolParam, {"type": "tool", "name": "submit_education"}),
        )

        # Extract tool call input from the response
        # tool_choice forces a ToolUseBlock; cast since the union includes TextBlock etc.
        tool_input: dict[str, Any] = response.content[0].input  # type: ignore[union-attr]
        raw_response: dict[str, Any] = response.model_dump()

        model_answer: str = tool_input["model_answer"]
        gap_deepdives: str = tool_input["gap_deepdives"]

        return model_answer, gap_deepdives, raw_response
