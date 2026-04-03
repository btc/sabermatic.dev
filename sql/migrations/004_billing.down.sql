-- 004_billing.down.sql

CREATE TABLE usage_periods (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id),
    period_start  TIMESTAMPTZ NOT NULL,
    period_end    TIMESTAMPTZ NOT NULL,
    sessions_used INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, period_start)
);
CREATE INDEX idx_usage_periods_user ON usage_periods(user_id, period_start);

ALTER TABLE users DROP COLUMN free_full_educators_used;

ALTER TABLE interview_sessions DROP CONSTRAINT interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
    CHECK (status IN ('active', 'completed', 'evaluating', 'reviewed', 'evaluation_failed'));

ALTER TABLE interview_sessions DROP COLUMN reserved_minutes;

DROP INDEX idx_users_stripe_customer;
DROP TABLE ledger_entries;
DROP TABLE grants;
