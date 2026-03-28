from fastapi import APIRouter, Depends, HTTPException
import asyncpg

from backend.deps import get_db
from backend.database import list_questions, get_question, insert_question
from backend.models import Question, QuestionCreate

router = APIRouter(prefix="/questions", tags=["questions"])


@router.get("/", response_model=list[Question])
async def list_all_questions(conn: asyncpg.Connection = Depends(get_db)):
    return await list_questions(conn)


@router.get("/{question_id}", response_model=Question)
async def get_one_question(
    question_id: int, conn: asyncpg.Connection = Depends(get_db)
):
    q = await get_question(conn, question_id)
    if q is None:
        raise HTTPException(status_code=404, detail="Question not found")
    return q


@router.post("/", response_model=Question, status_code=201)
async def create_question(
    body: QuestionCreate, conn: asyncpg.Connection = Depends(get_db)
):
    return await insert_question(conn, body)
