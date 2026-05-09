-- name: TryClaimWebhookEvent :one
-- Returns the event_id on first claim; returns no row on subsequent claims.
-- Use the no-row return as the signal "this event was already handled".
INSERT INTO stripe_webhook_dedup (event_id, event_type)
VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id;
