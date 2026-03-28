"""Pydantic models for drill application.

Create models are used for input validation; Read models include
database-generated fields like id and timestamps.
"""

from datetime import datetime
from enum import Enum
from typing import Optional

from pydantic import BaseModel, Field, field_validator


# --- Enums ---


class Difficulty(str, Enum):
    medium = "medium"
    hard = "hard"


class QuestionSource(str, Enum):
    seed = "seed"
    custom = "custom"
    coach_generated = "coach_generated"


class SessionStatus(str, Enum):
    active = "active"
    completed = "completed"
    evaluating = "evaluating"
    reviewed = "reviewed"
    evaluation_failed = "evaluation_failed"


class MessageRole(str, Enum):
    interviewer = "interviewer"
    candidate = "candidate"


class AnnotationType(str, Enum):
    strength = "strength"
    gap = "gap"
    missed_opportunity = "missed_opportunity"
    note = "note"


# --- Question ---


class QuestionCreate(BaseModel):
    title: str
    prompt: str
    difficulty: Difficulty
    tags: list[str] = Field(default_factory=list)
    hints: Optional[dict] = None
    source: QuestionSource = QuestionSource.seed
    source_detail: Optional[str] = None


class Question(BaseModel):
    id: int
    title: str
    prompt: str
    difficulty: Difficulty
    tags: list[str]
    hints: Optional[dict] = None
    source: QuestionSource
    source_detail: Optional[str] = None
    created_at: datetime


# --- Session ---


class SessionCreate(BaseModel):
    question_id: int
    status: SessionStatus = SessionStatus.active
    timer_setting_sec: int = 2700
    interviewer_briefed: bool = False
    started_at: Optional[datetime] = None
    ended_at: Optional[datetime] = None
    duration_seconds: Optional[int] = None
    turn_count: Optional[int] = None
    audio_dir: Optional[str] = None
    status_detail: Optional[str] = None


class Session(BaseModel):
    id: int
    question_id: int
    status: SessionStatus
    status_detail: Optional[str] = None
    timer_setting_sec: int
    interviewer_briefed: bool
    started_at: datetime
    ended_at: Optional[datetime] = None
    duration_seconds: Optional[int] = None
    turn_count: Optional[int] = None
    audio_dir: Optional[str] = None


# --- Message ---


class MessageCreate(BaseModel):
    session_id: int
    sequence: int
    role: MessageRole
    content: str
    raw_content: Optional[str] = None
    audio_path: Optional[str] = None
    audio_duration_sec: Optional[float] = None
    timestamp: Optional[datetime] = None


class Message(BaseModel):
    id: int
    session_id: int
    sequence: int
    role: MessageRole
    content: str
    raw_content: Optional[str] = None
    timestamp: datetime
    audio_path: Optional[str] = None
    audio_duration_sec: Optional[float] = None


# --- Evaluation ---


def _validate_score(v: int) -> int:
    if not 1 <= v <= 5:
        raise ValueError("Score must be between 1 and 5")
    return v


class EvaluationCreate(BaseModel):
    session_id: int
    score_requirements: int
    score_highlevel: int
    score_deepdive: int
    score_scalability: int
    score_communication: int
    score_overall: int
    strengths: list[str]
    gaps: list[str]
    advice: str
    raw_response: dict
    evaluated_at: Optional[datetime] = None

    @field_validator(
        "score_requirements",
        "score_highlevel",
        "score_deepdive",
        "score_scalability",
        "score_communication",
        "score_overall",
    )
    @classmethod
    def score_in_range(cls, v: int) -> int:
        return _validate_score(v)


class Evaluation(BaseModel):
    id: int
    session_id: int
    score_requirements: int
    score_highlevel: int
    score_deepdive: int
    score_scalability: int
    score_communication: int
    score_overall: int
    strengths: list[str]
    gaps: list[str]
    advice: str
    raw_response: dict
    evaluated_at: datetime


# --- MessageAnnotation ---


class MessageAnnotationCreate(BaseModel):
    evaluation_id: int
    message_id: int
    annotation_type: AnnotationType
    content: str


class MessageAnnotation(BaseModel):
    id: int
    evaluation_id: int
    message_id: int
    annotation_type: AnnotationType
    content: str


# --- CoachReview ---


class CoachReviewCreate(BaseModel):
    recommendation: str
    gap_analysis: dict
    suggested_question_id: Optional[int] = None
    sessions_analyzed: list[int] = Field(default_factory=list)
    raw_response: dict
    created_at: Optional[datetime] = None


class CoachReview(BaseModel):
    id: int
    recommendation: str
    gap_analysis: dict
    suggested_question_id: Optional[int] = None
    sessions_analyzed: list[int]
    raw_response: dict
    created_at: datetime
