# OAuth Login Refactor

## Problem

`backend.OAuthLogin` is complex and hard to reason about:

1. **3-step cascade with interleaved side effects.** The method checks OAuth account, then email, then creates a new user — each branch has different side effects (reactivate, verify email, link account, provision) tangled into the lookup logic.

2. **Retry-as-concurrency-control.** Recursive calls to `oauthLoginWithRetry` / `OAuthLogin` on duplicate-key errors. Correct only if the retry lands on an earlier step — nothing enforces that structurally.

3. **Session creation divergence.** `Login` creates an auth session inline without a transaction. `OAuthLogin` creates one via `createAuthSessionInTx` inside a transaction. Same operation, different code paths, subtly different transactional guarantees.

## Design

### Shared auth session creation

Extract a `createAuthSession` method on `Backend` that takes `db.DBTX`. Named `createAuthSession` (not `createAuthSession`) to distinguish from drill interview sessions.

```go
type AuthSessionParams struct {
    UserID    uuid.UUID
    IP        string
    UserAgent string
}

type AuthSessionResult struct {
    Token string
}

func (b *Backend) createAuthSession(ctx context.Context, dbtx db.DBTX, p AuthSessionParams) (_ *AuthSessionResult, err error) {
    ctx, span := tracer.Start(ctx, "Backend.createAuthSession")
    defer func() { drilotel.End(span, err) }()
    // ...
}
```

- Generates token via `auth.GenerateSessionToken()`
- Parses IP via `parseClientIP()`
- Calls `db.New(dbtx).CreateAuthSession()`
- Uses `b.cfg.Auth.SessionTTL` for expiry

`Login` calls `b.createAuthSession(ctx, b.pool, ...)`. `OAuthLogin` calls `b.createAuthSession(ctx, tx, ...)`. One path.

Delete `createAuthSessionInTx` and the inline session logic in `Login`.

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
func (b *Backend) OAuthLogin(ctx context.Context, p OAuthLoginParams) (_ *OAuthLoginResult, err error) {
    ctx, span := tracer.Start(ctx, "Backend.OAuthLogin")
    defer func() { drilotel.End(span, err) }()

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

    // (A) Known OAuth account: provider+provider_id already linked to a user.
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

    // (B) No OAuth link. Existing user with this email? FOR UPDATE prevents
    //     concurrent logins for the same email from racing.
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

    // (C) No OAuth link, no existing user. Create one.
    //     ON CONFLICT DO NOTHING handles the rare case where another tx
    //     inserted the same email between (B) and here.
    if path == "" {
        user, err = q.CreateOAuthUserOrNoop(ctx, ...)
        if err == nil {
            path = pathNewUser
        } else if errors.Is(err, pgx.ErrNoRows) {
            // Lost insert race — re-select the row the other tx created.
            user, err = q.GetUserByEmailForUpdate(ctx, p.Email)
            // ... if ErrNoRows (other tx rolled back), return error.
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
        // Clear deleted_at, link OAuth account.
    case pathLinkedExisting:
        // Verify email if unverified, link OAuth account.
    case pathExistingOAuth:
        // Nothing — user and link already exist.
    }

    // --- Auth session (shared with Login) ---
    sess, err := b.createAuthSession(ctx, tx, AuthSessionParams{...})

    tx.Commit(ctx)
    // return result
}
```

#### Resolution (priority order)

Each check runs only if the previous one didn't match (`path == ""`):

- **(A) Known OAuth account** — `GetOAuthAccount(provider, provider_id)`. If found, load user by ID. If soft-deleted, `pathReactivated`. Otherwise `pathExistingOAuth`.
- **(B) No OAuth link, existing email** — `GetUserByEmailForUpdate(email)`. If found and soft-deleted, `pathReactivated`. Otherwise `pathLinkedExisting`. The `FOR UPDATE` lock prevents concurrent logins for the same email from racing.
- **(C) No OAuth link, no existing user** — `CreateOAuthUserOrNoop(email, display_name)`. If row returned, `pathNewUser`. If no row (lost insert race), `GetUserByEmailForUpdate` again to pick up the row the other tx created. If `ErrNoRows` (other tx rolled back), return error — caller can retry at HTTP level.

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
sess, err := b.createAuthSession(ctx, b.pool, AuthSessionParams{
    UserID:    user.ID,
    IP:        p.IP,
    UserAgent: p.UserAgent,
})
```

`Login` does not need a transaction — password check is read-only, session insert is a single atomic write.

## What gets deleted

1. `oauthLoginWithRetry` — replaced by inline `FOR UPDATE` + `ON CONFLICT DO NOTHING` flow.
2. `createAuthSessionInTx` — replaced by shared `createAuthSession`.
3. Inline session creation in `Login` — replaced by `b.createAuthSession(ctx, b.pool, ...)`.
4. `isDuplicateKeyError` calls in `oauth.go` — no longer needed there. Stays in `auth.go` for `Signup`.

## What doesn't change

- `internal/handler/oauth.go` — untouched. Same `b.OAuthLogin()` call and result type.
- `internal/auth/oauth.go` — untouched. Goth setup is out of scope.
- `OAuthLoginParams` / `OAuthLoginResult` — same fields, same types.
- All existing tests pass as-is — behavior is preserved.

## New test

**Reactivation via email match (no prior OAuth link).** User signs up with password, gets soft-deleted, then logs in via OAuth with the same email. This exercises the "known email" `pathReactivated` flow where no `oauth_accounts` row exists yet — the user must be reactivated AND the OAuth account linked.

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

- **Session creation is one path.** `createAuthSession` is the only way to create an auth session. Both `Login` and `OAuthLogin` use it.
- **No retry loops.** `FOR UPDATE` serializes concurrent access to existing users. `ON CONFLICT DO NOTHING` + re-select handles new-user races without recursion.
- **Resolution and mutation are separated by structure, not by function.** The top half of `OAuthLogin` sets `path` and `user`; the bottom half switches on `path` to apply side effects. One function, clearly sectioned.
- **Every path's side effects are explicit.** A switch statement with four cases, each listing exactly what it does.
