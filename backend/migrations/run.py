"""Simple migration runner.

Reads .sql files from the migrations directory, tracks applied migrations
in a _migrations table, and applies unapplied ones in numerical order.
"""

import asyncio
from pathlib import Path

import asyncpg


MIGRATIONS_DIR = Path(__file__).parent


async def ensure_migrations_table(conn: asyncpg.Connection) -> None:
    """Create the _migrations tracking table if it doesn't exist."""
    await conn.execute("""
        CREATE TABLE IF NOT EXISTS _migrations (
            id          SERIAL PRIMARY KEY,
            filename    TEXT NOT NULL UNIQUE,
            applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
        )
    """)


async def get_applied_migrations(conn: asyncpg.Connection) -> set[str]:
    """Return the set of already-applied migration filenames."""
    rows = await conn.fetch("SELECT filename FROM _migrations")
    return {row["filename"] for row in rows}


def discover_migrations() -> list[Path]:
    """Find all .sql migration files, sorted numerically."""
    sql_files = sorted(MIGRATIONS_DIR.glob("*.sql"))
    return sql_files


async def run_migrations(conn: asyncpg.Connection) -> list[str]:
    """Run all unapplied migrations in order. Returns list of applied filenames."""
    await ensure_migrations_table(conn)
    applied = await get_applied_migrations(conn)
    migrations = discover_migrations()

    newly_applied: list[str] = []
    for migration_path in migrations:
        filename = migration_path.name
        if filename in applied:
            continue

        sql = migration_path.read_text()
        async with conn.transaction():
            await conn.execute(sql)
            await conn.execute(
                "INSERT INTO _migrations (filename) VALUES ($1)", filename
            )
        newly_applied.append(filename)
        print(f"  Applied: {filename}")

    if not newly_applied:
        print("  No new migrations to apply.")

    return newly_applied


async def main(database_url: str) -> None:
    """Connect to the database and run pending migrations."""
    conn = await asyncpg.connect(database_url)
    try:
        print("Running migrations...")
        await run_migrations(conn)
        print("Migrations complete.")
    finally:
        await conn.close()


if __name__ == "__main__":
    import sys

    url = sys.argv[1] if len(sys.argv) > 1 else "postgresql://localhost:5432/drill"
    asyncio.run(main(url))
