ALTER TABLE users
  DROP COLUMN idle_eligible_after,
  DROP COLUMN pending_kept_banner,
  DROP COLUMN sub_current_period_start,
  DROP COLUMN sub_cancel_is_auto,
  DROP COLUMN sub_cancel_at_period_end,
  DROP COLUMN stripe_subscription_id;
