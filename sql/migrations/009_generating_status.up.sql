-- Add 'generating' to interview_sessions status CHECK constraint.
ALTER TABLE interview_sessions DROP CONSTRAINT interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
    CHECK (status IN ('active', 'generating', 'completed', 'evaluating', 'reviewed', 'evaluation_failed', 'failed', 'cancelled'));

-- Track when a session entered 'generating' state for crash recovery.
ALTER TABLE interview_sessions ADD COLUMN generating_since TIMESTAMPTZ;
