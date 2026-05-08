DROP INDEX IF EXISTS idx_auth_sessions_user_last_active;
ALTER TABLE auth_sessions DROP COLUMN revoked_at;
