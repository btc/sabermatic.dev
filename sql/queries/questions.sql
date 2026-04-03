-- name: ListSeedQuestions :many
SELECT id, title, prompt, difficulty, tags, hints, source, created_at
FROM questions
WHERE source = 'seed' AND user_id IS NULL
ORDER BY created_at;

-- name: GetQuestion :one
SELECT id, user_id, title, prompt, difficulty, tags, hints, source, coach_rationale, created_at, updated_at
FROM questions
WHERE id = $1;

-- name: CountSeedQuestions :one
SELECT COUNT(*) FROM questions WHERE source = 'seed' AND user_id IS NULL;

-- name: ListQuestionsForUser :many
SELECT id, user_id, title, prompt, difficulty, tags, hints, source, created_at
FROM questions
WHERE (source = 'seed' AND user_id IS NULL) OR user_id = $1
ORDER BY created_at;
