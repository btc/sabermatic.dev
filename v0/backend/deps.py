from __future__ import annotations

from typing import AsyncGenerator

import anthropic
from fastapi import Request
from openai import AsyncOpenAI

from backend.config import Settings
from backend.database import Conn


async def get_db(
    request: Request,
) -> AsyncGenerator[Conn, None]:
    async with request.app.state.pool.acquire() as conn:
        yield conn


def get_settings(request: Request) -> Settings:
    settings: Settings = request.app.state.settings
    return settings


def get_anthropic(request: Request) -> anthropic.AsyncAnthropic:
    client: anthropic.AsyncAnthropic = request.app.state.anthropic_client
    return client


def get_openai(request: Request) -> AsyncOpenAI:
    client: AsyncOpenAI = request.app.state.openai_client
    return client
