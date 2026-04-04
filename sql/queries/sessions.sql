-- name: CreateSession :one
INSERT INTO interview_sessions (user_id, question_id, config_duration_minutes, config_tts_enabled, reserved_minutes)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetSession :one
SELECT s.id, s.user_id, s.question_id, s.status,
       s.config_duration_minutes, s.config_tts_enabled,
       s.config_coach_briefing, s.started_at, s.ended_at,
       s.turn_count, s.archived, s.created_at, s.updated_at,
       q.title AS question_title, q.prompt AS question_prompt,
       q.difficulty AS question_difficulty, q.hints AS question_hints
FROM interview_sessions s
JOIN questions q ON q.id = s.question_id
WHERE s.id = $1;

-- name: ListSessionsByUser :many
SELECT s.id, s.user_id, s.question_id, s.status, s.config_duration_minutes,
       s.config_tts_enabled, s.started_at, s.ended_at, s.turn_count, s.archived,
       s.created_at, q.title AS question_title
FROM interview_sessions s
JOIN questions q ON q.id = s.question_id
WHERE s.user_id = $1
ORDER BY s.created_at DESC;

-- name: UpdateSessionStatus :exec
UPDATE interview_sessions
SET status = $2, ended_at = NOW(), turn_count = $3, updated_at = NOW()
WHERE id = $1;

-- name: FindAbandonedSessions :many
SELECT id FROM interview_sessions
WHERE status = 'active'
  AND started_at + (config_duration_minutes + 5) * INTERVAL '1 minute' < NOW();

-- name: MarkSessionCompleted :exec
UPDATE interview_sessions
SET status = 'completed', ended_at = NOW(), updated_at = NOW()
WHERE id = $1;

-- name: MarkAbandonedSessionsCompleted :many
-- Batch-marks all abandoned sessions as completed and returns their IDs.
-- A session is abandoned if it's active and past its duration + 5 min buffer.
UPDATE interview_sessions
SET status = 'completed', ended_at = NOW(), updated_at = NOW()
WHERE status = 'active'
  AND started_at + (config_duration_minutes + 5) * INTERVAL '1 minute' < NOW()
RETURNING id;

-- name: UpdateSessionStatusOnly :exec
-- NB: Unlike UpdateSessionStatus, this does NOT touch ended_at or turn_count.
-- Used for status transitions after session completion (evaluating → reviewed,
-- → evaluation_failed) where end time and turn count should not change.
UPDATE interview_sessions
SET status = $2, updated_at = NOW()
WHERE id = $1;

-- name: GetReviewedSessionsForUser :many
SELECT * FROM interview_sessions
WHERE user_id = $1 AND status = 'reviewed' AND archived = FALSE
ORDER BY created_at;

-- name: GetReviewedSessionIDsForUser :many
SELECT id FROM interview_sessions
WHERE user_id = $1 AND status = 'reviewed' AND archived = FALSE
ORDER BY created_at;

-- name: CountActiveSessionsByUser :one
SELECT COUNT(*)::int AS count
FROM interview_sessions
WHERE user_id = $1 AND status = 'active';

-- name: GetSessionByID :one
SELECT * FROM interview_sessions WHERE id = $1;
