CREATE TABLE widgets (
    id           UUID PRIMARY KEY,
    name         TEXT NOT NULL DEFAULT '',
    enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    count        INTEGER NOT NULL DEFAULT 0,
    small_count  SMALLINT NOT NULL DEFAULT 0,
    big_count    BIGINT NOT NULL DEFAULT 0,
    color        TEXT NOT NULL DEFAULT 'red'
                 CHECK (color IN ('red', 'blue')),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at   TIMESTAMPTZ
);
