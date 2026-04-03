-- name: InsertMessage :one
INSERT INTO messages (id, session_id, seq, role, content, input_method, audio_url)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id, session_id, seq, role, content, input_method, audio_url, created_at;

-- name: GetMessagesBySession :many
SELECT id, session_id, seq, role, content, input_method, audio_url, created_at
FROM messages
WHERE session_id = $1
ORDER BY seq;

-- name: GetMessagesBySessionAfterSeq :many
SELECT id, session_id, seq, role, content, input_method, audio_url, created_at
FROM messages
WHERE session_id = $1 AND seq > $2
ORDER BY seq;

-- name: GetMaxSeqForSession :one
SELECT COALESCE(MAX(seq), 0)::int AS max_seq
FROM messages
WHERE session_id = $1;
