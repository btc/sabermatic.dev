#!/usr/bin/env bash
# Seed a local dev user and questions. Does NOT require the server.
set -euo pipefail

set -a && . ./.env && set +a

EMAIL="dev@drill.dev"
PASSWORD="devdevdev123"
NAME="Dev User"

# bcrypt hash of "devdevdev123" (cost 10 for speed)
HASH='$2a$10$kjFgcQxsWOHGHOJuZLtPEeZfByr/Y/CIg5QfL.pttHkaf/VLRxmJ6'

echo "Creating dev user..."
psql "$DATABASE_URL" -q <<SQL
INSERT INTO users (email, email_verified, password_hash, display_name, role, plan)
VALUES ('$EMAIL', true, '$HASH', '$NAME', 'candidate', 'free')
ON CONFLICT (email) DO NOTHING;
SQL

echo "Seeding questions..."
psql "$DATABASE_URL" -f seed/questions.sql

echo ""
echo "Done. Log in at http://localhost:3000/login"
echo "  Email:    $EMAIL"
echo "  Password: $PASSWORD"
