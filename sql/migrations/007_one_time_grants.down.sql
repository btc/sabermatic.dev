-- 007_one_time_grants.down.sql
-- Revert to monthly free grant model.

-- Restore monthly-scoped unique index.
DROP INDEX idx_grants_free_per_user;
CREATE UNIQUE INDEX idx_grants_free_per_month
    ON grants(user_id, date_trunc('month', timezone('UTC', created_at)))
    WHERE source = 'free_grant';

-- Revert 'free_trial' reason back to 'free_monthly'.
ALTER TABLE ledger_entries DROP CONSTRAINT ledger_entries_reason_check;
UPDATE ledger_entries SET reason = 'free_monthly' WHERE reason = 'free_trial';
ALTER TABLE ledger_entries ADD CONSTRAINT ledger_entries_reason_check
    CHECK (reason IN (
        'free_monthly', 'subscription_renewal', 'purchase', 'admin_grant',
        'session_reserve', 'session_refund', 'error_refund'
    ));
