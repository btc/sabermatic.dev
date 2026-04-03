-- name: InsertCoachAnalysis :one
INSERT INTO coach_analyses (
    user_id, narrative, weakest_dimension,
    improving_dimensions, topic_gaps,
    suggested_question_id, sessions_analyzed
) VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: GetLatestCoachAnalysis :one
SELECT id, user_id, narrative, weakest_dimension,
       improving_dimensions, topic_gaps,
       suggested_question_id, sessions_analyzed, created_at
FROM coach_analyses
WHERE user_id = $1
ORDER BY created_at DESC LIMIT 1;
