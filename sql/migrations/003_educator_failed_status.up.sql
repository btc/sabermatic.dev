-- Add 'failed' to educator_analyses status CHECK constraint.
ALTER TABLE educator_analyses DROP CONSTRAINT IF EXISTS educator_analyses_status_check;
ALTER TABLE educator_analyses ADD CONSTRAINT educator_analyses_status_check
    CHECK (status IN ('generating', 'completed', 'failed'));
