"""Coach module — reads historical session data and produces strategic guidance.

The coach analyzes all past interview sessions to provide dimension analysis,
topic coverage assessment, thinking pattern recognition, scenario generation,
and metacognitive coaching. Uses Claude's tool_use feature for structured output.
"""

from typing import Any

from pydantic import BaseModel

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


# --- Tool Schema ---

COACH_TOOL: dict[str, Any] = {
    "name": "submit_analysis",
    "description": "Submit the strategic coaching analysis based on the candidate's interview history.",
    "input_schema": {
        "type": "object",
        "required": ["recommendation", "gap_analysis"],
        "properties": {
            "recommendation": {
                "type": "string",
                "description": "Strategic recommendation for what the candidate should work on next.",
            },
            "gap_analysis": {
                "type": "object",
                "properties": {
                    "weakest_dimension": {
                        "type": "string",
                        "description": "The evaluation dimension where the candidate scores lowest.",
                    },
                    "improving_dimensions": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Dimensions showing improvement over time.",
                    },
                    "topic_gaps": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "System design topics the candidate hasn't practiced yet.",
                    },
                    "thinking_patterns": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Recurring patterns in the candidate's thinking (both positive and negative).",
                    },
                },
            },
            "generated_question": {
                "type": ["object", "null"],
                "description": "An optional custom question targeting the candidate's weaknesses.",
                "properties": {
                    "title": {
                        "type": "string",
                        "description": "Short title for the generated question.",
                    },
                    "prompt": {
                        "type": "string",
                        "description": "Full interview-style prompt for the question.",
                    },
                    "difficulty": {
                        "type": "string",
                        "enum": ["medium", "hard"],
                        "description": "Difficulty level of the generated question.",
                    },
                    "tags": {
                        "type": "array",
                        "items": {"type": "string"},
                        "description": "Topic tags for the generated question.",
                    },
                },
            },
        },
    },
}


# --- Config ---


class CoachConfig(BaseModel):
    """Configuration for the coach LLM."""

    model: str = "claude-sonnet-4-20250514"
    max_tokens: int = 4096


# --- Coach ---


class Coach:
    """Analyzes interview history and produces strategic coaching guidance.

    Reads all historical session data to provide dimension analysis,
    topic coverage, thinking pattern recognition, scenario generation,
    and metacognitive coaching.
    """

    def __init__(self, config: CoachConfig | None = None) -> None:
        self.config = config or CoachConfig()

    def get_tool_schema(self) -> list[dict[str, Any]]:
        """Return the tool schema list for the coaching analysis tool.

        Returns:
            A list containing the submit_analysis tool definition.
        """
        return [COACH_TOOL]

    def build_system_prompt(self) -> str:
        """Build the system prompt with coaching instructions.

        Returns:
            The complete system prompt string with coaching responsibilities
            covering dimension analysis, topic coverage, thinking patterns,
            scenario generation, progressive difficulty, and metacognitive coaching.
        """
        return """\
You are an expert system design interview coach. You have access to the candidate's full interview history — past sessions, evaluations, scores, strengths, gaps, and advice. Your job is to analyze this history and produce strategic guidance.

## Your Responsibilities

### 1. Dimension Analysis
Analyze the candidate's scores across all evaluation dimensions (requirements, high-level architecture, deep dive, scalability, communication). Identify:
- Which dimension is consistently weakest
- Which dimensions are improving over time
- Where scores plateau and what might break through

### 2. Topic Coverage
Review which system design topics the candidate has practiced and which they haven't. Identify gaps in their coverage and recommend topics that would round out their preparation.

### 3. Thinking Pattern Recognition
Look across multiple interviews for recurring patterns in how the candidate approaches problems:
- Do they consistently skip requirements gathering?
- Do they go too deep too early?
- Do they struggle with back-of-envelope calculations?
- Do they miss trade-off discussions?
Identify both positive patterns (strengths to maintain) and negative patterns (habits to break).

### 4. Scenario Generation
Based on identified gaps, optionally generate a custom practice question that specifically targets the candidate's weaknesses. The question should:
- Address a topic or dimension they struggle with
- Be at an appropriate difficulty level for their current skill
- Include tags that map to their gap areas

### 5. Progressive Difficulty
Track the candidate's overall trajectory and recommend appropriate difficulty:
- If scores are consistently low (1-2), suggest medium difficulty questions
- If scores are improving (3-4), suggest harder variants or new topics
- If scores are high (4-5), suggest hard questions with complex constraints

### 6. Metacognitive Coaching
Help the candidate develop self-awareness about their interview approach:
- Point out blind spots they may not realize they have
- Suggest reflection exercises or frameworks
- Encourage deliberate practice on specific sub-skills
- Frame feedback in terms of growth and improvement

## Output

You MUST use the submit_analysis tool to submit your structured analysis. Do not produce free-text output — use the tool.

If you identify a specific gap that would benefit from a custom practice question, include it in the generated_question field. Otherwise, set generated_question to null."""

    def build_history_summary(
        self,
        sessions: list[Session],
        evaluations: list[Evaluation],
        questions: list[Question],
    ) -> str:
        """Build a text summary of the candidate's interview history.

        Correctly maps evaluation.session_id -> session.question_id -> question
        to resolve which question each evaluation corresponds to.

        Only includes tags from questions that have actually been attempted
        (have a reviewed session).

        Args:
            sessions: All sessions in the history.
            evaluations: All evaluations in the history.
            questions: All available questions.

        Returns:
            Formatted history summary string for the LLM.
        """
        if not sessions:
            return (
                "The candidate has no interview history yet. "
                "This is their first coaching session. "
                "Recommend starting with a medium difficulty question to establish a baseline."
            )

        question_map: dict[int, Question] = {q.id: q for q in questions}
        session_map: dict[int, Session] = {s.id: s for s in sessions}

        lines: list[str] = [
            "# Candidate Interview History",
            "",
        ]

        # Build per-session summaries with correct mapping
        lines.append("## Session Details")
        lines.append("")

        for ev in evaluations:
            session = session_map.get(ev.session_id)
            if not session:
                continue
            # CORRECT: session.question_id -> question (NOT ev.session_id -> question)
            question = question_map.get(session.question_id)
            if not question:
                continue

            lines.append(f"### {question.title} (Session {session.id})")
            lines.append(f"- Difficulty: {question.difficulty.value}")
            lines.append(f"- Tags: {', '.join(question.tags)}")
            if session.duration_seconds is not None:
                minutes = session.duration_seconds // 60
                lines.append(f"- Duration: {minutes} minutes")
            lines.append(f"- Scores:")
            lines.append(f"  - Requirements: {ev.score_requirements}")
            lines.append(f"  - High-Level: {ev.score_highlevel}")
            lines.append(f"  - Deep Dive: {ev.score_deepdive}")
            lines.append(f"  - Scalability: {ev.score_scalability}")
            lines.append(f"  - Communication: {ev.score_communication}")
            lines.append(f"  - Overall: {ev.score_overall}")
            lines.append(f"- Strengths: {'; '.join(ev.strengths)}")
            lines.append(f"- Gaps: {'; '.join(ev.gaps)}")
            lines.append(f"- Advice: {ev.advice}")
            lines.append("")

        # Only include tags from actually attempted questions
        attempted_question_ids = {
            s.question_id for s in sessions if s.status == SessionStatus.reviewed
        }
        attempted_tags: set[str] = set()
        for q in questions:
            if q.id in attempted_question_ids:
                attempted_tags.update(q.tags)

        all_tags: set[str] = set()
        for q in questions:
            all_tags.update(q.tags)

        uncovered_tags = all_tags - attempted_tags

        lines.append("## Topics Covered (Attempted)")
        lines.append(", ".join(sorted(attempted_tags)) if attempted_tags else "None")
        lines.append("")

        if uncovered_tags:
            lines.append("## Topics Not Yet Covered (Available)")
            lines.append(", ".join(sorted(uncovered_tags)))
            lines.append("")

        # Compute aggregate score stats
        if evaluations:
            dimensions = [
                "requirements",
                "highlevel",
                "deepdive",
                "scalability",
                "communication",
                "overall",
            ]
            dim_labels = {
                "requirements": "Requirements",
                "highlevel": "High-Level",
                "deepdive": "Deep Dive",
                "scalability": "Scalability",
                "communication": "Communication",
                "overall": "Overall",
            }

            lines.append("## Score Averages")
            for dim in dimensions:
                scores = [getattr(ev, f"score_{dim}") for ev in evaluations]
                avg = sum(scores) / len(scores)
                lines.append(f"- {dim_labels[dim]}: {avg:.1f}")
            lines.append("")

        return "\n".join(lines)

    def parse_response(
        self,
        raw: dict[str, Any],
        session_ids: list[int],
        raw_response: dict[str, Any] | None = None,
    ) -> tuple[CoachReviewCreate, QuestionCreate | None]:
        """Extract the tool call input and build a CoachReviewCreate.

        Args:
            raw: The tool_use input dict from the API response.
            session_ids: List of session IDs that were analyzed.
            raw_response: The full raw API response for storage.

        Returns:
            A tuple of (CoachReviewCreate, optional QuestionCreate).

        Raises:
            KeyError: If required fields are missing from the response.
        """
        # Validate required fields
        if "recommendation" not in raw:
            raise KeyError("recommendation")
        if "gap_analysis" not in raw:
            raise KeyError("gap_analysis")

        review = CoachReviewCreate(
            recommendation=raw["recommendation"],
            gap_analysis=raw["gap_analysis"],
            sessions_analyzed=session_ids,
            raw_response=raw_response or {},
        )

        # Handle optional generated question
        generated_question: QuestionCreate | None = None
        gq = raw.get("generated_question")
        if gq is not None and isinstance(gq, dict) and gq:
            generated_question = QuestionCreate(
                title=gq["title"],
                prompt=gq["prompt"],
                difficulty=Difficulty(gq["difficulty"]),
                tags=gq.get("tags", []),
                source=QuestionSource.coach_generated,
                source_detail=review.recommendation,
            )

        return review, generated_question

    async def analyze(
        self,
        client: object,
        sessions: list[Session],
        evaluations: list[Evaluation],
        questions: list[Question],
        session_ids: list[int],
    ) -> tuple[CoachReviewCreate, QuestionCreate | None]:
        """Run the full coaching analysis pipeline.

        Builds the history summary, calls the Anthropic API with the
        coaching tool, and parses the structured response.

        Args:
            client: An Anthropic client instance.
            sessions: All sessions in the history.
            evaluations: All evaluations in the history.
            questions: All available questions.
            session_ids: List of session IDs being analyzed.

        Returns:
            A tuple of (CoachReviewCreate, optional QuestionCreate).
        """
        system_prompt = self.build_system_prompt()
        history_summary = self.build_history_summary(
            sessions=sessions,
            evaluations=evaluations,
            questions=questions,
        )

        response = client.messages.create(  # type: ignore[union-attr]
            model=self.config.model,
            max_tokens=self.config.max_tokens,
            system=system_prompt,
            messages=[{"role": "user", "content": history_summary}],
            tools=self.get_tool_schema(),
            tool_choice={"type": "tool", "name": "submit_analysis"},
        )

        # Extract tool call input from the response
        tool_input = response.content[0].input
        raw_response = response.model_dump()

        return self.parse_response(
            raw=tool_input,
            session_ids=session_ids,
            raw_response=raw_response,
        )
