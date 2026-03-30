from collections.abc import AsyncIterator
from contextlib import asynccontextmanager

import anthropic
import asyncpg
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware
from openai import AsyncOpenAI

from backend.config import Settings
from backend.tracing import setup_tracing, instrument_app


def create_app(
    database_url: str | None = None, settings: Settings | None = None
) -> FastAPI:
    @asynccontextmanager
    async def lifespan(app: FastAPI) -> AsyncIterator[None]:
        s = settings or Settings()
        app.state.settings = s
        setup_tracing(data_dir=str(s.data_dir))
        instrument_app(app)
        url = database_url or s.database_url
        app.state.pool = await asyncpg.create_pool(url, min_size=1, max_size=3)
        app.state.anthropic_client = anthropic.AsyncAnthropic(
            api_key=s.anthropic_api_key
        )
        app.state.openai_client = AsyncOpenAI(api_key=s.openai_api_key)
        yield
        await app.state.pool.close()

    app = FastAPI(title="System Design Drill", lifespan=lifespan)
    app.add_middleware(
        CORSMiddleware,
        allow_origins=["http://localhost:3000"],
        allow_credentials=True,
        allow_methods=["*"],
        allow_headers=["*"],
    )

    from backend.routes import api_router
    from backend.routes.ws import router as ws_router

    app.include_router(api_router)
    app.include_router(ws_router)

    return app


app = create_app()
