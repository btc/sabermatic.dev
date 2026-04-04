ALTER TABLE interview_sessions ADD COLUMN archived BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE interview_sessions SET archived = TRUE WHERE archived_at IS NOT NULL;
ALTER TABLE interview_sessions DROP COLUMN archived_at;

DROP INDEX IF EXISTS idx_sessions_user_status;
CREATE INDEX idx_sessions_user_status ON interview_sessions(user_id, status)
    WHERE archived = FALSE;

DROP INDEX IF EXISTS idx_sessions_user_archived;
CREATE INDEX idx_sessions_user_archived ON interview_sessions(user_id, archived);
