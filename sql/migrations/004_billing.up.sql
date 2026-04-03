-- 004_billing.up.sql

-- Grant buckets: each source of minutes creates a grant.
CREATE TABLE grants (
    id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id           UUID NOT NULL REFERENCES users(id),
    source            TEXT NOT NULL
                      CHECK (source IN ('free_grant', 'subscription', 'purchase', 'admin')),
    stripe_event_id   TEXT UNIQUE,
    initial_minutes   INT NOT NULL CHECK (initial_minutes > 0),
    remaining_minutes INT NOT NULL CHECK (remaining_minutes >= 0),
    expires_at        TIMESTAMPTZ,
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (remaining_minutes <= initial_minutes)
);

CREATE INDEX idx_grants_user_balance ON grants(user_id)
    WHERE remaining_minutes > 0;

CREATE UNIQUE INDEX idx_grants_free_per_month
    ON grants(user_id, date_trunc('month', timezone('UTC', created_at)))
    WHERE source = 'free_grant';

CREATE INDEX idx_users_stripe_customer ON users(stripe_customer_id)
    WHERE stripe_customer_id IS NOT NULL;

-- Append-only audit log of all balance mutations.
CREATE TABLE ledger_entries (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    grant_id    UUID NOT NULL REFERENCES grants(id),
    amount      INT NOT NULL,
    reason      TEXT NOT NULL
                CHECK (reason IN (
                    'free_monthly', 'subscription_renewal', 'purchase', 'admin_grant',
                    'session_reserve', 'session_refund', 'error_refund'
                )),
    session_id  UUID REFERENCES interview_sessions(id),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_ledger_user_created ON ledger_entries(user_id, created_at);
CREATE INDEX idx_ledger_session ON ledger_entries(session_id)
    WHERE session_id IS NOT NULL;

-- Track reserved minutes per session for refund calculation.
ALTER TABLE interview_sessions ADD COLUMN reserved_minutes INT;

-- Add 'failed' status for platform-error sessions that get full refunds.
ALTER TABLE interview_sessions DROP CONSTRAINT interview_sessions_status_check;
ALTER TABLE interview_sessions ADD CONSTRAINT interview_sessions_status_check
    CHECK (status IN ('active', 'completed', 'evaluating', 'reviewed', 'evaluation_failed', 'failed'));

-- Track free educator analysis taste-test usage (lifetime counter).
ALTER TABLE users ADD COLUMN free_full_educators_used INT NOT NULL DEFAULT 0;

-- usage_periods is superseded by grants + ledger.
DROP TABLE usage_periods;
