ALTER TABLE users
  ADD COLUMN stripe_subscription_id     TEXT,
  ADD COLUMN sub_cancel_at_period_end   BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN sub_cancel_is_auto         BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN sub_current_period_start   TIMESTAMPTZ,
  ADD COLUMN pending_kept_banner        BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN idle_eligible_after        TIMESTAMPTZ NOT NULL DEFAULT NOW();
