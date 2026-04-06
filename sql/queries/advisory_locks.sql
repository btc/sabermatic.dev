-- name: PGTryAdvisoryLock :one
SELECT pg_try_advisory_lock(@key);

-- name: PGAdvisoryUnlock :one
SELECT pg_advisory_unlock(@key);
