CREATE TABLE keep_link_token_uses (
  token_hash  BYTEA       PRIMARY KEY,
  user_id     UUID        NOT NULL REFERENCES users(id),
  used_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
