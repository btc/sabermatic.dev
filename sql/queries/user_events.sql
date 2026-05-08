-- name: HasAutoCanceledThisPeriod :one
-- Returns true if a subscription_auto_canceled event already exists for
-- this (user, subscription, period). Backs the multi-firing dedup in §4.1.
-- metadata->>'current_period_start' is RFC3339 text written by cancelMetadata.
SELECT EXISTS (
  SELECT 1 FROM user_events
  WHERE user_id = @user_id
    AND event_type = 'subscription_auto_canceled'
    AND (metadata->>'subscription_id') = @subscription_id::text
    AND (metadata->>'current_period_start')::timestamptz = @current_period_start::timestamptz
);

-- name: GetMostRecentKeptOrCanceledForPeriod :one
-- For multi-firing dedup: did a 'subscription_kept' event arrive after the
-- most recent 'subscription_auto_canceled' for this period? If yes, the user
-- already kept their sub for this period; don't re-cancel.
SELECT event_type
FROM user_events
WHERE user_id = @user_id
  AND event_type IN ('subscription_auto_canceled', 'subscription_kept')
  AND (metadata->>'subscription_id') = @subscription_id::text
  AND (metadata->>'current_period_start')::timestamptz = @current_period_start::timestamptz
ORDER BY created_at DESC
LIMIT 1;

-- name: InsertSubscriptionAutoCanceledEvent :exec
-- Composed insert for the cancel decision. metadata is already-marshaled JSON.
INSERT INTO user_events (user_id, event_type, metadata)
VALUES (@user_id, 'subscription_auto_canceled', @metadata);

-- name: InsertSubscriptionKeptEvent :exec
INSERT INTO user_events (user_id, event_type, metadata)
VALUES (@user_id, 'subscription_kept', @metadata);
