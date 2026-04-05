-- 007_rls.up.sql: Row-Level Security for tenant isolation.

-- Create a restricted role for the application.
-- NOTE: In production, set a password for drill_app via:
--   ALTER ROLE drill_app PASSWORD 'strong-password-here';
-- The password should come from secrets management, not this migration file.
-- Set DATABASE_APP_URL to use this role for the application pool.
DO $$
BEGIN
    IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'drill_app') THEN
        CREATE ROLE drill_app LOGIN;
    END IF;
END
$$;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO drill_app;
GRANT USAGE ON ALL SEQUENCES IN SCHEMA public TO drill_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO drill_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE ON SEQUENCES TO drill_app;

-- Enable RLS on direct-user_id tables.
ALTER TABLE interview_sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE coach_analyses ENABLE ROW LEVEL SECURITY;
ALTER TABLE grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE ledger_entries ENABLE ROW LEVEL SECURITY;
ALTER TABLE llm_calls ENABLE ROW LEVEL SECURITY;
ALTER TABLE user_events ENABLE ROW LEVEL SECURITY;
ALTER TABLE questions ENABLE ROW LEVEL SECURITY;
ALTER TABLE oauth_accounts ENABLE ROW LEVEL SECURITY;
ALTER TABLE auth_sessions ENABLE ROW LEVEL SECURITY;

-- Helper: safely convert the session variable to uuid.
-- current_setting with missing_ok=true returns '' when unset.
-- NULLIF converts '' to NULL; NULL::uuid is valid and NULL = x is always
-- false, so unset → zero rows (fail-closed) without a cast error.
CREATE FUNCTION app_current_user_id() RETURNS uuid
    LANGUAGE sql STABLE
    AS $$ SELECT nullif(current_setting('app.current_user_id', true), '')::uuid $$;

CREATE POLICY user_isolation ON interview_sessions
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

CREATE POLICY user_isolation ON coach_analyses
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

CREATE POLICY user_isolation ON grants
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

CREATE POLICY user_isolation ON ledger_entries
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

CREATE POLICY user_isolation ON llm_calls
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

CREATE POLICY user_isolation ON user_events
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

CREATE POLICY questions_isolation ON questions
    USING (user_id IS NULL OR user_id = app_current_user_id())
    WITH CHECK (user_id IS NULL OR user_id = app_current_user_id());

CREATE POLICY user_isolation ON oauth_accounts
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());

CREATE POLICY user_isolation ON auth_sessions
    USING (user_id = app_current_user_id())
    WITH CHECK (user_id = app_current_user_id());
