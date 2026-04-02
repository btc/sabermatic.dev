-- 002_improvements.sql: Archive, TTS, educator output, token tracking

-- Archive support
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS archived BOOLEAN NOT NULL DEFAULT false;
CREATE INDEX IF NOT EXISTS idx_sessions_archived ON sessions (archived);

-- TTS preference persistence
ALTER TABLE sessions ADD COLUMN IF NOT EXISTS tts_enabled BOOLEAN NOT NULL DEFAULT true;

-- Educator output
ALTER TABLE evaluations ADD COLUMN IF NOT EXISTS educator_model_answer TEXT;
ALTER TABLE evaluations ADD COLUMN IF NOT EXISTS educator_gap_deepdives TEXT;
ALTER TABLE evaluations ADD COLUMN IF NOT EXISTS educator_raw_response JSONB;

-- LLM token tracking (interviewer per-turn usage)
ALTER TABLE messages ADD COLUMN IF NOT EXISTS raw_response JSONB;

-- Convenience view for token aggregation
CREATE OR REPLACE VIEW llm_token_usage AS
    SELECT session_id, 'interviewer' as role,
        raw_response->>'model' as model,
        (raw_response->'usage'->>'input_tokens')::int as input_tokens,
        (raw_response->'usage'->>'output_tokens')::int as output_tokens,
        timestamp as created_at
    FROM messages
    WHERE role = 'interviewer' AND raw_response IS NOT NULL
    UNION ALL
    SELECT session_id, 'evaluator',
        raw_response->>'model',
        (raw_response->'usage'->>'input_tokens')::int,
        (raw_response->'usage'->>'output_tokens')::int,
        evaluated_at
    FROM evaluations
    UNION ALL
    SELECT null, 'coach',
        raw_response->>'model',
        (raw_response->'usage'->>'input_tokens')::int,
        (raw_response->'usage'->>'output_tokens')::int,
        created_at
    FROM coach_reviews
    UNION ALL
    SELECT session_id, 'educator',
        educator_raw_response->>'model',
        (educator_raw_response->'usage'->>'input_tokens')::int,
        (educator_raw_response->'usage'->>'output_tokens')::int,
        evaluated_at
    FROM evaluations
    WHERE educator_raw_response IS NOT NULL;
