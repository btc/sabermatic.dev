from __future__ import annotations

from fastapi import APIRouter, Depends, HTTPException

from backend.database import Conn, list_questions, get_question, insert_question
from backend.deps import get_db
from backend.models import Question, QuestionCreate

router = APIRouter(prefix="/questions", tags=["questions"])


@router.get("/", response_model=list[Question])
async def list_all_questions(
    conn: Conn = Depends(get_db),
) -> list[Question]:
    return await list_questions(conn)


@router.get("/{question_id}", response_model=Question)
async def get_one_question(
    question_id: int,
    conn: Conn = Depends(get_db),
) -> Question:
    q = await get_question(conn, question_id)
    if q is None:
        raise HTTPException(status_code=404, detail="Question not found")
    return q


@router.post("/", response_model=Question, status_code=201)
async def create_question(
    body: QuestionCreate,
    conn: Conn = Depends(get_db),
) -> Question:
    return await insert_question(conn, body)
