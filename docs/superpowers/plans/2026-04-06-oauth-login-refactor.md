# OAuth Login Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the recursive retry-based `OAuthLogin` with a linear, race-safe flow using `FOR UPDATE` and `ON CONFLICT DO NOTHING`, unify auth session creation between `Login` and `OAuthLogin`, and extract `normalizeEmail`.

**Architecture:** Single transaction per OAuth login with explicit Read Committed isolation. Resolution via priority-ordered checks (known OAuth account → known email → new user), side effects via switch on resolution path, shared `createAuthSession` method for both login flows.

**Tech Stack:** Go, PostgreSQL, sqlc, pgx/v5, OpenTelemetry

**Spec:** `docs/superpowers/specs/2026-04-06-oauth-login-refactor-design.md`

---

### Task 1: Add new SQL queries and regenerate

**Files:**
- Modify: `sql/queries/users.sql`
- Modify: `sql/queries/oauth_accounts.sql`
- Regenerate: `internal/db/users.sql.go`, `internal/db/oauth_accounts.sql.go`, `internal/db/querier.go`

- [ ] **Step 1: Add `GetUserByEmailForUpdate` query**

Add to the end of `sql/queries/users.sql`:

```sql
-- name: GetUserByEmailForUpdate :one
SELECT * FROM users
WHERE email = @email
FOR UPDATE;
```

- [ ] **Step 2: Add `CreateOAuthUserOrNoop` query**

Add to the end of `sql/queries/users.sql`:

```sql
-- name: CreateOAuthUserOrNoop :one
INSERT INTO users (email, email_verified, display_name)
VALUES (@email, TRUE, @display_name)
ON CONFLICT (email) DO NOTHING
RETURNING *;
```

- [ ] **Step 3: Add `LinkOAuthAccount` query**

Add to the end of `sql/queries/oauth_accounts.sql`:

```sql
-- name: LinkOAuthAccount :one
INSERT INTO oauth_accounts (user_id, provider, provider_id)
VALUES (@user_id, @provider, @provider_id)
ON CONFLICT (provider, provider_id) DO NOTHING
RETURNING *;
```

- [ ] **Step 4: Regenerate sqlc**

Run: `sqlc generate`
Expected: exits 0, updates `internal/db/users.sql.go`, `internal/db/oauth_accounts.sql.go`, `internal/db/querier.go`

- [ ] **Step 5: Verify build**

Run: `go build ./...`
Expected: PASS

- [ ] **Step 6: Run existing tests**

Run: `go test ./internal/backend/ -count=1 -v`
Expected: all existing tests pass (new queries are additive, nothing calls them yet)

- [ ] **Step 7: Commit**

```bash
git add sql/queries/users.sql sql/queries/oauth_accounts.sql internal/db/
git commit -m "feat(db): add GetUserByEmailForUpdate, CreateOAuthUserOrNoop, LinkOAuthAccount queries"
```

---

### Task 2: Extract `normalizeEmail` helper

**Files:**
- Modify: `internal/backend/auth.go`

- [ ] **Step 1: Add `normalizeEmail` function**

Add after the `isDuplicateKeyError` function (line 355) in `internal/backend/auth.go`:

```go
// normalizeEmail lowercases and trims whitespace from an email address.
func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}
```

- [ ] **Step 2: Replace raw usages in `Signup`**

In `internal/backend/auth.go`, in `Signup` (line 72), replace:

```go
p.Email = strings.ToLower(strings.TrimSpace(p.Email))
```

with:

```go
p.Email = normalizeEmail(p.Email)
```

- [ ] **Step 3: Replace raw usages in `Login`**

In `internal/backend/auth.go`, in `Login` (line 148), replace:

```go
p.Email = strings.ToLower(strings.TrimSpace(p.Email))
```

with:

```go
p.Email = normalizeEmail(p.Email)
```

- [ ] **Step 4: Replace raw usages in `ForgotPassword`**

In `internal/backend/auth.go`, in `ForgotPassword` (line 256), replace:

```go
email = strings.TrimSpace(strings.ToLower(email))
```

with:

```go
email = normalizeEmail(email)
```

Note: `ForgotPassword` has the order reversed (`TrimSpace(ToLower(...))` vs `ToLower(TrimSpace(...))`). Both produce the same result. The helper standardizes on `ToLower(TrimSpace(...))`.

- [ ] **Step 5: Verify build and tests**

Run: `go build ./... && go test ./internal/backend/ -count=1 -v`
Expected: all pass

- [ ] **Step 6: Commit**

```bash
git add internal/backend/auth.go
git commit -m "refactor(backend): extract normalizeEmail helper, replace raw usages"
```

---

### Task 3: Extract `createAuthSession` shared method

**Files:**
- Modify: `internal/backend/auth.go` (add method, update `Login`)
- Modify: `internal/backend/oauth.go` (delete `createSessionInTx`)

- [ ] **Step 1: Add `AuthSessionParams`, `AuthSessionResult`, and `createAuthSession`**

Add in `internal/backend/auth.go`, after the `LoginResult` type (after line 61):

```go
// AuthSessionParams holds the parameters for createAuthSession.
type AuthSessionParams struct {
	UserID    uuid.UUID
	IP        string
	UserAgent string
}

// AuthSessionResult is returned by createAuthSession on success.
type AuthSessionResult struct {
	Token string
}

// createAuthSession generates a session token, stores the hashed token in the
// database, and returns the raw token. Works with any db.DBTX (pool or tx).
// Shared by Login and OAuthLogin.
func (b *Backend) createAuthSession(ctx context.Context, dbtx db.DBTX, p AuthSessionParams) (_ *AuthSessionResult, err error) {
	ctx, span := tracer.Start(ctx, "Backend.createAuthSession")
	defer func() { drilotel.End(span, err) }()

	rawToken, tokenHash, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("create auth session: generate token: %w", err)
	}

	_, err = db.New(dbtx).CreateAuthSession(ctx, db.CreateAuthSessionParams{
		UserID:    p.UserID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(b.cfg.Auth.SessionTTL),
		IpAddress: parseClientIP(p.IP),
		UserAgent: pgtype.Text{String: p.UserAgent, Valid: p.UserAgent != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("create auth session: %w", err)
	}

	return &AuthSessionResult{Token: rawToken}, nil
}
```

- [ ] **Step 2: Update `Login` to use `createAuthSession`**

In `internal/backend/auth.go`, replace the inline session creation in `Login` (lines 173-197):

Replace this block:

```go
	// Generate session.
	rawToken, tokenHash, err := auth.GenerateSessionToken()
	if err != nil {
		return nil, fmt.Errorf("generate session token: %w", err)
	}

	// Parse client IP.
	ipAddr := parseClientIP(p.IP)

	_, err = queries.CreateAuthSession(ctx, db.CreateAuthSessionParams{
		UserID:    user.ID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(b.cfg.Auth.SessionTTL),
		IpAddress: ipAddr,
		UserAgent: pgtype.Text{String: p.UserAgent, Valid: p.UserAgent != ""},
	})
	if err != nil {
		return nil, fmt.Errorf("create auth session: %w", err)
	}

	return &LoginResult{
		UserID: user.ID,
		Email:  user.Email,
		Token:  rawToken,
	}, nil
```

With:

```go
	sess, err := b.createAuthSession(ctx, b.pool, AuthSessionParams{
		UserID:    user.ID,
		IP:        p.IP,
		UserAgent: p.UserAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}

	return &LoginResult{
		UserID: user.ID,
		Email:  user.Email,
		Token:  sess.Token,
	}, nil
```

- [ ] **Step 3: Verify build and tests**

Run: `go build ./... && go test ./internal/backend/ -count=1 -v`
Expected: all pass. `OAuthLogin` still calls `createSessionInTx` — that method is deleted in Task 4 when the whole file is rewritten.

- [ ] **Step 4: Commit**

```bash
git add internal/backend/auth.go
git commit -m "refactor(backend): extract createAuthSession, unify Login session creation"
```

---

### Task 4: Rewrite `OAuthLogin`

**Files:**
- Modify: `internal/backend/oauth.go`

- [ ] **Step 1: Rewrite `OAuthLogin`**

Replace the entire contents of `internal/backend/oauth.go` with:

```go
package backend

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/btc/drill/internal/db"
	"github.com/btc/drill/internal/drilotel"
)

// OAuthLoginParams holds the parameters for OAuthLogin.
type OAuthLoginParams struct {
	Provider    string // "google" or "github"
	ProviderID  string // unique ID from the OAuth provider
	Email       string
	DisplayName string
	IP          string
	UserAgent   string
}

// OAuthLoginResult is returned by OAuthLogin on success.
type OAuthLoginResult struct {
	UserID       uuid.UUID
	Email        string
	Token        string
	NeedsProfile bool // true when display_name is empty
}

// OAuthLogin finds or creates a user from an OAuth provider callback.
// One transaction, no retry loop. See design spec for details.
func (b *Backend) OAuthLogin(ctx context.Context, p OAuthLoginParams) (_ *OAuthLoginResult, err error) {
	ctx, span := tracer.Start(ctx, "Backend.OAuthLogin")
	defer func() { drilotel.End(span, err) }()

	const (
		pathExistingOAuth  = "existing_oauth"
		pathLinkedExisting = "linked_existing"
		pathReactivated    = "reactivated"
		pathNewUser        = "new_user"
	)

	p.Email = normalizeEmail(p.Email)

	// Read Committed: FOR UPDATE blocks concurrent access to existing rows,
	// and ON CONFLICT DO NOTHING handles concurrent inserts. Higher isolation
	// levels would convert these into serialization failures requiring
	// full-transaction retries.
	tx, err := b.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.ReadCommitted,
	})
	if err != nil {
		return nil, fmt.Errorf("oauth login: begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := db.New(tx)

	var user db.User
	var path string

	// --- Resolve user ---

	// (A) Returning user: they've logged in with this provider before.
	oauthAcct, err := q.GetOAuthAccount(ctx, db.GetOAuthAccountParams{
		Provider:   p.Provider,
		ProviderID: p.ProviderID,
	})
	if err == nil {
		user, err = q.GetUserByIDIncludingDeleted(ctx, oauthAcct.UserID)
		if err != nil {
			return nil, fmt.Errorf("oauth login: get user by id: %w", err)
		}
		if user.DeletedAt.Valid {
			path = pathReactivated
		} else {
			path = pathExistingOAuth
		}
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("oauth login: get oauth account: %w", err)
	}

	// (B) First time with this provider, but we already have an account
	//     for their email (e.g. they signed up with password, now adding
	//     Google). Lock the row so a concurrent login can't race us.
	if path == "" {
		user, err = q.GetUserByEmailForUpdate(ctx, p.Email)
		if err == nil {
			if user.DeletedAt.Valid {
				path = pathReactivated
			} else {
				path = pathLinkedExisting
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: get user by email: %w", err)
		}
	}

	// (C) Brand new user — no account exists for this email.
	//     In rare cases, another request for the same email may have
	//     created the account between (B) and now (e.g. the user
	//     double-clicked the OAuth button). CreateOAuthUserOrNoop
	//     safely no-ops in that case, and we re-select their row.
	if path == "" {
		user, err = q.CreateOAuthUserOrNoop(ctx, db.CreateOAuthUserOrNoopParams{
			Email:       p.Email,
			DisplayName: p.DisplayName,
		})
		if err == nil {
			path = pathNewUser
		} else if errors.Is(err, pgx.ErrNoRows) {
			// Another request just created this account. Use theirs.
			user, err = q.GetUserByEmailForUpdate(ctx, p.Email)
			if err != nil {
				return nil, fmt.Errorf("oauth login: get user after race: %w", err)
			}
			if user.DeletedAt.Valid {
				path = pathReactivated
			} else {
				path = pathLinkedExisting
			}
		} else {
			return nil, fmt.Errorf("oauth login: create user: %w", err)
		}
	}

	// --- Side effects ---

	switch path {
	case pathNewUser:
		// Provision free grant.
		if err := b.provisionNewUser(ctx, tx, user.ID); err != nil {
			return nil, fmt.Errorf("oauth login: provision: %w", err)
		}
		// Link OAuth account.
		if _, err := q.LinkOAuthAccount(ctx, db.LinkOAuthAccountParams{
			UserID:     user.ID,
			Provider:   p.Provider,
			ProviderID: p.ProviderID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: link account: %w", err)
		}

	case pathReactivated:
		// Clear deleted_at.
		if err := q.ReactivateUser(ctx, user.ID); err != nil {
			return nil, fmt.Errorf("oauth login: reactivate: %w", err)
		}
		// Link OAuth account.
		if _, err := q.LinkOAuthAccount(ctx, db.LinkOAuthAccountParams{
			UserID:     user.ID,
			Provider:   p.Provider,
			ProviderID: p.ProviderID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: link account: %w", err)
		}

	case pathLinkedExisting:
		// Verify email if unverified.
		if !user.EmailVerified {
			if err := q.VerifyUserEmail(ctx, user.ID); err != nil {
				return nil, fmt.Errorf("oauth login: verify email: %w", err)
			}
		}
		// Link OAuth account.
		if _, err := q.LinkOAuthAccount(ctx, db.LinkOAuthAccountParams{
			UserID:     user.ID,
			Provider:   p.Provider,
			ProviderID: p.ProviderID,
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("oauth login: link account: %w", err)
		}

	case pathExistingOAuth:
		// Nothing — user and link already exist.
	}

	// --- Auth session (shared with Login) ---

	sess, err := b.createAuthSession(ctx, tx, AuthSessionParams{
		UserID:    user.ID,
		IP:        p.IP,
		UserAgent: p.UserAgent,
	})
	if err != nil {
		return nil, fmt.Errorf("oauth login: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("oauth login: commit: %w", err)
	}

	slog.Info("oauth login", "provider", p.Provider, "email", user.Email, "path", path, "user_id", user.ID)

	return &OAuthLoginResult{
		UserID:       user.ID,
		Email:        user.Email,
		Token:        sess.Token,
		NeedsProfile: user.DisplayName == "",
	}, nil
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`
Expected: PASS. The `uuid` import should already be available from the existing file. If not, add `"github.com/google/uuid"` to imports.

- [ ] **Step 3: Run all existing OAuth tests**

Run: `go test ./internal/backend/ -run TestOAuth -count=1 -v`
Expected: all 8 existing tests pass with no changes.

- [ ] **Step 4: Run all backend tests**

Run: `go test ./internal/backend/ -count=1 -v`
Expected: all pass.

- [ ] **Step 5: Run handler OAuth tests**

Run: `go test ./internal/handler/ -run TestOAuth -count=1 -v`
Expected: all 4 handler tests pass.

- [ ] **Step 6: Commit**

```bash
git add internal/backend/oauth.go
git commit -m "refactor(backend): rewrite OAuthLogin with linear FOR UPDATE flow

Replace recursive retry-based OAuthLogin with a single-transaction
flow using FOR UPDATE and ON CONFLICT DO NOTHING. Three resolution
paths (A/B/C) with explicit side effects per path. No retry loops."
```

---

### Task 5: Add missing test — reactivation via email match

**Files:**
- Modify: `internal/backend/oauth_test.go`

- [ ] **Step 1: Write the test**

Add to the end of `internal/backend/oauth_test.go`, before the closing of the file:

```go
func TestOAuthLogin_SoftDeletedUser_ReactivatedViaEmailMatch(t *testing.T) {
	t.Parallel()
	b := pg.NewBackend(t)
	ctx := context.Background()
	queries := db.New(b.Pool())

	// Create user via Signup (password-based, no OAuth link).
	signupRes := signupUser(t, b, "reactivate-email@example.com", "strongpass1", "Reactivate")

	// Soft-delete the user.
	err := queries.SoftDeleteUser(ctx, signupRes.UserID)
	require.NoError(t, err)

	// Verify user is soft-deleted.
	user, err := queries.GetUserByIDIncludingDeleted(ctx, signupRes.UserID)
	require.NoError(t, err)
	require.True(t, user.DeletedAt.Valid)

	// OAuthLogin with matching email but new provider+provider_id.
	// This exercises path (B) → pathReactivated: no OAuth link exists,
	// but the email matches a soft-deleted user.
	res, err := b.OAuthLogin(ctx, backend.OAuthLoginParams{
		Provider:    "google",
		ProviderID:  "google-reactivate-email-555",
		Email:       "reactivate-email@example.com",
		DisplayName: "Reactivate",
		IP:          "10.0.0.1:9999",
		UserAgent:   "test-agent",
	})
	require.NoError(t, err)
	require.Equal(t, signupRes.UserID, res.UserID, "should reuse the same user")
	require.NotEmpty(t, res.Token)

	// Verify user is no longer soft-deleted.
	user, err = queries.GetUserByIDIncludingDeleted(ctx, signupRes.UserID)
	require.NoError(t, err)
	require.False(t, user.DeletedAt.Valid)
	require.True(t, user.EmailVerified)

	// Verify OAuth account was linked.
	oauthAccts, err := queries.GetOAuthAccountsByUser(ctx, signupRes.UserID)
	require.NoError(t, err)
	require.Len(t, oauthAccts, 1)
	require.Equal(t, "google", oauthAccts[0].Provider)
	require.Equal(t, "google-reactivate-email-555", oauthAccts[0].ProviderID)
}
```

- [ ] **Step 2: Run the new test**

Run: `go test ./internal/backend/ -run TestOAuthLogin_SoftDeletedUser_ReactivatedViaEmailMatch -count=1 -v`
Expected: PASS

- [ ] **Step 3: Run all OAuth tests to check for regressions**

Run: `go test ./internal/backend/ -run TestOAuth -count=1 -v`
Expected: all 9 tests pass (8 existing + 1 new)

- [ ] **Step 4: Commit**

```bash
git add internal/backend/oauth_test.go
git commit -m "test(backend): add reactivation via email match test for OAuthLogin"
```

---

### Task 6: Final verification

**Files:** None (verification only)

- [ ] **Step 1: Run full backend test suite**

Run: `go test ./internal/backend/ -count=1 -v`
Expected: all pass

- [ ] **Step 2: Run full handler test suite**

Run: `go test ./internal/handler/ -count=1 -v`
Expected: all pass

- [ ] **Step 3: Run full project tests**

Run: `go test ./... -count=1`
Expected: all pass

- [ ] **Step 4: Run linter**

Run: `golangci-lint run ./...`
Expected: no new issues

- [ ] **Step 5: Verify no dead code**

Confirm the following are gone from the codebase:
- `oauthLoginWithRetry` — should not appear anywhere
- `createSessionInTx` — should not appear anywhere
- No `isDuplicateKeyError` calls in `internal/backend/oauth.go`

Run: `grep -n 'oauthLoginWithRetry\|createSessionInTx' internal/backend/*.go`
Expected: no output

- [ ] **Step 6: Verify `normalizeEmail` is used everywhere**

Run: `grep -n 'strings.ToLower.*strings.TrimSpace\|strings.TrimSpace.*strings.ToLower' internal/backend/*.go`
Expected: no output (all raw usages replaced)
