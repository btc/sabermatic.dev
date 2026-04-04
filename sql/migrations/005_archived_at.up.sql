-- Replace archived BOOLEAN with archived_at TIMESTAMPTZ.
ALTER TABLE interview_sessions ADD COLUMN archived_at TIMESTAMPTZ;

-- Backfill: set archived_at = updated_at for already-archived rows.
UPDATE interview_sessions SET archived_at = updated_at WHERE archived = TRUE;

ALTER TABLE interview_sessions DROP COLUMN archived;

-- Recreate indexes using archived_at IS NULL instead of archived = FALSE.
DROP INDEX IF EXISTS idx_sessions_user_status;
CREATE INDEX idx_sessions_user_status ON interview_sessions(user_id, status)
    WHERE archived_at IS NULL;

DROP INDEX IF EXISTS idx_sessions_user_archived;
CREATE INDEX idx_sessions_user_archived ON interview_sessions(user_id, archived_at);
