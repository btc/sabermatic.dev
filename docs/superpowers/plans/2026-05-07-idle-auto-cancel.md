# Idle Auto-Cancel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Auto-cancel a Pro subscription via `cancel_at_period_end=true` when the user has been idle for two consecutive billing periods. No settings toggle — this is the default Sabermatic behavior. Reversible via email-link click or any authenticated activity during the cancel window.

**Architecture:** Event-driven via Stripe `invoice.upcoming` webhook (~T-7 days before each renewal). Cancel decision happens inside a per-user-locked DB transaction (`SELECT ... FOR UPDATE`) with a generic `stripe_webhook_dedup` table for retry-storm protection and period-keyed `user_events` rows for multi-firing dedup. Reversal paths verify Stripe state before acting (handles partial-failure cache drift). Activity signal = `MAX(last_active)` from soft-deleted `auth_sessions`.

**Tech Stack:**
- Go 1.25.x (`go.mod:5`); `github.com/stripe/stripe-go/v82` (v82.5.1)
- Postgres 16 with sqlc-generated queries
- River queue for async email send
- TypeScript/React frontend with ConnectRPC
- HMAC-SHA256 for keep-link tokens

**Spec:** `docs/superpowers/specs/2026-05-07-idle-auto-cancel-design.md` (commit `780ef4ea`).

**Key project conventions** (from `/Users/btc/Projects/src/drill/CLAUDE.md`):
- Frontend typecheck: `cd web && npx tsc -b` (NEVER bare `tsc --noEmit`)
- Full CI: `make test` (buf lint + codegen check + frontend tsc/lint/tests + backend tests)
- Test users via `backendtest.SeedUser` (handler tests) or `seedUser` (backend internal tests); never raw SQL inserts
- After UI-affecting commits: load in browser before declaring done
- `_ = err` for ignored errors; never blanket `nolint`
- After every code-modifying task, a separate review agent reads actual files and verifies against spec

---

## Phase 1: Schema

Each migration is a standalone task with paired `up.sql` / `down.sql`. After migration files land, sqlc generation must be re-run via `sqlc generate` (the existing project script — see `Makefile` if present, else use `sqlc generate` from the repo root).

### Task 1: Migration 015 — `auth_sessions` soft-delete

**Files:**
- Create: `sql/migrations/015_auth_sessions_soft_delete.up.sql`
- Create: `sql/migrations/015_auth_sessions_soft_delete.down.sql`
- Modify: `sql/queries/auth_sessions.sql`
- Modify: `sql/queries/users.sql` (rename existing logout-everywhere query for clarity)
- Modify: `internal/backend/auth.go:261` (sole caller of `DeleteAuthSession`)
- Test: `internal/backend/auth_test.go` (verify logout now soft-deletes; sessions persist for activity reads)

- [ ] **Step 1: Write the up migration**

```sql
-- 015_auth_sessions_soft_delete.up.sql
ALTER TABLE auth_sessions
  ADD COLUMN revoked_at TIMESTAMPTZ;

CREATE INDEX idx_auth_sessions_user_last_active
  ON auth_sessions(user_id, last_active DESC);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 015_auth_sessions_soft_delete.down.sql
DROP INDEX IF EXISTS idx_auth_sessions_user_last_active;
ALTER TABLE auth_sessions DROP COLUMN revoked_at;
```

- [ ] **Step 3: Update `sql/queries/auth_sessions.sql`**

Replace the file contents:

```sql
-- name: CreateAuthSession :one
INSERT INTO auth_sessions (user_id, token_hash, expires_at, ip_address, user_agent)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetAuthSessionByToken :one
-- Filters out soft-revoked sessions; only returns valid live sessions.
-- (Activity queries do NOT filter on revoked_at — see GetUserLastActive.)
SELECT s.*, u.email, u.display_name, u.role, u.plan, u.email_verified,
       u.created_at AS user_created_at
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.expires_at > NOW()
  AND s.revoked_at IS NULL
  AND u.deleted_at IS NULL;

-- name: TouchAuthSession :exec
UPDATE auth_sessions SET last_active = NOW()
WHERE id = $1;

-- name: RevokeAuthSession :exec
-- Soft-delete: marks the row revoked but preserves it for the activity query.
UPDATE auth_sessions SET revoked_at = NOW()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeUserAuthSessions :exec
-- Soft-delete every active session for a user (logout-everywhere).
UPDATE auth_sessions SET revoked_at = NOW()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: GetUserLastActive :one
-- Reads across all history (including revoked sessions) — this is a
-- "when did the user last act, ever?" query, not a session-validity check.
SELECT MAX(last_active)::timestamptz
FROM auth_sessions
WHERE user_id = $1;
```

- [ ] **Step 4: Update `sql/queries/users.sql`**

Locate the existing `DELETE FROM auth_sessions WHERE user_id = @id;` query (the account-deletion path). Rename for clarity:

```sql
-- name: HardDeleteUserAuthSessions :exec
-- Used ONLY by account deletion (data minimization / GDPR). Logout uses
-- RevokeUserAuthSessions in auth_sessions.sql instead.
DELETE FROM auth_sessions WHERE user_id = @id;
```

- [ ] **Step 5: Update the sole caller in `internal/backend/auth.go:261`**

Find the call to `queries.DeleteAuthSession` (Logout path) and replace with `queries.RevokeAuthSession`. The signatures are identical (`exec` query taking the session ID).

Also locate any caller of `DeleteUserAuthSessions` (logout-everywhere paths) and rename to `RevokeUserAuthSessions`. Find with: `grep -rn "DeleteUserAuthSessions\|DeleteAuthSession" internal/`.

For the account-deletion caller, use `HardDeleteUserAuthSessions`.

- [ ] **Step 6: Run sqlc generate and verify**

```bash
sqlc generate
```

Expected: regenerates `internal/db/auth_sessions.sql.go` and `internal/db/users.sql.go` with new query method names.

- [ ] **Step 7: Run go build to confirm callsites compile**

```bash
go build ./...
```

Expected: builds clean. Any remaining caller of the old query names will fail compilation — fix in this task before committing.

- [ ] **Step 8: Write a test in `internal/backend/auth_test.go`**

Add this test alongside existing logout tests:

```go
func TestLogout_SoftDeletes_PreservesActivityHistory(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)

	// Sign in to create an auth session, then log out.
	// (Use whatever existing helper your project has for sign-in;
	//  search for an existing test that logs in then out and mirror it.)
	tok := signinHelper(t, b, userID)
	require.NoError(t, b.Logout(ctx, tok))

	// The session row should still exist (soft delete) with revoked_at set.
	var revokedAt sql.NullTime
	err := b.Pool().QueryRow(ctx,
		`SELECT revoked_at FROM auth_sessions WHERE user_id = $1`,
		userID).Scan(&revokedAt)
	require.NoError(t, err)
	require.True(t, revokedAt.Valid, "revoked_at should be set after logout")

	// GetUserLastActive should still return a value (across all history).
	last, err := db.New(b.Pool()).GetUserLastActive(ctx, userID)
	require.NoError(t, err)
	require.NotZero(t, last, "last_active must be visible across revoked sessions")
}
```

- [ ] **Step 9: Run the test to verify it passes**

```bash
go test ./internal/backend/ -run TestLogout_SoftDeletes_PreservesActivityHistory -race -count=1 -v
```

Expected: PASS.

- [ ] **Step 10: Run the full backend test suite to catch regressions**

```bash
go test ./internal/... ./cmd/... -race -count=1 -timeout=300s
```

Expected: all pass. If any existing test fails, the soft-delete change broke an assumption — fix in this task.

- [ ] **Step 11: Commit**

```bash
git add sql/migrations/015_*.sql sql/queries/auth_sessions.sql sql/queries/users.sql \
        internal/backend/auth.go internal/backend/auth_test.go \
        internal/db/auth_sessions.sql.go internal/db/users.sql.go internal/db/models.go
git commit -m "auth_sessions: soft-delete on logout (migration 015)"
```

---

### Task 2: Migration 016 — users subscription state cache

**Files:**
- Create: `sql/migrations/016_users_sub_state.up.sql`
- Create: `sql/migrations/016_users_sub_state.down.sql`
- Modify: `sql/queries/users.sql` (add new sub-state queries)
- Test: deferred to Task 5 where these columns are exercised end-to-end via AuthUser projection.

- [ ] **Step 1: Write the up migration**

```sql
-- 016_users_sub_state.up.sql
ALTER TABLE users
  ADD COLUMN stripe_subscription_id     TEXT,
  ADD COLUMN sub_cancel_at_period_end   BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN sub_cancel_is_auto         BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN sub_current_period_start   TIMESTAMPTZ,
  ADD COLUMN pending_kept_banner        BOOLEAN     NOT NULL DEFAULT FALSE,
  ADD COLUMN idle_eligible_after        TIMESTAMPTZ NOT NULL DEFAULT NOW();
```

- [ ] **Step 2: Write the down migration**

```sql
-- 016_users_sub_state.down.sql
ALTER TABLE users
  DROP COLUMN idle_eligible_after,
  DROP COLUMN pending_kept_banner,
  DROP COLUMN sub_current_period_start,
  DROP COLUMN sub_cancel_is_auto,
  DROP COLUMN sub_cancel_at_period_end,
  DROP COLUMN stripe_subscription_id;
```

- [ ] **Step 3: Append new queries to `sql/queries/users.sql`**

```sql
-- name: SetUserAutoCancelState :exec
-- Called by idleunsub.HandleInvoiceUpcoming when our trigger fires.
-- Sets BOTH cache flags so the auto-reverse middleware gate fires for this user.
UPDATE users
SET sub_cancel_at_period_end = TRUE,
    sub_cancel_is_auto       = TRUE,
    sub_current_period_start = $2
WHERE id = $1;

-- name: ClearUserAutoCancelState :exec
-- Called by KeepSubscription / AutoReverse on reversal. Sets banner flag.
UPDATE users
SET sub_cancel_at_period_end = FALSE,
    sub_cancel_is_auto       = FALSE,
    pending_kept_banner      = TRUE,
    sub_current_period_start = $2
WHERE id = $1;

-- name: SyncSubStateFromWebhook :exec
-- Called by handleSubscriptionUpdated. Does NOT touch sub_cancel_is_auto:
-- only our handler sets that flag; webhook sync must not overwrite it.
UPDATE users
SET stripe_subscription_id   = $2,
    sub_cancel_at_period_end = $3,
    sub_current_period_start = $4
WHERE id = $1;

-- name: ClearSubStateOnDeletion :exec
-- Called by handleSubscriptionDeleted. Clears all sub state and downgrades plan.
UPDATE users
SET stripe_subscription_id   = NULL,
    sub_cancel_at_period_end = FALSE,
    sub_cancel_is_auto       = FALSE,
    sub_current_period_start = NULL,
    plan                     = 'free'
WHERE id = $1;

-- name: ClearKeptBanner :exec
UPDATE users SET pending_kept_banner = FALSE WHERE id = $1;

-- name: ClearStaleKeptBanners :exec
-- Periodic hygiene: clear banners that are older than 14 days.
-- (Run from a tiny daily cron; the spec calls this out as low-priority cleanup.)
UPDATE users
SET pending_kept_banner = FALSE
WHERE pending_kept_banner = TRUE
  AND id IN (
    SELECT user_id FROM user_events
    WHERE event_type = 'subscription_kept'
      AND created_at < NOW() - INTERVAL '14 days'
  );

-- name: GetUserAutoCancelGates :one
-- Used by AutoReverse to defensively re-read both gates.
SELECT sub_cancel_at_period_end, sub_cancel_is_auto,
       stripe_subscription_id,   sub_current_period_start
FROM users
WHERE id = $1;

-- name: LockUserForSubDecision :one
-- Per-user mutex for the cancel transaction. Acquires a row-level lock that
-- serializes concurrent invoice.upcoming evaluations for the same user.
-- Must be inside a transaction; releases on COMMIT or ROLLBACK.
SELECT id FROM users WHERE id = $1 FOR UPDATE;
```

- [ ] **Step 4: Run sqlc generate**

```bash
sqlc generate
```

Expected: `internal/db/users.sql.go` gains the new methods; `internal/db/models.go` `User` struct gains the new fields.

- [ ] **Step 5: Run go build**

```bash
go build ./...
```

Expected: builds clean (no callers reference the new methods yet).

- [ ] **Step 6: Commit**

```bash
git add sql/migrations/016_*.sql sql/queries/users.sql \
        internal/db/users.sql.go internal/db/models.go
git commit -m "users: sub state cache columns (migration 016)"
```

---

### Task 3: Migration 017 — `stripe_webhook_dedup` table

**Files:**
- Create: `sql/migrations/017_stripe_webhook_dedup.up.sql`
- Create: `sql/migrations/017_stripe_webhook_dedup.down.sql`
- Create: `sql/queries/stripe_webhook_dedup.sql`

- [ ] **Step 1: Write the up migration**

```sql
-- 017_stripe_webhook_dedup.up.sql
CREATE TABLE stripe_webhook_dedup (
  event_id      TEXT        PRIMARY KEY,
  event_type    TEXT        NOT NULL,
  processed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 017_stripe_webhook_dedup.down.sql
DROP TABLE IF EXISTS stripe_webhook_dedup;
```

- [ ] **Step 3: Create `sql/queries/stripe_webhook_dedup.sql`**

```sql
-- name: TryClaimWebhookEvent :one
-- Returns the event_id on first claim; returns no row on subsequent claims.
-- Use the no-row return as the signal "this event was already handled".
INSERT INTO stripe_webhook_dedup (event_id, event_type)
VALUES ($1, $2)
ON CONFLICT (event_id) DO NOTHING
RETURNING event_id;
```

- [ ] **Step 4: Run sqlc generate and go build**

```bash
sqlc generate && go build ./...
```

Expected: clean build.

- [ ] **Step 5: Commit**

```bash
git add sql/migrations/017_*.sql sql/queries/stripe_webhook_dedup.sql \
        internal/db/stripe_webhook_dedup.sql.go internal/db/models.go
git commit -m "stripe_webhook_dedup: generic webhook idempotency (migration 017)"
```

---

### Task 4: Migration 018 — `keep_link_token_uses` + period-keyed dedup queries

**Files:**
- Create: `sql/migrations/018_keep_link_token_uses.up.sql`
- Create: `sql/migrations/018_keep_link_token_uses.down.sql`
- Create: `sql/queries/keep_link_token_uses.sql`
- Modify: `sql/queries/user_events.sql` (add the two period-keyed dedup queries; create the file if it doesn't exist)

- [ ] **Step 1: Write the up migration**

```sql
-- 018_keep_link_token_uses.up.sql
CREATE TABLE keep_link_token_uses (
  token_hash  BYTEA       PRIMARY KEY,
  user_id     UUID        NOT NULL REFERENCES users(id),
  used_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

- [ ] **Step 2: Write the down migration**

```sql
-- 018_keep_link_token_uses.down.sql
DROP TABLE IF EXISTS keep_link_token_uses;
```

- [ ] **Step 3: Create `sql/queries/keep_link_token_uses.sql`**

```sql
-- name: TryClaimKeepToken :one
-- Single-use enforcement. Returns the hash on first claim, no row otherwise.
INSERT INTO keep_link_token_uses (token_hash, user_id)
VALUES ($1, $2)
ON CONFLICT (token_hash) DO NOTHING
RETURNING token_hash;
```

- [ ] **Step 4: Add period-keyed dedup queries**

If `sql/queries/user_events.sql` exists, append; else create it with these queries:

```sql
-- name: HasAutoCanceledThisPeriod :one
-- Returns true if a subscription_auto_canceled event already exists for
-- this (user, subscription, period). Backs the multi-firing dedup in §4.1.
-- metadata->>'current_period_start' is RFC3339 text written by cancelMetadata.
SELECT EXISTS (
  SELECT 1 FROM user_events
  WHERE user_id = $1
    AND event_type = 'subscription_auto_canceled'
    AND (metadata->>'subscription_id') = $2
    AND (metadata->>'current_period_start')::timestamptz = $3
);

-- name: GetMostRecentKeptOrCanceledForPeriod :one
-- For multi-firing dedup: did a 'subscription_kept' event arrive after the
-- most recent 'subscription_auto_canceled' for this period? If yes, the user
-- already kept their sub for this period; don't re-cancel.
SELECT event_type
FROM user_events
WHERE user_id = $1
  AND event_type IN ('subscription_auto_canceled', 'subscription_kept')
  AND (metadata->>'subscription_id') = $2
  AND (metadata->>'current_period_start')::timestamptz = $3
ORDER BY created_at DESC
LIMIT 1;

-- name: InsertSubscriptionAutoCanceledEvent :exec
-- Composed insert for the cancel decision. metadata is already-marshaled JSON.
INSERT INTO user_events (user_id, event_type, metadata)
VALUES ($1, 'subscription_auto_canceled', $2);

-- name: InsertSubscriptionKeptEvent :exec
INSERT INTO user_events (user_id, event_type, metadata)
VALUES ($1, 'subscription_kept', $2);
```

- [ ] **Step 5: Run sqlc generate and go build**

```bash
sqlc generate && go build ./...
```

- [ ] **Step 6: Commit**

```bash
git add sql/migrations/018_*.sql sql/queries/keep_link_token_uses.sql \
        sql/queries/user_events.sql \
        internal/db/keep_link_token_uses.sql.go internal/db/user_events.sql.go \
        internal/db/models.go
git commit -m "keep_link_token_uses + user_events dedup queries (migration 018)"
```

---

## Phase 2: AuthUser extension

### Task 5: Project new sub-state columns into `AuthUser`

The auto-reverse hook in `Backend.AuthenticateSession` gates on `user.SubCancelAtPeriodEnd && user.SubCancelIsAuto`. The `AuthUser` struct must carry these.

**Files:**
- Modify: `internal/auth/user_context.go` (extend `AuthUser` struct)
- Modify: `sql/queries/auth_sessions.sql` (`GetAuthSessionByToken` projects new columns)
- Modify: `internal/backend/backend.go:267-285` (`AuthenticateSession` populates new fields)
- Test: `internal/backend/auth_test.go` (verify projection)

- [ ] **Step 1: Update `internal/auth/user_context.go`**

Add three fields to the `AuthUser` struct:

```go
type AuthUser struct {
	ID                   uuid.UUID
	Email                string
	DisplayName          string
	Role                 string
	Plan                 string
	EmailVerified        bool
	CreatedAt            time.Time

	// Auto-cancel state (migration 016).
	SubCancelAtPeriodEnd bool
	SubCancelIsAuto      bool
	PendingKeptBanner    bool
}
```

- [ ] **Step 2: Update `GetAuthSessionByToken` in `sql/queries/auth_sessions.sql`**

Extend the SELECT projection:

```sql
-- name: GetAuthSessionByToken :one
SELECT s.*, u.email, u.display_name, u.role, u.plan, u.email_verified,
       u.created_at AS user_created_at,
       u.sub_cancel_at_period_end, u.sub_cancel_is_auto, u.pending_kept_banner
FROM auth_sessions s
JOIN users u ON u.id = s.user_id
WHERE s.token_hash = $1
  AND s.expires_at > NOW()
  AND s.revoked_at IS NULL
  AND u.deleted_at IS NULL;
```

- [ ] **Step 3: Run sqlc generate**

```bash
sqlc generate
```

Expected: `GetAuthSessionByTokenRow` struct gains the three new fields.

- [ ] **Step 4: Update `Backend.AuthenticateSession` in `internal/backend/backend.go`**

Find the function (around line 267). After `row, err := queries.GetAuthSessionByToken(...)`, populate the new fields when constructing the returned `AuthUser`:

```go
return &auth.AuthUser{
	ID:                   row.UserID,
	Email:                row.Email,
	DisplayName:          row.DisplayName,
	Role:                 row.Role,
	Plan:                 row.Plan,
	EmailVerified:        row.EmailVerified,
	CreatedAt:            row.UserCreatedAt,
	SubCancelAtPeriodEnd: row.SubCancelAtPeriodEnd,
	SubCancelIsAuto:      row.SubCancelIsAuto,
	PendingKeptBanner:    row.PendingKeptBanner,
}, nil
```

(Match the existing field-assignment style; the new fields are appended.)

- [ ] **Step 5: Write a test**

In `internal/backend/auth_test.go` add:

```go
func TestAuthenticateSession_ProjectsSubState(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)

	// Force the cache columns to known values.
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET sub_cancel_at_period_end = TRUE,
		    sub_cancel_is_auto       = TRUE,
		    pending_kept_banner      = TRUE
		WHERE id = $1`, userID)
	require.NoError(t, err)

	tok := signinHelper(t, b, userID) // existing helper; mirror existing tests
	tokenHash := auth.HashSessionToken(tok)

	user, err := b.AuthenticateSession(ctx, tokenHash)
	require.NoError(t, err)
	require.True(t, user.SubCancelAtPeriodEnd)
	require.True(t, user.SubCancelIsAuto)
	require.True(t, user.PendingKeptBanner)
}
```

- [ ] **Step 6: Run the test**

```bash
go test ./internal/backend/ -run TestAuthenticateSession_ProjectsSubState -race -count=1 -v
```

Expected: PASS.

- [ ] **Step 7: Run all backend tests**

```bash
go test ./internal/... ./cmd/... -race -count=1 -timeout=300s
```

Expected: all pass.

- [ ] **Step 8: Commit**

```bash
git add internal/auth/user_context.go sql/queries/auth_sessions.sql \
        internal/backend/backend.go internal/backend/auth_test.go \
        internal/db/auth_sessions.sql.go
git commit -m "auth: project sub_cancel_at_period_end and friends into AuthUser"
```

---

## Phase 3: idleunsub package

The feature package home. New convention: `internal/feat/<feature>/` for cohesive feature packages. This is the first one.

### Task 6: Token sign/verify

**Files:**
- Create: `internal/feat/idleunsub/token.go`
- Create: `internal/feat/idleunsub/token_test.go`

- [ ] **Step 1: Write the failing test first**

Create `internal/feat/idleunsub/token_test.go`:

```go
package idleunsub_test

import (
	"crypto/rand"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/btc/drill/internal/feat/idleunsub"
)

func newSigner(t *testing.T) *idleunsub.TokenSigner {
	t.Helper()
	key := make([]byte, 32)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return idleunsub.NewTokenSigner(key)
}

func TestSignVerify_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	now := time.Now().UTC().Truncate(time.Second)
	end := now.Add(7 * 24 * time.Hour)

	claims := idleunsub.KeepTokenClaims{
		UserID:           uuid.New(),
		SubscriptionID:   "sub_123",
		Action:           "keep_subscription",
		CurrentPeriodEnd: end,
		IssuedAt:         now.Unix(),
		ExpiresAt:        end.Unix(),
	}

	tok := s.Sign(claims)
	got, err := s.Verify(tok)
	require.NoError(t, err)
	require.Equal(t, claims.UserID, got.UserID)
	require.Equal(t, claims.SubscriptionID, got.SubscriptionID)
	require.Equal(t, claims.Action, got.Action)
	require.True(t, claims.CurrentPeriodEnd.Equal(got.CurrentPeriodEnd))
	require.Equal(t, claims.ExpiresAt, got.ExpiresAt)
}

func TestVerify_RejectsTampered(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	tok := s.Sign(idleunsub.KeepTokenClaims{
		UserID: uuid.New(), SubscriptionID: "sub_x", Action: "keep_subscription",
		CurrentPeriodEnd: time.Now().Add(time.Hour).UTC(), ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	tampered := tok[:len(tok)-2] + "AA" // mutate last two characters
	_, err := s.Verify(tampered)
	require.ErrorIs(t, err, idleunsub.ErrTokenInvalid)
}

func TestVerify_RejectsExpired(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	past := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	tok := s.Sign(idleunsub.KeepTokenClaims{
		UserID: uuid.New(), SubscriptionID: "sub_x", Action: "keep_subscription",
		CurrentPeriodEnd: past, IssuedAt: past.Add(-24 * time.Hour).Unix(), ExpiresAt: past.Unix(),
	})
	_, err := s.Verify(tok)
	require.ErrorIs(t, err, idleunsub.ErrTokenExpired)
}

func TestVerify_RejectsExpDriftFromPeriodEnd(t *testing.T) {
	t.Parallel()
	s := newSigner(t)
	end := time.Now().Add(time.Hour).UTC().Truncate(time.Second)

	claims := idleunsub.KeepTokenClaims{
		UserID: uuid.New(), SubscriptionID: "sub_x", Action: "keep_subscription",
		CurrentPeriodEnd: end,
		IssuedAt:         time.Now().Unix(),
		ExpiresAt:        end.Add(24 * time.Hour).Unix(), // drift!
	}
	tok := s.Sign(claims)
	_, err := s.Verify(tok)
	require.ErrorIs(t, err, idleunsub.ErrTokenInvalid)
}
```

- [ ] **Step 2: Run the test to verify failure**

```bash
go test ./internal/feat/idleunsub/ -run TestSignVerify -v
```

Expected: FAIL — package doesn't exist yet.

- [ ] **Step 3: Implement `internal/feat/idleunsub/token.go`**

```go
package idleunsub

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrTokenInvalid = errors.New("idleunsub: token invalid")
	ErrTokenExpired = errors.New("idleunsub: token expired")
)

// KeepTokenClaims is the canonical signed payload. Field order is fixed by
// struct definition; encoding/json over a typed struct produces a stable
// serialization (no map iteration order ambiguity).
type KeepTokenClaims struct {
	UserID           uuid.UUID `json:"user_id"`
	SubscriptionID   string    `json:"subscription_id"`
	Action           string    `json:"action"`            // "keep_subscription"
	CurrentPeriodEnd time.Time `json:"current_period_end"`
	IssuedAt         int64     `json:"iat"`
	ExpiresAt        int64     `json:"exp"` // INVARIANT: == CurrentPeriodEnd.Unix()
}

// TokenSigner signs and verifies KeepTokenClaims with HMAC-SHA256.
type TokenSigner struct {
	key []byte
	now func() time.Time
}

// NewTokenSigner constructs a signer with the given HMAC key.
func NewTokenSigner(key []byte) *TokenSigner {
	return &TokenSigner{key: key, now: func() time.Time { return time.Now() }}
}

// Sign serializes and HMAC-signs the claims, returning a URL-safe base64
// string of the form "<payload_b64>.<sig_b64>".
func (s *TokenSigner) Sign(c KeepTokenClaims) string {
	body, _ := json.Marshal(c) // typed struct never errors
	bodyB64 := base64.RawURLEncoding.EncodeToString(body)
	sig := hmac.New(sha256.New, s.key)
	sig.Write([]byte(bodyB64))
	sigB64 := base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
	return bodyB64 + "." + sigB64
}

// Verify parses and validates a token. Returns the claims or an error.
//   - ErrTokenInvalid:  bad signature, malformed, or invariant violated
//   - ErrTokenExpired:  signature valid but exp < now
func (s *TokenSigner) Verify(tok string) (KeepTokenClaims, error) {
	var zero KeepTokenClaims
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return zero, ErrTokenInvalid
	}
	bodyB64, sigB64 := parts[0], parts[1]

	expectedSig := hmac.New(sha256.New, s.key)
	expectedSig.Write([]byte(bodyB64))
	givenSig, err := base64.RawURLEncoding.DecodeString(sigB64)
	if err != nil || !hmac.Equal(givenSig, expectedSig.Sum(nil)) {
		return zero, ErrTokenInvalid
	}

	body, err := base64.RawURLEncoding.DecodeString(bodyB64)
	if err != nil {
		return zero, ErrTokenInvalid
	}
	var c KeepTokenClaims
	if err := json.Unmarshal(body, &c); err != nil {
		return zero, ErrTokenInvalid
	}

	// Spec invariant: ExpiresAt must equal CurrentPeriodEnd.Unix().
	if c.ExpiresAt != c.CurrentPeriodEnd.Unix() {
		return zero, ErrTokenInvalid
	}

	if s.now().Unix() >= c.ExpiresAt {
		return zero, ErrTokenExpired
	}
	return c, nil
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/feat/idleunsub/ -race -count=1 -v
```

Expected: all four tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feat/idleunsub/token.go internal/feat/idleunsub/token_test.go
git commit -m "idleunsub: HMAC keep-token sign/verify"
```

---

### Task 7: Service skeleton + `HandleInvoiceUpcoming` (the cancel decision)

This is the largest task. It builds the trigger evaluation and the entire cancel transaction. Tests are integration-shaped (live DB, stubbed Stripe).

**Files:**
- Create: `internal/feat/idleunsub/idleunsub.go` (Service + HandleInvoiceUpcoming)
- Create: `internal/feat/idleunsub/metadata.go` (cancelMetadata / keptMetadata structs)
- Create: `internal/feat/idleunsub/metrics.go` (OTEL counter wrappers)
- Create: `internal/feat/idleunsub/idleunsub_test.go`
- Create: `internal/feat/idleunsub/stripestub_test.go` (in-package fake Stripe client used only by tests)

**Stripe test pattern note:** `internal/backend/billing_test.go` imports `stripe-go/v82` directly and constructs Stripe events via fixture data. We'll mirror that: hand-construct `stripe.Event` and `stripe.Subscription` payloads in tests; the Service takes a `StripeClient` interface so tests inject a fake.

- [ ] **Step 1: Define the StripeClient interface and metadata structs**

Create `internal/feat/idleunsub/metadata.go`:

```go
package idleunsub

import "time"

// All timestamps are canonicalized to second precision via
// time.Unix(stripeInt64, 0).UTC() before assignment. Stripe period fields
// arrive as int64 Unix seconds; explicit second precision and UTC guard
// against future drift if a code path ever constructs a time.Time from a
// different source. JSON encoding via encoding/json produces RFC3339
// ("...Z") which Postgres ::timestamptz parses reliably.

type cancelMetadata struct {
	SubscriptionID     string    `json:"subscription_id"`
	StripeEventID      string    `json:"stripe_event_id"`
	CurrentPeriodStart time.Time `json:"current_period_start"`
	CurrentPeriodEnd   time.Time `json:"current_period_end"`
}

type keptMetadata struct {
	SubscriptionID     string    `json:"subscription_id"`
	Via                string    `json:"via"`                  // "link" | "auto_activity"
	CurrentPeriodStart time.Time `json:"current_period_start"` // identifies the period kept
}
```

- [ ] **Step 2: Define the metrics shim**

Create `internal/feat/idleunsub/metrics.go`:

```go
package idleunsub

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter           = otel.Meter("idleunsub")
	cancelFired, _  = meter.Int64Counter("idleunsub.cancel.fired")
	cancelSkipped, _ = meter.Int64Counter("idleunsub.cancel.skipped")
	cancelError, _  = meter.Int64Counter("idleunsub.cancel.error")
	cacheDrift, _   = meter.Int64Counter("idleunsub.cancel.cache_drift_corrected")
	reverseLink, _  = meter.Int64Counter("idleunsub.reverse.link")
	reverseAct, _   = meter.Int64Counter("idleunsub.reverse.activity")
	emailEnqueue, _ = meter.Int64Counter("idleunsub.email.enqueue")
)

func mCancelFired(ctx context.Context, subID string) {
	cancelFired.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}
func mCancelSkipped(ctx context.Context, reason string) {
	cancelSkipped.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}
func mCancelError(ctx context.Context, reason string) {
	cancelError.Add(ctx, 1, metric.WithAttributes(attribute.String("reason", reason)))
}
func mCacheDriftCorrected(ctx context.Context, subID string) {
	cacheDrift.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}
func mReverseLink(ctx context.Context, subID string) {
	reverseLink.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}
func mReverseActivity(ctx context.Context, subID string) {
	reverseAct.Add(ctx, 1, metric.WithAttributes(attribute.String("sub_id", subID)))
}
func mEmailEnqueue(ctx context.Context, kind, status string) {
	emailEnqueue.Add(ctx, 1, metric.WithAttributes(
		attribute.String("kind", kind),
		attribute.String("status", status)))
}
```

- [ ] **Step 3: Define the Service struct + StripeClient interface**

Create `internal/feat/idleunsub/idleunsub.go`:

```go
package idleunsub

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/email"
)

// StripeClient is the surface idleunsub needs from Stripe. Production wires
// this to wrappers around stripe-go's package-level functions; tests inject
// an in-memory fake.
type StripeClient interface {
	GetSubscription(ctx context.Context, id string) (*stripe.Subscription, error)
	UpdateSubscriptionCancel(ctx context.Context, id string, cancelAtPeriodEnd bool, idempotencyKey string) (*stripe.Subscription, error)
}

// Service owns the cancel/keep/auto-reverse logic.
type Service struct {
	pool   *pgxpool.Pool
	stripe StripeClient
	mailer email.Sender
	signer *TokenSigner
	now    func() time.Time
	log    *slog.Logger
}

func NewService(pool *pgxpool.Pool, sc StripeClient, m email.Sender, sn *TokenSigner, log *slog.Logger) *Service {
	return &Service{pool: pool, stripe: sc, mailer: m, signer: sn, now: time.Now, log: log}
}
```

- [ ] **Step 4: Implement `HandleInvoiceUpcoming`**

Append to `idleunsub.go`:

```go
// HandleInvoiceUpcoming evaluates the trigger rule. Idempotent: safe to call
// multiple times for the same Stripe event.
func (s *Service) HandleInvoiceUpcoming(ctx context.Context, event stripe.Event) error {
	// Extract subscription ID from the invoice.upcoming event payload.
	subID := event.GetObjectValue("subscription")
	if subID == "" {
		s.log.Warn("invoice.upcoming missing subscription", "event_id", event.ID)
		return nil
	}

	// 1. Fetch the subscription (authoritative source for current period and status).
	sub, err := s.stripe.GetSubscription(ctx, subID)
	if err != nil {
		mCancelError(ctx, "stripe_get_failed")
		return fmt.Errorf("get subscription %s: %w", subID, err)
	}

	// 2. Status / state early returns.
	if sub.Status != stripe.SubscriptionStatusActive {
		mCancelSkipped(ctx, "non_active_status")
		return nil
	}
	if sub.CancelAtPeriodEnd {
		mCancelSkipped(ctx, "already_canceled")
		return nil
	}
	if len(sub.Items.Data) == 0 {
		s.log.Warn("subscription has no items", "sub_id", subID)
		return nil
	}
	item := sub.Items.Data[0]
	periodStart := time.Unix(item.CurrentPeriodStart, 0).UTC()
	periodEnd := time.Unix(item.CurrentPeriodEnd, 0).UTC()
	if item.Price == nil || item.Price.Recurring == nil {
		s.log.Warn("subscription item missing price.recurring", "sub_id", subID)
		return nil
	}
	threshold := subtractInterval(periodStart, item.Price.Recurring.Interval)

	// 3. Look up our user by stripe customer ID.
	custID := sub.Customer.ID
	q := db.New(s.pool)
	user, err := q.GetUserByStripeCustomer(ctx, pgxText(custID))
	if err != nil {
		return fmt.Errorf("get user by stripe customer %s: %w", custID, err)
	}

	// 4. Begin the cancel transaction.
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		mCancelError(ctx, "db_begin_failed")
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // rollback after commit is a no-op

	qtx := db.New(tx)

	// 4a. Per-user mutex.
	if _, err := qtx.LockUserForSubDecision(ctx, user.ID); err != nil {
		mCancelError(ctx, "db_lock_failed")
		return fmt.Errorf("lock user: %w", err)
	}

	// 4b. Webhook event dedup (retry-storm protection).
	claimed, err := qtx.TryClaimWebhookEvent(ctx, db.TryClaimWebhookEventParams{
		EventID: event.ID, EventType: string(event.Type),
	})
	if err == pgx.ErrNoRows {
		mCancelSkipped(ctx, "duplicate_event")
		return nil
	}
	if err != nil {
		mCancelError(ctx, "db_dedup_failed")
		return fmt.Errorf("claim webhook event: %w", err)
	}
	_ = claimed // we got the row; proceed

	// 4c. Period-keyed dedup queries.
	mostRecent, err := qtx.GetMostRecentKeptOrCanceledForPeriod(ctx,
		db.GetMostRecentKeptOrCanceledForPeriodParams{
			UserID: user.ID, SubID: subID, PeriodStart: pgxTime(periodStart),
		})
	if err != nil && err != pgx.ErrNoRows {
		mCancelError(ctx, "db_dedup_query_failed")
		return fmt.Errorf("most recent decision: %w", err)
	}
	if mostRecent == "subscription_kept" {
		mCancelSkipped(ctx, "already_kept_this_period")
		return nil
	}
	hasCanceled, err := qtx.HasAutoCanceledThisPeriod(ctx,
		db.HasAutoCanceledThisPeriodParams{
			UserID: user.ID, SubID: subID, PeriodStart: pgxTime(periodStart),
		})
	if err != nil {
		mCancelError(ctx, "db_dedup_query_failed")
		return fmt.Errorf("has auto canceled: %w", err)
	}
	if hasCanceled {
		mCancelSkipped(ctx, "already_canceled_this_period")
		return nil
	}

	// 4d. Read state and compute trigger.
	lastActive, err := qtx.GetUserLastActive(ctx, user.ID)
	if err != nil {
		return fmt.Errorf("get last active: %w", err)
	}
	if !lastActive.Valid {
		mCancelSkipped(ctx, "no_activity_history")
		return nil
	}
	if !lastActive.Time.Before(threshold) {
		mCancelSkipped(ctx, "active_in_window")
		return nil
	}
	if periodStart.Before(user.IdleEligibleAfter) {
		mCancelSkipped(ctx, "grandfathered")
		return nil
	}

	// 5. Trigger fires: insert audit row + cache update inside the TX.
	mdJSON, err := marshalCancelMetadata(subID, event.ID, periodStart, periodEnd)
	if err != nil {
		return fmt.Errorf("marshal cancel metadata: %w", err)
	}
	if err := qtx.InsertSubscriptionAutoCanceledEvent(ctx,
		db.InsertSubscriptionAutoCanceledEventParams{
			UserID: user.ID, Metadata: mdJSON,
		}); err != nil {
		return fmt.Errorf("insert event row: %w", err)
	}
	if err := qtx.SetUserAutoCancelState(ctx, db.SetUserAutoCancelStateParams{
		ID: user.ID, SubCurrentPeriodStart: pgxTime(periodStart),
	}); err != nil {
		return fmt.Errorf("set cache: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		mCancelError(ctx, "db_commit_failed")
		return fmt.Errorf("commit: %w", err)
	}

	// 6. Stripe call OUTSIDE the transaction. Idempotency key keeps retries safe.
	if _, err := s.stripe.UpdateSubscriptionCancel(ctx, subID, true, event.ID); err != nil {
		mCancelError(ctx, "stripe_update_failed")
		s.log.Error("stripe update failed after commit",
			"sub_id", subID, "event_id", event.ID, "err", err)
		// Cache is now ahead of Stripe. AutoReverse re-checks Stripe state,
		// so this drift will self-heal on the user's next authed request.
		return fmt.Errorf("stripe update: %w", err)
	}

	mCancelFired(ctx, subID)

	// 7. Enqueue cancel email.
	if err := s.enqueueCancelEmail(ctx, user, subID, periodEnd); err != nil {
		mEmailEnqueue(ctx, "cancel", "error")
		s.log.Error("cancel email enqueue failed", "user_id", user.ID, "err", err)
	} else {
		mEmailEnqueue(ctx, "cancel", "ok")
	}
	return nil
}

// subtractInterval returns t minus one Stripe billing interval.
func subtractInterval(t time.Time, interval stripe.PriceRecurringInterval) time.Time {
	switch interval {
	case stripe.PriceRecurringIntervalDay:
		return t.AddDate(0, 0, -1)
	case stripe.PriceRecurringIntervalWeek:
		return t.AddDate(0, 0, -7)
	case stripe.PriceRecurringIntervalMonth:
		return t.AddDate(0, -1, 0)
	case stripe.PriceRecurringIntervalYear:
		return t.AddDate(-1, 0, 0)
	default:
		return t.AddDate(0, -1, 0) // safe default
	}
}

// Helpers (placeholders — implement based on existing project conventions
// for pgtype.Text / pgtype.Timestamptz):
func pgxText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: true} }
func pgxTime(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}
```

(Add the `import "github.com/jackc/pgx/v5/pgtype"` to the import block.)

- [ ] **Step 5: Implement `marshalCancelMetadata` and email enqueue stub**

Append to `idleunsub.go`:

```go
import "encoding/json"

func marshalCancelMetadata(subID, eventID string, start, end time.Time) ([]byte, error) {
	return json.Marshal(cancelMetadata{
		SubscriptionID:     subID,
		StripeEventID:      eventID,
		CurrentPeriodStart: start.UTC(),
		CurrentPeriodEnd:   end.UTC(),
	})
}

// enqueueCancelEmail composes and enqueues the cancel email.
// Implementation lands in Task 9 alongside the templates; for now this is a
// stub so the cancel path compiles. Task 9 replaces it with the real impl.
func (s *Service) enqueueCancelEmail(ctx context.Context, user db.User, subID string, periodEnd time.Time) error {
	return nil // placeholder; replaced in Task 9
}
```

- [ ] **Step 6: Add a sqlc query needed above (`GetUserByStripeCustomer`)**

This may already exist; if so, skip. Otherwise add to `sql/queries/users.sql`:

```sql
-- name: GetUserByStripeCustomer :one
SELECT * FROM users WHERE stripe_customer_id = $1 AND deleted_at IS NULL;
```

Run `sqlc generate` after.

- [ ] **Step 7: Write integration test for happy path**

Create `internal/feat/idleunsub/stripestub_test.go`:

```go
package idleunsub_test

import (
	"context"
	"testing"

	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/feat/idleunsub"
)

// fakeStripe is an in-memory StripeClient for tests.
type fakeStripe struct {
	subs           map[string]*stripe.Subscription
	updateCalls    []updateCall
	updateErr      error
}

type updateCall struct {
	ID                string
	CancelAtPeriodEnd bool
	IdempotencyKey    string
}

func (f *fakeStripe) GetSubscription(ctx context.Context, id string) (*stripe.Subscription, error) {
	if s, ok := f.subs[id]; ok {
		return s, nil
	}
	return nil, stripe.ErrInvalidRequest
}

func (f *fakeStripe) UpdateSubscriptionCancel(ctx context.Context, id string, cap bool, key string) (*stripe.Subscription, error) {
	f.updateCalls = append(f.updateCalls, updateCall{id, cap, key})
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	if s, ok := f.subs[id]; ok {
		s.CancelAtPeriodEnd = cap
		return s, nil
	}
	return nil, stripe.ErrInvalidRequest
}

var _ idleunsub.StripeClient = (*fakeStripe)(nil)
```

Then `internal/feat/idleunsub/idleunsub_test.go`:

```go
package idleunsub_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/feat/idleunsub"
)

func TestHandleInvoiceUpcoming_FiresCancel_WhenIdleTwoPeriods(t *testing.T) {
	t.Parallel()
	pgEnv := backendtest.NewPGEnv(t) // existing helper that gives a fresh DB
	b := pgEnv.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)

	// Set the user's Stripe customer ID and idle_eligible_after far in the past.
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET stripe_customer_id = $1,
		    idle_eligible_after = NOW() - INTERVAL '6 months',
		    plan = 'pro'
		WHERE id = $2`, "cus_test", userID)
	require.NoError(t, err)

	// Force MAX(last_active) to far in the past (3 months ago).
	_, err = b.Pool().Exec(ctx, `
		UPDATE auth_sessions SET last_active = NOW() - INTERVAL '3 months'
		WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Fake Stripe sub: monthly, current period started 23 days ago.
	now := time.Now().UTC().Truncate(time.Second)
	periodStart := now.AddDate(0, 0, -23)
	periodEnd := periodStart.AddDate(0, 1, 0)
	subID := "sub_test"
	fake := &fakeStripe{
		subs: map[string]*stripe.Subscription{
			subID: {
				ID:                subID,
				Status:            stripe.SubscriptionStatusActive,
				CancelAtPeriodEnd: false,
				Customer:          &stripe.Customer{ID: "cus_test"},
				Items: &stripe.SubscriptionItemList{Data: []*stripe.SubscriptionItem{{
					CurrentPeriodStart: periodStart.Unix(),
					CurrentPeriodEnd:   periodEnd.Unix(),
					Price: &stripe.Price{Recurring: &stripe.PriceRecurring{
						Interval: stripe.PriceRecurringIntervalMonth,
					}},
				}}},
			},
		},
	}

	svc := idleunsub.NewService(b.Pool(), fake, &nullMailer{}, nil, b.Logger())

	event := stripe.Event{
		ID:   "evt_1",
		Type: "invoice.upcoming",
		Data: &stripe.EventData{Object: map[string]interface{}{
			"subscription": subID,
		}},
	}

	require.NoError(t, svc.HandleInvoiceUpcoming(ctx, event))

	// Stripe Update was called with cancel_at_period_end=true.
	require.Len(t, fake.updateCalls, 1)
	require.True(t, fake.updateCalls[0].CancelAtPeriodEnd)
	require.Equal(t, "evt_1", fake.updateCalls[0].IdempotencyKey)

	// user_events row exists.
	var count int
	err = b.Pool().QueryRow(ctx, `
		SELECT COUNT(*) FROM user_events
		WHERE user_id = $1 AND event_type = 'subscription_auto_canceled'`,
		userID).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)

	// Cache flags flipped.
	var cap, isAuto bool
	err = b.Pool().QueryRow(ctx,
		`SELECT sub_cancel_at_period_end, sub_cancel_is_auto FROM users WHERE id = $1`,
		userID).Scan(&cap, &isAuto)
	require.NoError(t, err)
	require.True(t, cap)
	require.True(t, isAuto)

	// Webhook dedup row claimed.
	err = b.Pool().QueryRow(ctx,
		`SELECT COUNT(*) FROM stripe_webhook_dedup WHERE event_id = 'evt_1'`).Scan(&count)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

type nullMailer struct{}
func (nullMailer) Send(ctx context.Context, m email.Message) error { return nil }
```

(`backendtest.NewPGEnv` may need to be added if not present; if the project uses a different convention for spinning up a test DB, mirror what `internal/backend/billing_test.go` uses — typically `pg.NewBackend(t)`.)

- [ ] **Step 8: Run the test to verify it passes**

```bash
go test ./internal/feat/idleunsub/ -race -count=1 -v
```

Expected: PASS.

- [ ] **Step 9: Add the early-return tests**

Append to `idleunsub_test.go`:

```go
func TestHandleInvoiceUpcoming_SkipsTrialing(t *testing.T) {
	// Same setup but sub.Status = trialing → no Stripe Update call, no event row.
	// Pattern: copy TestHandleInvoiceUpcoming_FiresCancel_WhenIdleTwoPeriods,
	// change sub.Status, assert len(fake.updateCalls) == 0 and no user_events row.
	// (Spec §3 early returns.)
}

func TestHandleInvoiceUpcoming_SkipsAlreadyCanceled(t *testing.T) {
	// sub.CancelAtPeriodEnd = true at fetch time → skip.
}

func TestHandleInvoiceUpcoming_SkipsGrandfathered(t *testing.T) {
	// idle_eligible_after = NOW() + 1 month (future) → skip even when idle.
}

func TestHandleInvoiceUpcoming_DedupesRetryStorm(t *testing.T) {
	// Call twice with same event.ID → second call is a no-op (one update call total).
}

func TestHandleInvoiceUpcoming_DedupesPostKeep(t *testing.T) {
	// Insert a subscription_kept user_events row for this period BEFORE calling
	// HandleInvoiceUpcoming with a different event.ID → no re-cancel.
}

func TestHandleInvoiceUpcoming_NoActivityHistory(t *testing.T) {
	// User with no auth_sessions rows → MAX(last_active) is NULL → defensive skip.
	// (Spec §8.1: "should be unreachable for a Pro user, but defensive".)
}

func TestHandleInvoiceUpcoming_NotIdleStill(t *testing.T) {
	// Activity within current period → trigger predicate false → skip.
}

func TestHandleInvoiceUpcoming_FirstPeriodGrace(t *testing.T) {
	// User just signed up; threshold predates their first activity → no cancel.
}
```

Implement each test by mirroring the happy-path test with the relevant precondition adjusted. Run all of them:

```bash
go test ./internal/feat/idleunsub/ -race -count=1 -v
```

Expected: all PASS.

- [ ] **Step 10: Commit**

```bash
git add internal/feat/idleunsub/idleunsub.go \
        internal/feat/idleunsub/metadata.go \
        internal/feat/idleunsub/metrics.go \
        internal/feat/idleunsub/idleunsub_test.go \
        internal/feat/idleunsub/stripestub_test.go \
        sql/queries/users.sql internal/db/users.sql.go
git commit -m "idleunsub: HandleInvoiceUpcoming + transactional cancel decision"
```

---

### Task 8: `KeepSubscription` and `AutoReverse`

**Files:**
- Modify: `internal/feat/idleunsub/idleunsub.go` (add two methods)
- Modify: `internal/feat/idleunsub/idleunsub_test.go` (new tests)

- [ ] **Step 1: Implement `KeepSubscription`**

Append to `idleunsub.go`:

```go
// KeepSubscription reverses cancel_at_period_end after a verified, single-use
// keep-link click. The endpoint is responsible for verifying the token AND
// claiming single-use BEFORE invoking this method. Refuses to act if the
// stored cache says the cancel is NOT our auto-cancel (manual portal cancel).
func (s *Service) KeepSubscription(ctx context.Context, claims KeepTokenClaims) error {
	q := db.New(s.pool)
	gates, err := q.GetUserAutoCancelGates(ctx, claims.UserID)
	if err != nil {
		return fmt.Errorf("read gates: %w", err)
	}
	if !gates.SubCancelAtPeriodEnd || !gates.SubCancelIsAuto {
		// Either the cancel was already reversed, or it's a manual portal cancel.
		// Don't touch Stripe; render confirmation page idempotently.
		return nil
	}
	updated, err := s.stripe.UpdateSubscriptionCancel(ctx, claims.SubscriptionID, false, "")
	if err != nil {
		return fmt.Errorf("stripe reverse: %w", err)
	}
	periodStart := time.Unix(updated.Items.Data[0].CurrentPeriodStart, 0).UTC()

	if err := q.ClearUserAutoCancelState(ctx, db.ClearUserAutoCancelStateParams{
		ID: claims.UserID, SubCurrentPeriodStart: pgxTime(periodStart),
	}); err != nil {
		return fmt.Errorf("clear gates: %w", err)
	}

	mdJSON, err := marshalKeptMetadata(claims.SubscriptionID, "link", periodStart)
	if err != nil {
		return fmt.Errorf("marshal kept metadata: %w", err)
	}
	if err := q.InsertSubscriptionKeptEvent(ctx, db.InsertSubscriptionKeptEventParams{
		UserID: claims.UserID, Metadata: mdJSON,
	}); err != nil {
		return fmt.Errorf("insert kept event: %w", err)
	}

	mReverseLink(ctx, claims.SubscriptionID)

	if err := s.enqueueKeptEmail(ctx, claims.UserID, claims.SubscriptionID, claims.CurrentPeriodEnd); err != nil {
		mEmailEnqueue(ctx, "kept", "error")
		s.log.Error("kept email enqueue failed", "user_id", claims.UserID, "err", err)
	} else {
		mEmailEnqueue(ctx, "kept", "ok")
	}
	return nil
}

func marshalKeptMetadata(subID, via string, periodStart time.Time) ([]byte, error) {
	return json.Marshal(keptMetadata{
		SubscriptionID:     subID,
		Via:                via,
		CurrentPeriodStart: periodStart.UTC(),
	})
}

func (s *Service) enqueueKeptEmail(ctx context.Context, userID uuid.UUID, subID string, periodEnd time.Time) error {
	return nil // placeholder; Task 9
}
```

- [ ] **Step 2: Implement `AutoReverse`**

Append:

```go
// AutoReverse is invoked by the auth middleware when an authenticated request
// arrives from a user whose cached gates are SubCancelAtPeriodEnd && SubCancelIsAuto.
// The current request itself is the activity signal; we do NOT re-read
// last_active (would race against TouchAuthSession).
//
// AutoReverse verifies Stripe state before acting. If Stripe says the sub is
// NOT canceled (cache drift from a prior partial failure), AutoReverse silently
// clears the cache and returns without sending email or inserting an event row.
func (s *Service) AutoReverse(ctx context.Context, userID uuid.UUID) error {
	q := db.New(s.pool)
	gates, err := q.GetUserAutoCancelGates(ctx, userID)
	if err != nil {
		return fmt.Errorf("read gates: %w", err)
	}
	if !gates.SubCancelAtPeriodEnd || !gates.SubCancelIsAuto || !gates.StripeSubscriptionID.Valid {
		return nil // cache says off, or no sub — nothing to do
	}
	subID := gates.StripeSubscriptionID.String

	// Verify Stripe state — handles the partial-failure window where our cache
	// says canceled but Stripe never confirmed.
	stripeSub, err := s.stripe.GetSubscription(ctx, subID)
	if err != nil {
		return fmt.Errorf("get sub: %w", err)
	}
	if !stripeSub.CancelAtPeriodEnd {
		// Cache drift. Silently correct and return without email/event.
		mCacheDriftCorrected(ctx, subID)
		periodStart := time.Unix(stripeSub.Items.Data[0].CurrentPeriodStart, 0).UTC()
		if err := q.ClearUserAutoCancelState(ctx, db.ClearUserAutoCancelStateParams{
			ID: userID, SubCurrentPeriodStart: pgxTime(periodStart),
		}); err != nil {
			return fmt.Errorf("clear cache (drift): %w", err)
		}
		// Note: ClearUserAutoCancelState sets pending_kept_banner=true even on drift
		// correction. That's wrong for this case — we don't want to celebrate a
		// reversal that didn't happen. Fix: use a dedicated clear-without-banner
		// query, or split ClearUserAutoCancelState into two variants.
		if err := q.ClearKeptBanner(ctx, userID); err != nil {
			return fmt.Errorf("clear banner (drift): %w", err)
		}
		return nil
	}

	// Real reversal.
	updated, err := s.stripe.UpdateSubscriptionCancel(ctx, subID, false, "")
	if err != nil {
		return fmt.Errorf("stripe reverse: %w", err)
	}
	periodStart := time.Unix(updated.Items.Data[0].CurrentPeriodStart, 0).UTC()
	periodEnd := time.Unix(updated.Items.Data[0].CurrentPeriodEnd, 0).UTC()

	if err := q.ClearUserAutoCancelState(ctx, db.ClearUserAutoCancelStateParams{
		ID: userID, SubCurrentPeriodStart: pgxTime(periodStart),
	}); err != nil {
		return fmt.Errorf("clear gates: %w", err)
	}

	mdJSON, err := marshalKeptMetadata(subID, "auto_activity", periodStart)
	if err != nil {
		return fmt.Errorf("marshal kept metadata: %w", err)
	}
	if err := q.InsertSubscriptionKeptEvent(ctx, db.InsertSubscriptionKeptEventParams{
		UserID: userID, Metadata: mdJSON,
	}); err != nil {
		return fmt.Errorf("insert kept event: %w", err)
	}

	mReverseActivity(ctx, subID)

	if err := s.enqueueKeptEmail(ctx, userID, subID, periodEnd); err != nil {
		mEmailEnqueue(ctx, "kept", "error")
		s.log.Error("kept email enqueue failed", "user_id", userID, "err", err)
	} else {
		mEmailEnqueue(ctx, "kept", "ok")
	}
	return nil
}
```

- [ ] **Step 3: Write tests**

Append to `idleunsub_test.go`:

```go
func TestKeepSubscription_HappyPath(t *testing.T) {
	// Seed an auto-canceled state. Call KeepSubscription. Assert:
	// - Stripe Update called with false
	// - cache flipped (sub_cancel_at_period_end=false, sub_cancel_is_auto=false)
	// - subscription_kept user_events row inserted with current_period_start
	// - pending_kept_banner = true
}

func TestKeepSubscription_RefusesManualCancel(t *testing.T) {
	// Seed sub_cancel_at_period_end=true, sub_cancel_is_auto=FALSE.
	// Call KeepSubscription. Assert: no Stripe call, no event row, no cache change.
}

func TestKeepSubscription_Idempotent(t *testing.T) {
	// Call twice. Second call is a no-op (cache already cleared after first).
	// One Stripe call, one event row, one email enqueued.
}

func TestAutoReverse_GateOff(t *testing.T) {
	// User with sub_cancel_at_period_end=false → AutoReverse is no-op.
	// (No Stripe call, no event row.)
}

func TestAutoReverse_HappyPath(t *testing.T) {
	// Seed auto-canceled state, Stripe state agrees (CancelAtPeriodEnd=true).
	// Call AutoReverse. Assert: Stripe Update called, cache cleared, banner set,
	// email enqueued, subscription_kept event with via='auto_activity'.
}

func TestAutoReverse_CacheDriftCorrected(t *testing.T) {
	// Seed auto-canceled state in DB, but Stripe says CancelAtPeriodEnd=false
	// (simulating partial-failure window).
	// Call AutoReverse. Assert: NO Stripe Update call, NO email,
	// NO subscription_kept event, cache silently cleared, banner NOT set.
}

func TestAutoReverse_RefusesManualCancel(t *testing.T) {
	// Seed sub_cancel_at_period_end=true but sub_cancel_is_auto=false.
	// AutoReverse must be a no-op — we don't touch a manual portal cancel.
}
```

Implement each test by setting up the appropriate state and calling the methods.

- [ ] **Step 4: Run tests**

```bash
go test ./internal/feat/idleunsub/ -race -count=1 -v
```

Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/feat/idleunsub/idleunsub.go internal/feat/idleunsub/idleunsub_test.go
git commit -m "idleunsub: KeepSubscription + AutoReverse with cache-drift correction"
```

---

### Task 9: Email composition + templates

**Files:**
- Create: `internal/feat/idleunsub/email.go`
- Create: `internal/feat/idleunsub/templates/cancel.html.tmpl`
- Create: `internal/feat/idleunsub/templates/cancel.txt.tmpl`
- Create: `internal/feat/idleunsub/templates/kept.html.tmpl`
- Create: `internal/feat/idleunsub/templates/kept.txt.tmpl`
- Create: `internal/feat/idleunsub/email_test.go`
- Modify: `internal/feat/idleunsub/idleunsub.go` (replace the placeholder enqueue functions)

- [ ] **Step 1: Create the cancel templates**

`internal/feat/idleunsub/templates/cancel.txt.tmpl`:

```
Subject: We won't charge you for the next period

Hi {{.FirstName}},

We noticed you haven't been around Sabermatic in the last two billing
periods, so we've stopped your auto-renewal. You'll keep access through
{{.CurrentPeriodEnd}} — we won't charge you for the next period.

If you'd like to keep your subscription active, one click does it:

  {{.KeepLink}}

If you're done for now, no action needed. We'll be here whenever you
want to come back.

— Sabermatic
```

`cancel.html.tmpl`: same content, marked up minimally (one `<p>` per paragraph; `<a href="{{.KeepLink}}">Keep my subscription</a>` for the link). Keep it simple — no styling beyond what's in the existing wrapper template (`internal/email/templates/wrapper.html`).

- [ ] **Step 2: Create the kept templates**

`internal/feat/idleunsub/templates/kept.txt.tmpl`:

```
Subject: Your subscription is still active

Hi {{.FirstName}},

You're all set — your Sabermatic subscription will renew normally on
{{.NextRenewalDate}}.

Welcome back.

— Sabermatic
```

`kept.html.tmpl`: same, minimal markup.

- [ ] **Step 3: Implement `email.go`**

```go
package idleunsub

import (
	"bytes"
	"context"
	"embed"
	"fmt"
	"html/template"
	"text/template"
	"time"

	"github.com/btc/drill/internal/email"
)

//go:embed templates/*
var templatesFS embed.FS

type cancelEmailData struct {
	FirstName        string
	CurrentPeriodEnd string // formatted like "March 1, 2026"
	KeepLink         string
}

type keptEmailData struct {
	FirstName       string
	NextRenewalDate string
}

// composeCancelEmail renders the cancel email and returns an email.Message.
func composeCancelEmail(toEmail, firstName, keepURL string, periodEnd time.Time) (email.Message, error) {
	data := cancelEmailData{
		FirstName:        firstName,
		CurrentPeriodEnd: periodEnd.Format("January 2, 2006"),
		KeepLink:         keepURL,
	}
	return renderEmail("cancel", toEmail, "We won't charge you for the next period", data)
}

func composeKeptEmail(toEmail, firstName string, nextRenewal time.Time) (email.Message, error) {
	data := keptEmailData{
		FirstName:       firstName,
		NextRenewalDate: nextRenewal.Format("January 2, 2006"),
	}
	return renderEmail("kept", toEmail, "Your subscription is still active", data)
}

func renderEmail(name, to, subject string, data interface{}) (email.Message, error) {
	htmlT, err := template.ParseFS(templatesFS, "templates/"+name+".html.tmpl")
	if err != nil {
		return email.Message{}, fmt.Errorf("parse %s.html: %w", name, err)
	}
	txtT, err := texttemplate.ParseFS(templatesFS, "templates/"+name+".txt.tmpl")
	if err != nil {
		return email.Message{}, fmt.Errorf("parse %s.txt: %w", name, err)
	}
	var htmlBuf, txtBuf bytes.Buffer
	if err := htmlT.Execute(&htmlBuf, data); err != nil {
		return email.Message{}, fmt.Errorf("execute %s.html: %w", name, err)
	}
	if err := txtT.Execute(&txtBuf, data); err != nil {
		return email.Message{}, fmt.Errorf("execute %s.txt: %w", name, err)
	}
	return email.Message{
		To: to, Subject: subject,
		HTMLBody: htmlBuf.String(), TextBody: txtBuf.String(),
	}, nil
}
```

(Note: the import block needs both `html/template` and `text/template` — alias them as the snippet shows: `template` for HTML, `texttemplate` for text. If your project already has a different rendering helper, mirror it.)

- [ ] **Step 4: Replace the placeholder enqueue functions in `idleunsub.go`**

```go
// Replace `enqueueCancelEmail` placeholder with:
func (s *Service) enqueueCancelEmail(ctx context.Context, user db.User, subID string, periodEnd time.Time) error {
	keepURL, err := s.buildKeepURL(user.ID, subID, periodEnd)
	if err != nil {
		return fmt.Errorf("build keep url: %w", err)
	}
	msg, err := composeCancelEmail(user.Email, user.FirstName(), keepURL, periodEnd)
	if err != nil {
		return fmt.Errorf("compose cancel: %w", err)
	}
	return s.mailer.Send(ctx, msg)
}

// Replace `enqueueKeptEmail`:
func (s *Service) enqueueKeptEmail(ctx context.Context, userID uuid.UUID, subID string, periodEnd time.Time) error {
	q := db.New(s.pool)
	user, err := q.GetUser(ctx, userID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	msg, err := composeKeptEmail(user.Email, user.FirstName(), periodEnd)
	if err != nil {
		return fmt.Errorf("compose kept: %w", err)
	}
	return s.mailer.Send(ctx, msg)
}

// buildKeepURL constructs the public keep-link URL with a signed token.
func (s *Service) buildKeepURL(userID uuid.UUID, subID string, periodEnd time.Time) (string, error) {
	if s.signer == nil {
		return "", fmt.Errorf("token signer not configured")
	}
	periodEnd = periodEnd.UTC().Truncate(time.Second)
	tok := s.signer.Sign(KeepTokenClaims{
		UserID:           userID,
		SubscriptionID:   subID,
		Action:           "keep_subscription",
		CurrentPeriodEnd: periodEnd,
		IssuedAt:         s.now().Unix(),
		ExpiresAt:        periodEnd.Unix(),
	})
	return fmt.Sprintf("https://sabermatic.dev/sub/keep?t=%s", tok), nil
}
```

(`db.User.FirstName()` may not exist. If display_name is "Jane Doe", split on first space; or use display_name directly. Inspect `internal/db/models.go` for the User struct and pick a sensible field.)

- [ ] **Step 5: Write tests**

`internal/feat/idleunsub/email_test.go`:

```go
package idleunsub

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestComposeCancelEmail_Renders(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	msg, err := composeCancelEmail("user@example.com", "Jane",
		"https://sabermatic.dev/sub/keep?t=abc.def", end)
	require.NoError(t, err)
	require.Equal(t, "user@example.com", msg.To)
	require.Contains(t, msg.TextBody, "Hi Jane,")
	require.Contains(t, msg.TextBody, "March 1, 2026")
	require.Contains(t, msg.TextBody, "https://sabermatic.dev/sub/keep?t=abc.def")
	require.Contains(t, msg.HTMLBody, "Jane")
}

func TestComposeKeptEmail_Renders(t *testing.T) {
	t.Parallel()
	end := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	msg, err := composeKeptEmail("user@example.com", "Jane", end)
	require.NoError(t, err)
	require.Contains(t, msg.TextBody, "March 1, 2026")
}
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/feat/idleunsub/ -race -count=1 -v
```

Expected: all PASS, including the previously written cancel/keep happy-path tests now exercising real email composition.

- [ ] **Step 7: Commit**

```bash
git add internal/feat/idleunsub/email.go \
        internal/feat/idleunsub/email_test.go \
        internal/feat/idleunsub/templates/ \
        internal/feat/idleunsub/idleunsub.go
git commit -m "idleunsub: email composition + templates"
```

---

## Phase 4: Integration

### Task 10: Stripe webhook integration

**Files:**
- Modify: `internal/backend/billing.go` (add `case "invoice.upcoming"` and `case "customer.subscription.created"`; extend `handleSubscriptionUpdated` to call `SyncSubStateFromWebhook`; add `handleSubscriptionDeleted` to call `ClearSubStateOnDeletion`)
- Modify: `internal/backend/backend.go` (wire `idleunsub.Service` into the `Backend` struct constructor)
- Test: `internal/backend/billing_test.go` (add tests for the new dispatch cases and the sync behavior)

- [ ] **Step 1: Wire `idleunsub.Service` into `Backend`**

In `internal/backend/backend.go`, find the `Backend` struct definition and add a field:

```go
type Backend struct {
	// ... existing fields ...
	idleunsub *idleunsub.Service
}
```

Find the `NewBackend` (or equivalent) constructor and accept/construct the service. The exact signature depends on existing setup; add the parameter at the end of the existing argument list. In `cmd/.../main.go` (or wherever Backend is constructed), instantiate `idleunsub.NewService` with the pool, a real Stripe client wrapper, the email sender, the token signer, and the logger.

- [ ] **Step 2: Add `case "invoice.upcoming"` to `HandleStripeWebhook`**

In `internal/backend/billing.go`, find the switch:

```go
switch event.Type {
case "checkout.session.completed":
	return b.handleCheckoutCompleted(ctx, event)
case "invoice.paid":
	return b.handleInvoicePaid(ctx, event)
case "invoice.upcoming": // NEW
	return b.idleunsub.HandleInvoiceUpcoming(ctx, event)
case "customer.subscription.created": // NEW — same body as updated
	return b.handleSubscriptionUpdated(ctx, event)
case "customer.subscription.deleted":
	return b.handleSubscriptionDeleted(ctx, event)
case "customer.subscription.updated":
	return b.handleSubscriptionUpdated(ctx, event)
default:
	slog.Info("unhandled stripe event", "type", event.Type, "id", event.ID)
	return nil
}
```

- [ ] **Step 3: Extend `handleSubscriptionUpdated` to call `SyncSubStateFromWebhook`**

After the existing body (which calls `UpdatePlanByStripeCustomer`), parse the subscription from the event and call:

```go
sub := &stripe.Subscription{}
if err := json.Unmarshal(event.Data.Raw, sub); err != nil {
	return fmt.Errorf("unmarshal subscription: %w", err)
}
custID := sub.Customer.ID
user, err := b.queries.GetUserByStripeCustomer(ctx, pgxText(custID))
if err != nil {
	return fmt.Errorf("get user by customer: %w", err)
}
var periodStart pgtype.Timestamptz
if len(sub.Items.Data) > 0 {
	periodStart = pgxTime(time.Unix(sub.Items.Data[0].CurrentPeriodStart, 0).UTC())
}
if err := b.queries.SyncSubStateFromWebhook(ctx, db.SyncSubStateFromWebhookParams{
	ID:                    user.ID,
	StripeSubscriptionID:  pgxText(sub.ID),
	SubCancelAtPeriodEnd:  sub.CancelAtPeriodEnd,
	SubCurrentPeriodStart: periodStart,
}); err != nil {
	return fmt.Errorf("sync sub state: %w", err)
}
```

The order matters: keep existing plan-update behavior, then sync. Both can fail independently — if plan update succeeds and sync fails, retry of the webhook will re-attempt sync (idempotent UPDATE).

**Note** the SyncSubStateFromWebhook query intentionally does NOT touch `sub_cancel_is_auto`. This means after our cancel path sets both flags, a subsequent webhook with `cancel_at_period_end=true` overwrites only `sub_cancel_at_period_end` (with the same `true` value) and `sub_cancel_is_auto` stays `true`. Round-trip correct.

- [ ] **Step 4: Add `handleSubscriptionDeleted` body (or extend existing)**

If a `handleSubscriptionDeleted` already exists, extend it; otherwise add:

```go
func (b *Backend) handleSubscriptionDeleted(ctx context.Context, event stripe.Event) error {
	sub := &stripe.Subscription{}
	if err := json.Unmarshal(event.Data.Raw, sub); err != nil {
		return fmt.Errorf("unmarshal subscription: %w", err)
	}
	user, err := b.queries.GetUserByStripeCustomer(ctx, pgxText(sub.Customer.ID))
	if err != nil {
		return fmt.Errorf("get user by customer: %w", err)
	}
	return b.queries.ClearSubStateOnDeletion(ctx, user.ID)
}
```

- [ ] **Step 5: Tests**

In `internal/backend/billing_test.go`, add:

```go
func TestHandleSubscriptionUpdated_SyncsCacheColumns(t *testing.T) {
	// Fire an updated event with cancel_at_period_end=true and a known
	// current_period_start. Assert users row reflects both.
}

func TestHandleSubscriptionUpdated_DoesNotTouchSubCancelIsAuto(t *testing.T) {
	// Pre-set users.sub_cancel_is_auto=true. Fire updated event.
	// Assert users.sub_cancel_is_auto is STILL true after sync.
}

func TestHandleSubscriptionDeleted_ClearsCache(t *testing.T) {
	// Pre-set all cache columns. Fire deleted event.
	// Assert users plan='free' and all cache columns cleared.
}

func TestWebhookSwitch_InvoiceUpcoming_RoutesToIdleunsub(t *testing.T) {
	// Construct a minimal invoice.upcoming event. Call HandleStripeWebhook.
	// Assert idleunsub.HandleInvoiceUpcoming was invoked (use a recorder
	// version of Service or fake Stripe and check the side effects).
}

func TestWebhookSwitch_SubscriptionCreated_RoutesToUpdated(t *testing.T) {
	// Construct subscription.created event. Assert SyncSubStateFromWebhook ran.
}
```

- [ ] **Step 6: Run all tests**

```bash
go test ./internal/... -race -count=1 -timeout=300s
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/backend/billing.go internal/backend/backend.go \
        internal/backend/billing_test.go cmd/
git commit -m "billing: route invoice.upcoming and subscription.created to idleunsub"
```

---

### Task 11: `/sub/keep` endpoint

**Files:**
- Create: `internal/handler/keep.go`
- Modify: `cmd/.../main.go` (or wherever HTTP routes are mounted) to register the new endpoint
- Create: `internal/handler/keep_test.go`

- [ ] **Step 1: Implement `keep.go`**

```go
package handler

import (
	"errors"
	"fmt"
	"html/template"
	"net/http"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/feat/idleunsub"
)

// KeepHandler serves GET /sub/keep?t=<token>. Mounted publicly (no RequireAuth).
type KeepHandler struct {
	signer *idleunsub.TokenSigner
	svc    *idleunsub.Service
	pool   *pgxpool.Pool // for the single-use claim query
}

func NewKeepHandler(signer *idleunsub.TokenSigner, svc *idleunsub.Service, pool *pgxpool.Pool) *KeepHandler {
	return &KeepHandler{signer: signer, svc: svc, pool: pool}
}

func (h *KeepHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("t")
	claims, err := h.signer.Verify(tok)
	switch {
	case errors.Is(err, idleunsub.ErrTokenInvalid):
		renderInvalidLink(w)
		return
	case errors.Is(err, idleunsub.ErrTokenExpired):
		renderPeriodEnded(w)
		return
	case err != nil:
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// Single-use claim.
	tokenHash := sha256Of(tok) // returns []byte
	claimed, err := db.New(h.pool).TryClaimKeepToken(ctx, db.TryClaimKeepTokenParams{
		TokenHash: tokenHash,
		UserID:    claims.UserID,
	})
	if err != nil && err != pgx.ErrNoRows {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if err == pgx.ErrNoRows {
		// Token was already used — render the same confirmation page using
		// claims.CurrentPeriodEnd (still in the verified token).
		renderKeptConfirmation(w, claims.CurrentPeriodEnd)
		return
	}
	_ = claimed

	if err := h.svc.KeepSubscription(r.Context(), claims); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	renderKeptConfirmation(w, claims.CurrentPeriodEnd)
}

// Render functions (use html/template; templates can live in
// internal/handler/templates/ or be inline strings — match existing convention).
func renderInvalidLink(w http.ResponseWriter)    { /* 400 */ }
func renderPeriodEnded(w http.ResponseWriter)    { /* 200, "your sub already ended" */ }
func renderKeptConfirmation(w http.ResponseWriter, end time.Time) { /* 200, "you're all set, next renewal: <end>" */ }
func sha256Of(s string) []byte                   { /* sha256 helper */ }
```

(Fill in the rendering helpers using minimal HTML; the spec doesn't require pretty pages but they should not be empty.)

- [ ] **Step 2: Mount the route**

In `cmd/.../main.go` (search for where other handlers like `/api/auth/...` are mounted), add:

```go
mux.Handle("GET /sub/keep", handler.NewKeepHandler(tokenSigner, idleunsubSvc, pool))
```

The route is mounted publicly — do NOT wrap in `RequireAuth`. The token IS the auth.

- [ ] **Step 3: Tests**

`internal/handler/keep_test.go`:

```go
func TestKeep_HappyPath(t *testing.T)       // valid token, first click → reverses + renders confirmation
func TestKeep_TamperedToken(t *testing.T)   // bad signature → 400
func TestKeep_ExpiredToken(t *testing.T)    // expired but valid signature → 200 with period-ended page
func TestKeep_Replay(t *testing.T)          // valid token used twice → second renders same page, no Stripe call
func TestKeep_RefusesManualCancel(t *testing.T) // sub_cancel_is_auto=false → no Stripe call (KeepSubscription handles this)
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/handler/ -race -count=1 -v
```

- [ ] **Step 5: Commit**

```bash
git add internal/handler/keep.go internal/handler/keep_test.go cmd/
git commit -m "handler: GET /sub/keep with single-use token claim"
```

---

### Task 12: AutoReverse hook in `Backend.AuthenticateSession`

**Files:**
- Modify: `internal/backend/backend.go:267-285` (add second fire-and-forget goroutine)
- Modify: `internal/backend/auth_test.go` (test that AutoReverse fires when gates are set)

- [ ] **Step 1: Add the goroutine**

In `Backend.AuthenticateSession`, immediately after the existing `TouchAuthSession` goroutine:

```go
// Existing TouchAuthSession goroutine — leave unchanged.
go func() {
	touchCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	queries.TouchAuthSession(touchCtx, row.ID)
}()

// NEW: AutoReverse hook for users with our auto-cancel set.
if row.SubCancelAtPeriodEnd && row.SubCancelIsAuto {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := b.idleunsub.AutoReverse(ctx, row.UserID); err != nil {
			b.log.Warn("auto-reverse failed", "user_id", row.UserID, "err", err)
		}
	}()
}
```

- [ ] **Step 2: Test**

```go
func TestAuthenticateSession_FiresAutoReverseWhenGatesSet(t *testing.T) {
	// Seed user with sub_cancel_at_period_end=true, sub_cancel_is_auto=true,
	// and a fake Stripe that says CancelAtPeriodEnd=true.
	// Authenticate. Wait briefly for the goroutine. Assert cache cleared,
	// banner set, subscription_kept event row exists.
}

func TestAuthenticateSession_DoesNotFireAutoReverseForManualCancel(t *testing.T) {
	// sub_cancel_at_period_end=true but sub_cancel_is_auto=false.
	// Authenticate. Assert no Stripe call, no event row, cache unchanged.
}
```

(Goroutine timing in tests: a brief sleep or a `require.Eventually` polling assertion.)

- [ ] **Step 3: Run tests**

```bash
go test ./internal/backend/ -race -count=1 -v
```

- [ ] **Step 4: Commit**

```bash
git add internal/backend/backend.go internal/backend/auth_test.go
git commit -m "auth: fire idleunsub.AutoReverse on authed requests when gates set"
```

---

## Phase 5: RPC + Frontend

### Task 13: User-info RPC + `AckKeptBanner` RPC

**Files:**
- Modify: `pb/drill/v1/user.proto` (add `pending_kept_banner` to user-info response; add `AckKeptBanner` RPC method)
- Run: `buf generate` after proto changes
- Modify: `internal/rpc/user/server.go` (project the new field; implement `AckKeptBanner`)
- Modify: `web/src/pb/...` (regenerated by buf — should be automatic)
- Test: existing user-info RPC tests + a new `TestAckKeptBanner` test

- [ ] **Step 1: Update the proto**

In `pb/drill/v1/user.proto`, find the user-info message (search for `GetUser`, `WhoAmI`, or similar) and add:

```proto
message User {
  // ... existing fields ...
  bool pending_kept_banner = 20; // tag picks the next free number
}

// Add the new RPC:
service UserService {
  // ... existing methods ...
  rpc AckKeptBanner(AckKeptBannerRequest) returns (AckKeptBannerResponse);
}

message AckKeptBannerRequest {}
message AckKeptBannerResponse {}
```

(Use the actual existing service / message names — search the file.)

- [ ] **Step 2: Run buf generate**

```bash
buf generate
```

Expected: regenerates `internal/pb/drill/v1/user.pb.go` and the corresponding ConnectRPC service files, plus `web/src/pb/drill/v1/user_pb.ts` and `_connect.ts`.

- [ ] **Step 3: Project `pending_kept_banner` into the user-info response**

In `internal/rpc/user/server.go`, find the user-info handler and add the field to the response:

```go
return &drillv1.User{
	// ... existing fields ...
	PendingKeptBanner: user.PendingKeptBanner,
}, nil
```

- [ ] **Step 4: Implement `AckKeptBanner`**

```go
func (s *Server) AckKeptBanner(ctx context.Context, req *connect.Request[drillv1.AckKeptBannerRequest]) (*connect.Response[drillv1.AckKeptBannerResponse], error) {
	user := auth.UserFromContext(ctx.Context())
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}
	if err := s.queries.ClearKeptBanner(ctx.Context(), user.ID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("clear banner: %w", err))
	}
	return connect.NewResponse(&drillv1.AckKeptBannerResponse{}), nil
}
```

- [ ] **Step 5: Tests**

```go
func TestAckKeptBanner_ClearsFlag(t *testing.T) {
	// Seed a user with pending_kept_banner=true.
	// Call AckKeptBanner.
	// Assert: pending_kept_banner=false in DB.
}

func TestAckKeptBanner_RequiresAuth(t *testing.T) {
	// Call without auth context → CodeUnauthenticated.
}
```

- [ ] **Step 6: Run all tests**

```bash
make test
```

Expected: full pipeline (buf lint, codegen check, frontend tsc, lint, vitest, backend tests) passes.

- [ ] **Step 7: Commit**

```bash
git add pb/drill/v1/user.proto internal/pb/ web/src/pb/ \
        internal/rpc/user/server.go
git commit -m "user: pending_kept_banner field and AckKeptBanner RPC"
```

---

### Task 14: Frontend KeptBanner component

**Files:**
- Create: `web/src/components/KeptBanner.tsx`
- Modify: app shell (e.g., `web/src/App.tsx` or wherever the top-level layout lives) to mount `<KeptBanner />` in a place visible after auth

- [ ] **Step 1: Implement the banner component**

```tsx
import { useState, useEffect } from "react";
import { useUserStore } from "../store/user"; // existing convention; adjust import
import { userClient } from "../api/client";    // existing ConnectRPC client

export function KeptBanner() {
  const user = useUserStore((s) => s.user);
  const [dismissed, setDismissed] = useState(false);

  if (!user?.pendingKeptBanner || dismissed) return null;

  const dismiss = async () => {
    setDismissed(true); // optimistic
    try {
      await userClient.ackKeptBanner({});
    } catch (e) {
      console.warn("ackKeptBanner failed", e);
      // banner stays dismissed in this session; next page load reads server state
    }
  };

  return (
    <div role="alert" className="kept-banner">
      <span>Welcome back — we kept your subscription active.</span>
      <a href="/billing">Manage subscription</a>
      <button onClick={dismiss} aria-label="Dismiss">×</button>
    </div>
  );
}
```

(Match existing styling conventions — inspect a sibling component to see whether the project uses CSS modules, Tailwind, or plain CSS.)

- [ ] **Step 2: Mount it**

In the top-level layout component (likely `web/src/App.tsx` or `web/src/components/AppShell.tsx`), import and render `<KeptBanner />` somewhere users see it on next page load — typically just inside the authenticated-routes wrapper.

- [ ] **Step 3: Frontend typecheck**

```bash
cd web && npx tsc -b
```

Expected: no errors.

- [ ] **Step 4: Frontend lint**

```bash
cd web && npm run lint
```

Expected: no errors. (If your project uses a different lint command, mirror it.)

- [ ] **Step 5: Browser smoke test**

Per project convention (CLAUDE.md: "After any UI-affecting commit, load the page in the browser and verify before moving on"):

```bash
make dev   # starts overmind: vite + air on :8080
```

Manually:
1. Sign in as a test user.
2. Set `users.pending_kept_banner = TRUE` for that user via psql:
   ```sql
   UPDATE users SET pending_kept_banner = TRUE WHERE email = 'your-test@example.com';
   ```
3. Reload the page. Banner appears.
4. Click "×" — banner disappears.
5. Reload again — banner stays gone (server-side flag was cleared via AckKeptBanner).

- [ ] **Step 6: Commit**

```bash
git add web/src/components/KeptBanner.tsx web/src/App.tsx
git commit -m "web: KeptBanner component for post-reverse welcome-back UX"
```

---

## Phase 6: Verification

### Task 15: End-to-end integration test + full `make test` cleanup

**Files:**
- Create: `internal/feat/idleunsub/integration_test.go` (an end-to-end test that exercises the full path: webhook → cancel → email → keep-link click → reverse)
- Possibly: cleanup of any test leftovers from prior tasks

- [ ] **Step 1: Write the end-to-end happy-path test**

```go
//go:build integration

package idleunsub_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v82"

	"github.com/btc/drill/internal/backendtest"
	"github.com/btc/drill/internal/feat/idleunsub"
	"github.com/btc/drill/internal/handler"
)

func TestE2E_CancelAndKeepViaLink(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	userID := backendtest.SeedUser(t, b)
	subID := "sub_e2e"

	// Set up the user as a Pro subscriber idle for 2+ periods.
	_, err := b.Pool().Exec(ctx, `
		UPDATE users
		SET stripe_customer_id      = $1,
		    stripe_subscription_id  = $2,
		    plan                    = 'pro',
		    idle_eligible_after     = NOW() - INTERVAL '6 months'
		WHERE id = $3`, "cus_e2e", subID, userID)
	require.NoError(t, err)
	_, err = b.Pool().Exec(ctx, `
		UPDATE auth_sessions SET last_active = NOW() - INTERVAL '3 months'
		WHERE user_id = $1`, userID)
	require.NoError(t, err)

	// Fake Stripe with a sub that's mid-monthly-cycle.
	now := time.Now().UTC().Truncate(time.Second)
	periodStart := now.AddDate(0, 0, -23)
	periodEnd := periodStart.AddDate(0, 1, 0)
	fake := newFakeStripeWithSub(subID, "cus_e2e", periodStart, periodEnd)

	signer := idleunsub.NewTokenSigner([]byte("test-key"))
	mailer := newRecordingMailer()
	svc := idleunsub.NewService(b.Pool(), fake, mailer, signer, b.Logger())

	// 1. Fire invoice.upcoming → sub gets canceled.
	require.NoError(t, svc.HandleInvoiceUpcoming(ctx, makeUpcomingEvent("evt_1", subID)))
	require.True(t, fake.subs[subID].CancelAtPeriodEnd)
	require.Len(t, mailer.sent, 1) // cancel email enqueued
	require.Contains(t, mailer.sent[0].Subject, "next period")

	// 2. Extract the keep link from the email body.
	keepURL := extractKeepURL(t, mailer.sent[0].TextBody)

	// 3. Click the link → /sub/keep endpoint.
	mux := http.NewServeMux()
	mux.Handle("GET /sub/keep", handler.NewKeepHandler(signer, svc, b.Pool()))
	server := httptest.NewServer(mux)
	defer server.Close()

	resp, err := http.Get(server.URL + "/sub/keep?t=" + extractToken(keepURL))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// 4. Verify reversal happened.
	require.False(t, fake.subs[subID].CancelAtPeriodEnd)
	require.Len(t, mailer.sent, 2) // kept email enqueued
	require.Contains(t, mailer.sent[1].Subject, "still active")

	// 5. Verify cache and event rows.
	var cap, isAuto, banner bool
	err = b.Pool().QueryRow(ctx, `
		SELECT sub_cancel_at_period_end, sub_cancel_is_auto, pending_kept_banner
		FROM users WHERE id = $1`, userID).Scan(&cap, &isAuto, &banner)
	require.NoError(t, err)
	require.False(t, cap)
	require.False(t, isAuto)
	require.True(t, banner)

	// 6. Replay the keep link → idempotent (still 200, no second email).
	resp2, err := http.Get(server.URL + "/sub/keep?t=" + extractToken(keepURL))
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp2.StatusCode)
	require.Len(t, mailer.sent, 2) // STILL 2; no new email
}
```

(Helpers `newFakeStripeWithSub`, `newRecordingMailer`, `makeUpcomingEvent`, `extractKeepURL`, `extractToken`: small constructors. Implement at the bottom of the test file.)

- [ ] **Step 2: Add the auto-reverse e2e test**

```go
func TestE2E_AutoReverseOnLogin(t *testing.T) {
	// Same setup, but instead of clicking the link, simulate an authed request
	// by calling Backend.AuthenticateSession with a valid session token.
	// Wait briefly for the goroutine. Assert reversal + banner set + email sent.
}
```

- [ ] **Step 3: Run all tests**

```bash
go test -tags=integration ./internal/feat/idleunsub/ -race -count=1 -v
make test
```

Expected: full pipeline passes.

- [ ] **Step 4: Manually verify metrics and logs**

Run `make dev` and trigger a cancel by hand:

1. Use `stripe trigger invoice.upcoming` (Stripe CLI) against the local webhook, OR
2. Insert a fixture event into the dev DB manually and call the webhook handler.

Watch for:
- `idleunsub.cancel.fired` counter increments
- `subscription_auto_canceled` row in `user_events`
- Cancel email visible in the local mail log (Mailgun stub or `LogSender`)

- [ ] **Step 5: Final commit**

```bash
git add internal/feat/idleunsub/integration_test.go
git commit -m "idleunsub: end-to-end integration tests for cancel + reverse"
```

---

## Acceptance Criteria

The feature is shippable when:

1. **Schema migrations 015–018 apply cleanly** on a fresh DB and on a DB seeded with the current production schema (no data loss, no broken FKs).
2. **`make test` is green** end-to-end (buf lint, codegen check, frontend typecheck/lint/vitest, backend tests with `-race`).
3. **All spec test cases pass** (§8 of the design doc):
   - Trigger rule edge cases (idle/active/grandfathered/no-history/first-period)
   - Idempotency (retry storm + multi-firing)
   - Reversal paths (link, auto-activity, manual cancel guard)
   - Token (sign/verify/tampering/expired/replay/invariant)
   - Concurrent webhook race (`SELECT FOR UPDATE`)
   - Cache drift correction
4. **Manual smoke test** (browser, per `CLAUDE.md`): banner appears on auto-reverse, dismisses cleanly, doesn't reappear on next load.
5. **Observability**: counters from §9 of the spec are emitted on each path. Verify via local OTEL exporter or by inspecting the metrics handler output.

## Out of Scope (Per Spec §10)

These are explicitly *not* implemented in this plan; revisit later if needed:
- Multiple subscriptions per user
- Annual subscriptions (rule generalizes; no special handling shipped)
- Settings toggle to opt out
- Cross-Spanda-product code library
- Cancel-window UI surface beyond the banner (e.g., billing-page indicator)
- Auto-prune of `auth_sessions` and `keep_link_token_uses` (boy-scout cleanup, not blocking)
- Generic webhook dedup adoption by other handlers (`invoice.paid`, etc.)
- `sub_current_period_end` cache column (we read end from `subscription.Update` response)
