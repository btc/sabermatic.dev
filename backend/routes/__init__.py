from fastapi import APIRouter

from backend.routes.questions import router as questions_router
from backend.routes.sessions import router as sessions_router
from backend.routes.evaluation import router as evaluation_router
from backend.routes.coach_routes import router as coach_router
from backend.routes.health import router as health_router

api_router = APIRouter(prefix="/api")
api_router.include_router(questions_router)
api_router.include_router(sessions_router)
api_router.include_router(evaluation_router)
api_router.include_router(coach_router)
api_router.include_router(health_router)
