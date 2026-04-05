-- 007_one_time_grants.up.sql
-- Change free grants from monthly renewal to one-time at account creation.

-- Replace monthly-scoped unique index with per-user unique index.
DROP INDEX idx_grants_free_per_month;
CREATE UNIQUE INDEX idx_grants_free_per_user
    ON grants(user_id)
    WHERE source = 'free_grant';

-- Add 'free_trial' reason and rename existing 'free_monthly' entries.
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_reason_check;
UPDATE ledger_entries SET reason = 'free_trial' WHERE reason = 'free_monthly';
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_reason_check
    CHECK (reason IN (
        'free_trial', 'subscription_renewal', 'purchase', 'admin_grant',
        'session_reserve', 'session_refund', 'error_refund'
    ));
