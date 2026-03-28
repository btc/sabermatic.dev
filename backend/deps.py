from typing import AsyncGenerator

import asyncpg
from fastapi import Request

from backend.config import Settings


async def get_db(request: Request) -> AsyncGenerator[asyncpg.Connection, None]:
    async with request.app.state.pool.acquire() as conn:
        yield conn


def get_settings(request: Request) -> Settings:
    return request.app.state.settings


def get_anthropic(request: Request):
    return request.app.state.anthropic_client


def get_openai(request: Request):
    return request.app.state.openai_client
