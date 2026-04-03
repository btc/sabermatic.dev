-- name: InsertLLMCall :one
INSERT INTO llm_calls (session_id, user_id, role, model, input_tokens, output_tokens, estimated_cost, latency_ms)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id;

-- name: InsertLLMCallContent :exec
INSERT INTO llm_call_content (llm_call_id, prompt, response)
VALUES ($1, $2, $3);
