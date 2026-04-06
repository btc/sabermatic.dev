# OAuth Login Refactor

## Problem

`backend.OAuthLogin` is complex and hard to reason about:

1. **3-step cascade with interleaved side effects.** The method checks OAuth account, then email, then creates a new user — each branch has different side effects (reactivate, verify email, link account, provision) tangled into the lookup logic.

2. **Retry-as-concurrency-control.** Recursive calls to `oauthLoginWithRetry` / `OAuthLogin` on duplicate-key errors. Correct only if the retry lands on an earlier step — nothing enforces that structurally.

3. **Session creation divergence.** `Login` creates an auth session inline without a transaction. `OAuthLogin` creates one via `createSessionInTx` inside a transaction. Same operation, different code paths, subtly different transactional guarantees.

## Design

### Shared session creation

Extract a `createSession` method on `Backend` that takes `db.DBTX`:

```go
type CreateSessionParams struct {
    UserID    uuid.UUID
    IP        string
    UserAgent string
}

type CreateSessionResult struct {
    Token string
}

func (b *Backend) createSession(ctx context.Context, dbtx db.DBTX, p CreateSessionParams) (*CreateSessionResult, error)
```

- Generates token via `auth.GenerateSessionToken()`
- Parses IP via `parseClientIP()`
- Calls `db.New(dbtx).CreateAuthSession()`
- Uses `b.cfg.Auth.SessionTTL` for expiry

`Login` calls `b.createSession(ctx, b.pool, ...)`. `OAuthLogin` calls `b.createSession(ctx, tx, ...)`. One path.

Delete `createSessionInTx` and the inline session logic in `Login`.

### New SQL queries

Three new sqlc queries:

**`GetUserByEmailForUpdate`** — Row-level lock on existing user (including soft-deleted):

```sql
-- name: GetUserByEmailForUpdate :one
SELECT * FROM users
WHERE email = @email
FOR UPDATE;
```

**`CreateOAuthUserOrNoop`** — Insert new user, no-op on email conflict:

```sql
-- name: CreateOAuthUserOrNoop :one
INSERT INTO users (email, email_verified, display_name)
VALUES (@email, TRUE, @display_name)
ON CONFLICT (email) DO NOTHING
RETURNING *;
```

Returns the row if inserted, `pgx.ErrNoRows` if conflict.

**`LinkOAuthAccount`** — Link provider, no-op on conflict:

```sql
-- name: LinkOAuthAccount :one
INSERT INTO oauth_accounts (user_id, provider, provider_id)
VALUES (@user_id, @provider, @provider_id)
ON CONFLICT (provider, provider_id) DO NOTHING
RETURNING *;
```

Existing queries (`GetOAuthAccount`, `ReactivateUser`, `VerifyUserEmail`, `GetUserByIDIncludingDeleted`) remain unchanged.

### Restructured OAuthLogin

One function, one transaction, no retry loop. Three sections: **resolve user**, **apply side effects**, **create session**. A local `path` variable connects resolution to side effects.

```go
func (b *Backend) OAuthLogin(ctx context.Context, p OAuthLoginParams) (*OAuthLoginResult, error) {
    const (
        pathExistingOAuth  = "existing_oauth"
        pathLinkedExisting = "linked_existing"
        pathReactivated    = "reactivated"
        pathNewUser        = "new_user"
    )

    p.Email = strings.ToLower(strings.TrimSpace(p.Email))

    tx, err := b.pool.Begin(ctx)
    // ...
    q := db.New(tx)

    var user db.User
    var path string

    // --- Resolve user ---

    // Step 1: Known OAuth account?
    oauthAcct, err := q.GetOAuthAccount(ctx, ...)
    if err == nil {
        user, err = q.GetUserByIDIncludingDeleted(ctx, oauthAcct.UserID)
        // ...
        if user.DeletedAt.Valid {
            path = pathReactivated
        } else {
            path = pathExistingOAuth
        }
    }

    // Step 2: Known email? Lock the row.
    if path == "" {
        user, err = q.GetUserByEmailForUpdate(ctx, p.Email)
        if err == nil {
            if user.DeletedAt.Valid {
                path = pathReactivated
            } else {
                path = pathLinkedExisting
            }
        }
    }

    // Step 3: New user.
    if path == "" {
        user, err = q.CreateOAuthUserOrNoop(ctx, ...)
        if err == nil {
            path = pathNewUser
        } else if errors.Is(err, pgx.ErrNoRows) {
            // Lost insert race — other tx created this email.
            user, err = q.GetUserByEmailForUpdate(ctx, p.Email)
            // ... if ErrNoRows, return error (other tx rolled back)
            if user.DeletedAt.Valid {
                path = pathReactivated
            } else {
                path = pathLinkedExisting
            }
        }
    }

    // --- Side effects ---
    switch path {
    case pathNewUser:
        // Provision free grant, link OAuth account.
    case pathReactivated:
        // Reactivate user, link OAuth account.
    case pathLinkedExisting:
        // Verify email if needed, link OAuth account.
    case pathExistingOAuth:
        // Nothing — user and link already exist.
    }

    // --- Session (shared with Login) ---
    sess, err := b.createSession(ctx, tx, CreateSessionParams{...})

    tx.Commit(ctx)
    // return result
}
```

#### Resolution steps

1. `GetOAuthAccount(provider, provider_id)` — if found, load user by ID. If soft-deleted, `pathReactivated`. Otherwise `pathExistingOAuth`.
2. `GetUserByEmailForUpdate(email)` — if found and soft-deleted, `pathReactivated`. Otherwise `pathLinkedExisting`. The `FOR UPDATE` lock prevents races with concurrent logins for the same email.
3. `CreateOAuthUserOrNoop(email, display_name)` — if row returned, `pathNewUser`. If no row (lost race), `GetUserByEmailForUpdate` again. If `ErrNoRows` (other tx rolled back), return error — caller can retry at HTTP level.

#### Side effects per path

| Path | Side effects |
|---|---|
| `pathNewUser` | `provisionNewUser` (free grant), `LinkOAuthAccount` |
| `pathReactivated` | `ReactivateUser`, `LinkOAuthAccount` |
| `pathLinkedExisting` | `VerifyUserEmail` (if unverified), `LinkOAuthAccount` |
| `pathExistingOAuth` | None |

`LinkOAuthAccount` uses `ON CONFLICT DO NOTHING` — safe if the link already exists.

### Login changes

Replace inline session creation with shared helper:

```go
// In Login, after password verification:
sess, err := b.createSession(ctx, b.pool, CreateSessionParams{
    UserID: user.ID, IP: p.IP, UserAgent: p.UserAgent,
})
```

`Login` does not need a transaction — password check is read-only, session insert is a single atomic write.

## What gets deleted

1. `oauthLoginWithRetry` — replaced by inline `FOR UPDATE` + `ON CONFLICT DO NOTHING` flow.
2. `createSessionInTx` — replaced by shared `createSession`.
3. Inline session creation in `Login` — replaced by `b.createSession(ctx, b.pool, ...)`.
4. `isDuplicateKeyError` calls in `oauth.go` — no longer needed there. Stays in `auth.go` for `Signup`.

## What doesn't change

- `internal/handler/oauth.go` — untouched. Same `b.OAuthLogin()` call and result type.
- `internal/auth/oauth.go` — untouched. Goth setup is out of scope.
- `OAuthLoginParams` / `OAuthLoginResult` — same fields, same types.
- All existing tests pass as-is — behavior is preserved.

## New test

**Reactivation via email match (no prior OAuth link).** User signs up with password, gets soft-deleted, then logs in via OAuth with the same email. This exercises the step 2 `pathReactivated` flow where no `oauth_accounts` row exists yet — the user must be reactivated AND the OAuth account linked.

```go
func TestOAuthLogin_SoftDeletedUser_ReactivatedViaEmailMatch(t *testing.T) {
    // 1. Create user via Signup (password-based, no OAuth link).
    // 2. Soft-delete the user.
    // 3. OAuthLogin with matching email, new provider+provider_id.
    // 4. Assert: same user ID, active (deleted_at NULL), email verified,
    //    OAuth account linked, session token returned.
}
```

## Invariants

After this refactor, the following hold structurally:

- **Session creation is one path.** `createSession` is the only way to create an auth session. Both `Login` and `OAuthLogin` use it.
- **No retry loops.** `FOR UPDATE` serializes concurrent access to existing users. `ON CONFLICT DO NOTHING` + re-select handles new-user races without recursion.
- **Resolution and mutation are separated by structure, not by function.** The top half of `OAuthLogin` sets `path` and `user`; the bottom half switches on `path` to apply side effects. One function, clearly sectioned.
- **Every path's side effects are explicit.** A switch statement with four cases, each listing exactly what it does.
