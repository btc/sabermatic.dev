-- Add 'cancelled' to interview_sessions status CHECK constraint.
ALTER TABLE interview_sessions DROP CONSTRAINT interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
    CHECK (status IN ('active', 'completed', 'evaluating', 'reviewed', 'evaluation_failed', 'failed', 'cancelled'));
