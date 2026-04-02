"""Evaluator module — reads the full interview transcript and produces structured scores.

The evaluator uses Claude's tool_use feature to produce structured output:
scores per dimension, qualitative feedback (strengths, gaps, advice), and
per-message annotations that reference specific points in the conversation.
"""

from typing import Any, cast

import anthropic
from anthropic.types import MessageParam, ToolParam, ToolChoiceToolParam
from pydantic import BaseModel

from backend.models import EvaluationCreate, Message, MessageRole


# --- Tool Schema ---

EVALUATION_TOOL: dict[str, Any] = {
    "name": "submit_evaluation",
    "description": "Submit the structured evaluation of the interview transcript.",
    "input_schema": {
        "type": "object",
        "required": ["scores", "strengths", "gaps", "advice", "annotations"],
        "properties": {
            "scores": {
                "type": "object",
                "required": [
                    "requirements",
                    "highlevel",
                    "deepdive",
                    "scalability",
                    "communication",
                    "overall",
                ],
                "properties": {
                    "requirements": {
                        "type": "integer",
                        "minimum": 1,
                        "maximum": 5,
                        "description": "Requirements gathering and scoping (1-5)",
                    },
                    "highlevel": {
                        "type": "integer",
                        "minimum": 1,
                        "maximum": 5,
                        "description": "High-level architecture design (1-5)",
                    },
                    "deepdive": {
                        "type": "integer",
                        "minimum": 1,
                        "maximum": 5,
                        "description": "Deep dive on critical components (1-5)",
                    },
                    "scalability": {
                        "type": "integer",
                        "minimum": 1,
                        "maximum": 5,
                        "description": "Scalability, bottlenecks, and trade-offs (1-5)",
                    },
                    "communication": {
                        "type": "integer",
                        "minimum": 1,
                        "maximum": 5,
                        "description": "Communication clarity and structure (1-5)",
                    },
                    "overall": {
                        "type": "integer",
                        "minimum": 1,
                        "maximum": 5,
                        "description": "Overall interview performance (1-5)",
                    },
                },
            },
            "strengths": {
                "type": "array",
                "items": {"type": "string"},
                "minItems": 1,
                "description": "List of specific things the candidate did well.",
            },
            "gaps": {
                "type": "array",
                "items": {"type": "string"},
                "minItems": 1,
                "description": "List of specific areas where the candidate fell short.",
            },
            "advice": {
                "type": "string",
                "description": "Actionable advice for the candidate to improve.",
            },
            "annotations": {
                "type": "array",
                "items": {
                    "type": "object",
                    "required": ["message_sequence", "type", "content"],
                    "properties": {
                        "message_sequence": {
                            "type": "integer",
                            "description": "The sequence number of the message being annotated.",
                        },
                        "type": {
                            "type": "string",
                            "enum": [
                                "strength",
                                "gap",
                                "missed_opportunity",
                                "note",
                            ],
                            "description": "The type of annotation.",
                        },
                        "content": {
                            "type": "string",
                            "description": "The annotation text explaining what was good, bad, or missed.",
                        },
                    },
                },
                "description": "Per-message annotations referencing specific points in the conversation.",
            },
        },
    },
}


# --- Config ---


class EvaluatorConfig(BaseModel):
    """Configuration for the evaluator LLM."""

    model: str = "claude-sonnet-4-20250514"
    max_tokens: int = 4096


# --- Evaluator ---


class Evaluator:
    """Evaluates a completed system design interview transcript.

    Uses Claude's tool_use feature to produce structured evaluation output
    including dimension scores, qualitative feedback, and per-message annotations.
    """

    def __init__(self, config: EvaluatorConfig | None = None) -> None:
        self.config = config or EvaluatorConfig()

    def get_tool_schema(self) -> list[dict[str, Any]]:
        """Return the tool schema list for the evaluation tool.

        Returns:
            A list containing the submit_evaluation tool definition.
        """
        return [EVALUATION_TOOL]

    def build_system_prompt(self) -> str:
        """Build the system prompt containing the full evaluation rubric.

        Returns:
            The complete system prompt string with rubric, calibration
            guidance, and metacognitive feedback instructions.
        """
        return """\
You are an expert system design interview evaluator. You have just observed a complete system design interview. Your job is to evaluate the candidate's performance by reading the full transcript and producing a structured evaluation.

## Evaluation Rubric

Score each dimension from 1 (poor) to 5 (exceptional).

### 1. Requirements Gathering & Scoping (requirements)

- **1 — Poor:** Jumped straight into design without asking any clarifying questions. Made assumptions about scale, features, and constraints without validation.
- **2 — Below Average:** Asked one or two surface-level questions but missed critical dimensions (scale, user types, geographic distribution, latency requirements).
- **3 — Average:** Asked reasonable clarifying questions and established basic scope but missed some important constraints or didn't quantify scale precisely.
- **4 — Good:** Systematically gathered requirements across functional and non-functional dimensions. Quantified scale, identified key constraints, and established clear scope boundaries.
- **5 — Exceptional:** Thorough, structured requirements gathering. Identified edge cases, asked about priorities and trade-offs, quantified all critical metrics, and used requirements to drive the design.

### 2. High-Level Architecture (highlevel)

- **1 — Poor:** No coherent architecture. Random components without clear data flow or relationships.
- **2 — Below Average:** Basic architecture with major gaps. Missing critical components or unclear data flow between them.
- **3 — Average:** Reasonable high-level architecture with the main components identified. Some gaps in data flow or component interaction.
- **4 — Good:** Clear, well-structured architecture with all major components, data flows, and APIs identified. Justified key architectural choices.
- **5 — Exceptional:** Elegant architecture with clear separation of concerns, well-defined interfaces, and explicit trade-off analysis for every major decision.

### 3. Deep Dive (deepdive)

- **1 — Poor:** No depth on any component. Everything stayed at the surface level.
- **2 — Below Average:** Attempted a deep dive but hand-waved critical details (e.g., "we'll use a cache" without eviction policy, TTL, or consistency model).
- **3 — Average:** Reasonable depth on one component but lacked specifics on others. Some hand-waving on implementation details.
- **4 — Good:** Strong depth on at least one critical component with specific implementation details, data structures, and algorithms. Addressed failure modes.
- **5 — Exceptional:** Impressive depth on multiple components. Discussed specific algorithms, data structures, consistency models, failure handling, and operational concerns with precision.

### 4. Scalability & Trade-offs (scalability)

- **1 — Poor:** No discussion of scale, bottlenecks, or trade-offs. Design would not handle stated requirements.
- **2 — Below Average:** Mentioned scalability in passing but didn't identify actual bottlenecks or propose solutions.
- **3 — Average:** Identified some bottlenecks and proposed basic solutions (caching, sharding) but without deep analysis of trade-offs.
- **4 — Good:** Systematically identified bottlenecks, proposed specific solutions with trade-off analysis. Discussed CAP theorem implications and consistency models where relevant.
- **5 — Exceptional:** Comprehensive scalability analysis with back-of-envelope calculations, specific scaling strategies per component, detailed trade-off analysis, and awareness of operational complexity.

### 5. Communication (communication)

- **1 — Poor:** Disorganized, hard to follow. Jumped between topics randomly. Did not respond to interviewer signals.
- **2 — Below Average:** Some structure but frequently went off on tangents. Missed interviewer cues to move on or dig deeper.
- **3 — Average:** Generally clear communication with reasonable structure. Occasionally lost focus or missed interviewer signals.
- **4 — Good:** Well-structured approach. Clearly signposted transitions between topics, responded to interviewer cues, and explained decisions concisely.
- **5 — Exceptional:** Outstanding communication. Drove the interview proactively, structured the approach upfront, signposted every transition, responded perfectly to interviewer cues, and balanced breadth with depth.

## Calibration Guidance

Anchor your scores to real interview performance:

- A score of **3** represents an average candidate who would be a borderline hire at a mid-level position. Most candidates should cluster around 2-4.
- A score of **5** is rare and exceptional — reserve it for truly impressive performance that would stand out among senior/staff engineers.
- A score of **1** means the candidate completely failed this dimension — they would clearly not pass this part of a real interview.
- **Vary your scores.** It is extremely unlikely that a candidate performs identically across all dimensions. Most candidates have strengths and weaknesses. If you find yourself giving the same score for every dimension, reconsider.
- The **overall** score should NOT be a simple average. Weight it by how critical each dimension was for the specific question asked.

## Metacognitive Feedback Instructions

For each annotation you create:
- Reference the specific message by its sequence number
- Be specific about what was said or done (quote if helpful)
- Explain WHY it was good/bad/missed, not just THAT it was
- For missed opportunities, describe what the candidate COULD have said
- Focus annotations on moments that most impacted the evaluation

## Output

You MUST use the submit_evaluation tool to submit your structured evaluation. Do not produce free-text output — use the tool."""

    def build_transcript_text(
        self,
        question_title: str,
        question_prompt: str,
        messages: list[Message],
    ) -> str:
        """Format the interview transcript for the evaluator.

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

    def parse_response(
        self,
        raw: dict[str, Any],
        session_id: int,
        raw_response: dict[str, Any] | None = None,
    ) -> EvaluationCreate:
        """Extract the tool call input and build an EvaluationCreate.

        Args:
            raw: The tool_use input dict from the API response.
            session_id: The session ID to associate with the evaluation.
            raw_response: The full raw API response for storage.

        Returns:
            An EvaluationCreate instance with scores and feedback.

        Raises:
            KeyError: If required fields are missing from the response.
        """
        scores = raw["scores"]

        # Validate all required score fields are present
        for field in [
            "requirements",
            "highlevel",
            "deepdive",
            "scalability",
            "communication",
            "overall",
        ]:
            if field not in scores:
                raise KeyError(field)

        # Validate top-level required fields
        for field in ["strengths", "gaps", "advice", "annotations"]:
            if field not in raw:
                raise KeyError(field)

        return EvaluationCreate(
            session_id=session_id,
            score_requirements=scores["requirements"],
            score_highlevel=scores["highlevel"],
            score_deepdive=scores["deepdive"],
            score_scalability=scores["scalability"],
            score_communication=scores["communication"],
            score_overall=scores["overall"],
            strengths=raw["strengths"],
            gaps=raw["gaps"],
            advice=raw["advice"],
            raw_response=raw_response or {},
        )

    def validate_semantics(self, eval_create: EvaluationCreate) -> list[str]:
        """Validate the evaluation for degenerate or suspicious patterns.

        Args:
            eval_create: The parsed evaluation to validate.

        Returns:
            A list of warning strings. Empty list means no issues found.
        """
        warnings: list[str] = []

        # Check for degenerate (all identical) scores
        scores = [
            eval_create.score_requirements,
            eval_create.score_highlevel,
            eval_create.score_deepdive,
            eval_create.score_scalability,
            eval_create.score_communication,
            eval_create.score_overall,
        ]
        if len(set(scores)) == 1:
            warnings.append(
                f"All scores are identical ({scores[0]}). "
                "It is unlikely a candidate performs the same across all dimensions. "
                "Consider re-evaluating."
            )

        # Check for empty strengths
        if not eval_create.strengths:
            warnings.append(
                "Strengths list is empty. Every candidate does something well — "
                "identify at least one strength."
            )

        # Check for empty gaps
        if not eval_create.gaps:
            warnings.append(
                "Gaps list is empty. Every candidate has areas for improvement — "
                "identify at least one gap."
            )

        # Check for short advice
        if len(eval_create.advice) < 20:
            warnings.append(
                "Advice is very short. Provide actionable, specific advice "
                "the candidate can use to improve."
            )

        return warnings

    async def evaluate(
        self,
        client: anthropic.AsyncAnthropic,
        question_title: str,
        question_prompt: str,
        messages: list[Message],
        session_id: int,
    ) -> tuple[EvaluationCreate, list[dict[str, Any]]]:
        """Run the full evaluation pipeline.

        Builds the transcript, calls the Anthropic API with the evaluation
        tool, parses the response, and validates semantics.

        Args:
            client: An Anthropic client instance.
            question_title: The title of the design question.
            question_prompt: The full interview-style question text.
            messages: List of Message objects from the interview.
            session_id: The session ID to associate with the evaluation.

        Returns:
            A tuple of (EvaluationCreate, list of annotation dicts).
        """
        system_prompt = self.build_system_prompt()
        transcript_text = self.build_transcript_text(
            question_title=question_title,
            question_prompt=question_prompt,
            messages=messages,
        )

        response = await client.messages.create(
            model=self.config.model,
            max_tokens=self.config.max_tokens,
            system=system_prompt,
            messages=cast(list[MessageParam], [{"role": "user", "content": transcript_text}]),
            tools=cast(list[ToolParam], self.get_tool_schema()),
            tool_choice=cast(ToolChoiceToolParam, {"type": "tool", "name": "submit_evaluation"}),
        )

        # Extract tool call input from the response
        # tool_choice forces a ToolUseBlock; cast since the union includes TextBlock etc.
        tool_input: dict[str, Any] = response.content[0].input  # type: ignore[union-attr]
        raw_response: dict[str, Any] = response.model_dump()

        eval_create = self.parse_response(
            raw=tool_input,
            session_id=session_id,
            raw_response=raw_response,
        )

        # Extract annotations from the tool input
        annotations = tool_input.get("annotations", [])

        return eval_create, annotations
