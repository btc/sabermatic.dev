ALTER TABLE auth_sessions
  ADD COLUMN revoked_at TIMESTAMPTZ;

CREATE INDEX idx_auth_sessions_user_last_active
  ON auth_sessions(user_id, last_active DESC);
