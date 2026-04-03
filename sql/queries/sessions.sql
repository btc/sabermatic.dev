-- name: CreateSession :one
INSERT INTO interview_sessions (user_id, question_id, config_duration_minutes, config_tts_enabled)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, question_id, status, config_duration_minutes, config_tts_enabled,
          config_coach_briefing, started_at, ended_at, turn_count, archived, created_at, updated_at;

-- name: GetSession :one
SELECT id, user_id, question_id, status, config_duration_minutes, config_tts_enabled,
       config_coach_briefing, started_at, ended_at, turn_count, archived, created_at, updated_at
FROM interview_sessions
WHERE id = $1;

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
