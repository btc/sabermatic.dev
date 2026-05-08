CREATE TABLE stripe_webhook_dedup (
  event_id      TEXT        PRIMARY KEY,
  event_type    TEXT        NOT NULL,
  processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
