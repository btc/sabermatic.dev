-- name: AcquireGeneratingStatus :one
-- Atomically set status to 'generating'. Returns the session ID if successful.
-- No row returned means the session is not active or already generating.
UPDATE interview_sessions
SET status = 'generating', generating_since = NOW(), updated_at = NOW()
WHERE id = $1 AND status = 'active'
RETURNING id;

-- name: ReleaseGeneratingStatus :exec
-- Revert status to 'active' after a turn completes or fails.
UPDATE interview_sessions
SET status = 'active', generating_since = NULL, updated_at = NOW()
WHERE id = $1 AND status = 'generating';

-- name: GetSessionForTurn :one
-- Load session + question in a single query for turn execution.
SELECT s.id, s.user_id, s.question_id, s.status,
       s.config_duration_minutes, s.config_tts_enabled,
       s.config_coach_briefing, s.started_at,
       q.title AS question_title, q.prompt AS question_prompt,
       q.difficulty AS question_difficulty, q.hints AS question_hints
FROM interview_sessions s
JOIN questions q ON q.id = s.question_id
WHERE s.id = $1;

-- name: GetMessagesBySessionOffset :many
-- Return messages after the given offset (for known_message_count cursor).
SELECT id, session_id, seq, role, content, input_method, audio_url, created_at
FROM messages
WHERE session_id = $1
ORDER BY seq
OFFSET $2;

-- name: CountInterviewerMessages :one
-- Count completed interviewer turns for turn_count derivation.
SELECT COUNT(*)::int AS count
FROM messages
WHERE session_id = $1 AND role = 'interviewer';

-- name: GetSessionStatus :one
-- Lightweight status check for EndSession/CancelSession polling.
SELECT status, generating_since
FROM interview_sessions
WHERE id = $1;

-- name: CleanupStaleGenerating :exec
-- Reset sessions stuck in 'generating' for too long (crash recovery).
UPDATE interview_sessions
SET status = 'active', generating_since = NULL, updated_at = NOW()
WHERE status = 'generating'
  AND generating_since < NOW() - INTERVAL '5 minutes';

-- name: InlineRecoverStaleGenerating :exec
-- Inline crash recovery for EndSession/CancelSession.
-- Conditional WHERE makes this idempotent and race-free.
UPDATE interview_sessions
SET status = 'active', generating_since = NULL, updated_at = NOW()
WHERE id = $1
  AND status = 'generating'
  AND generating_since < NOW() - INTERVAL '5 minutes';
