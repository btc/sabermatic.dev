"""Test fixtures for drill tests.

Creates a drill_test database, runs migrations, and provides a db_conn
fixture with per-test transaction rollback for isolation.
"""

import asyncio
from typing import AsyncGenerator

import asyncpg
import pytest
import pytest_asyncio

from backend.migrations.run import run_migrations
from backend.seed_questions import seed_questions


TEST_DB_NAME = "drill_test"
ADMIN_URL = "postgresql://localhost:5432/postgres"
TEST_DB_URL = f"postgresql://localhost:5432/{TEST_DB_NAME}"


@pytest.fixture(scope="session")
def event_loop():
    """Create a session-scoped event loop for async tests."""
    loop = asyncio.new_event_loop()
    yield loop
    loop.close()


@pytest_asyncio.fixture(scope="session")
async def test_database() -> AsyncGenerator[str, None]:
    """Create the test database and run migrations. Session-scoped."""
    # Connect to postgres admin DB to create test DB
    admin_conn = await asyncpg.connect(ADMIN_URL)
    try:
        # Drop and recreate for a clean slate each test run
        await admin_conn.execute(
            f"DROP DATABASE IF EXISTS {TEST_DB_NAME} WITH (FORCE)"
        )
        await admin_conn.execute(f"CREATE DATABASE {TEST_DB_NAME}")
    finally:
        await admin_conn.close()

    # Run migrations and seed questions against the test DB
    conn = await asyncpg.connect(TEST_DB_URL)
    try:
        await run_migrations(conn)
        await seed_questions(conn)
    finally:
        await conn.close()

    yield TEST_DB_URL

    # Cleanup: drop test database after all tests
    admin_conn = await asyncpg.connect(ADMIN_URL)
    try:
        await admin_conn.execute(
            f"DROP DATABASE IF EXISTS {TEST_DB_NAME} WITH (FORCE)"
        )
    finally:
        await admin_conn.close()


@pytest_asyncio.fixture
async def db_conn(
    test_database: str,
) -> AsyncGenerator[asyncpg.Connection, None]:
    """Per-test database connection with transaction rollback for isolation."""
    conn = await asyncpg.connect(test_database)
    tx = conn.transaction()
    await tx.start()
    try:
        yield conn
    finally:
        await tx.rollback()
        await conn.close()
