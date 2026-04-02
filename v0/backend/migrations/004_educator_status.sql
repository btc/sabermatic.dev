-- 004_educator_status.sql: Explicit state machine for educator analysis

ALTER TABLE evaluations ADD COLUMN IF NOT EXISTS educator_status TEXT;

-- Backfill existing rows
UPDATE evaluations SET educator_status = 'completed' WHERE educator_model_answer IS NOT NULL;
UPDATE evaluations SET educator_status = 'failed'
    WHERE educator_model_answer IS NULL
      AND educator_raw_response IS NOT NULL
      AND educator_raw_response::jsonb @> '{"error": true}';
