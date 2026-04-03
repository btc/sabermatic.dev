-- name: InsertEvaluation :one
INSERT INTO evaluations (
    session_id, score_requirements, score_architecture,
    score_deep_dive, score_scalability, score_communication,
    score_overall, strengths, gaps, advice
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING id;

-- name: GetEvaluationBySession :one
SELECT id, session_id, score_requirements, score_architecture,
       score_deep_dive, score_scalability, score_communication,
       score_overall, strengths, gaps, advice, created_at
FROM evaluations WHERE session_id = $1;

-- name: InsertAnnotation :exec
INSERT INTO annotations (evaluation_id, message_id, annotation_type, content)
VALUES ($1, $2, $3, $4);

-- name: GetAnnotationsByEvaluation :many
SELECT a.id, a.evaluation_id, a.message_id, a.annotation_type, a.content,
       m.seq AS message_seq
FROM annotations a
JOIN messages m ON m.id = a.message_id
WHERE a.evaluation_id = $1
ORDER BY m.seq, a.annotation_type;
