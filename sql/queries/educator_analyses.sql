-- name: InsertEducatorAnalysis :one
INSERT INTO educator_analyses (session_id)
VALUES ($1)
RETURNING id;

-- name: UpdateEducatorAnalysisContent :exec
UPDATE educator_analyses
SET model_answer = $2, gap_deep_dives = $3, status = 'completed'
WHERE id = $1;

-- name: UpdateEducatorAnalysisStatus :exec
UPDATE educator_analyses SET status = $2 WHERE id = $1;

-- name: GetEducatorAnalysisBySession :one
SELECT id, session_id, status, model_answer, gap_deep_dives, created_at
FROM educator_analyses WHERE session_id = $1;
