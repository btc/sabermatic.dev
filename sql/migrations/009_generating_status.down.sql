-- Remove generating_since column.
ALTER TABLE interview_sessions DROP COLUMN generating_since;

-- Revert status CHECK constraint to remove 'generating'.
ALTER TABLE interview_sessions DROP CONSTRAINT interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
    CHECK (status IN ('active', 'completed', 'evaluating', 'reviewed', 'evaluation_failed', 'failed', 'cancelled'));
