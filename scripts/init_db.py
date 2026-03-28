"""Database initialization script.

Connects to the 'postgres' database via asyncpg, creates the 'drill' database
(catching "already exists"), runs migrations, and seeds questions.
"""

import asyncio

import asyncpg

from backend.config import Settings
from backend.migrations.run import run_migrations
from backend.seed_questions import seed_questions


def _parse_db_url(database_url: str) -> tuple[str, str]:
    """Extract the database name and build a 'postgres' admin URL.

    Returns (admin_url, db_name).
    """
    # database_url looks like: postgresql://host:port/dbname
    # or postgresql://user:pass@host:port/dbname
    last_slash = database_url.rfind("/")
    base = database_url[:last_slash]
    db_name = database_url[last_slash + 1 :]
    admin_url = f"{base}/postgres"
    return admin_url, db_name


async def create_database(admin_url: str, db_name: str) -> None:
    """Create the target database, ignoring 'already exists' errors."""
    conn = await asyncpg.connect(admin_url)
    try:
        await conn.execute(f'CREATE DATABASE "{db_name}"')
        print(f"Created database '{db_name}'.")
    except asyncpg.DuplicateDatabaseError:
        print(f"Database '{db_name}' already exists.")
    finally:
        await conn.close()


async def main() -> None:
    settings = Settings()
    database_url = settings.database_url
    admin_url, db_name = _parse_db_url(database_url)

    # Step 1: Create database
    print("Step 1: Creating database...")
    await create_database(admin_url, db_name)

    # Step 2: Run migrations
    print("Step 2: Running migrations...")
    conn = await asyncpg.connect(database_url)
    try:
        await run_migrations(conn)
    finally:
        await conn.close()

    # Step 3: Seed questions
    print("Step 3: Seeding questions...")
    conn = await asyncpg.connect(database_url)
    try:
        await seed_questions(conn)
    finally:
        await conn.close()

    print("Database initialization complete.")


if __name__ == "__main__":
    asyncio.run(main())
