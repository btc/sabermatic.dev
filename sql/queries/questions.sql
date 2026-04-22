-- name: ListSeedQuestions :many
SELECT id, title, prompt, difficulty, tags, hints, source, image_url, created_at
FROM questions
WHERE source = 'seed' AND user_id IS NULL
ORDER BY created_at;

-- name: GetQuestion :one
SELECT id, user_id, title, prompt, difficulty, tags, hints, source, coach_rationale, created_at, updated_at, image_url
FROM questions
WHERE id = $1;

-- name: ListQuestionsForUser :many
SELECT id, user_id, title, prompt, difficulty, tags, hints, source, image_url, created_at
FROM questions
WHERE (source = 'seed' AND user_id IS NULL) OR user_id = $1
ORDER BY created_at;

-- name: GetQuestionsForUser :many
SELECT * FROM questions
WHERE user_id IS NULL OR user_id = $1
ORDER BY created_at;

-- name: InsertQuestion :one
INSERT INTO questions (user_id, title, prompt, difficulty, tags, source, coach_rationale)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: ListQuestionsWithoutImages :many
SELECT id FROM questions
WHERE image_url IS NULL;

-- name: SetQuestionImageURL :exec
UPDATE questions SET image_url = $2, updated_at = NOW()
WHERE id = $1;

-- name: ListFeaturedQuestions :many
SELECT id, title, prompt, difficulty, tags, hints, source, image_url, created_at
FROM questions
WHERE is_featured = true
ORDER BY featured_order;

-- name: CountSeedQuestions :one
SELECT COUNT(*)::int AS count FROM questions
WHERE source = 'seed' AND user_id IS NULL;
