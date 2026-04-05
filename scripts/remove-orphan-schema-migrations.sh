#!/usr/bin/env bash
# Removes the orphan schema_migrations table from drill_v0.
# Created accidentally by golang-migrate when DATABASE_URL was misconfigured.
#
# Safety: verifies exact expected state before dropping.
set -euo pipefail

DB="${1:-drill_v0}"

echo "=== Removing orphan schema_migrations from $DB ==="
echo ""

echo "Step 1: Verify table exists..."
if ! psql "$DB" -tA -c "SELECT 1 FROM information_schema.tables WHERE table_name = 'schema_migrations'" | grep -q 1; then
    echo "Table schema_migrations does not exist in $DB. Nothing to do."
    exit 0
fi

echo "Step 2: Verify table state..."
ROW_COUNT=$(psql "$DB" -tA -c "SELECT count(*) FROM schema_migrations")
VERSION=$(psql "$DB" -tA -c "SELECT version FROM schema_migrations")
DIRTY=$(psql "$DB" -tA -c "SELECT dirty FROM schema_migrations")

echo "  Rows: $ROW_COUNT (expected: 1)"
echo "  Version: $VERSION (expected: 1)"
echo "  Dirty: $DIRTY (expected: f)"

if [[ "$ROW_COUNT" -ne 1 ]] || [[ "$VERSION" -ne 1 ]] || [[ "$DIRTY" != "f" ]]; then
    echo ""
    echo "ERROR: Table state does not match expected orphan state."
    echo "Aborting. Do NOT drop without manual investigation."
    exit 1
fi

echo ""
echo "Step 3: Dropping orphan table..."
psql "$DB" -c "DROP TABLE schema_migrations"
echo ""
echo "Done. Orphan removed. All other tables untouched."
