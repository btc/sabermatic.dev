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

The method becomes three phases: **resolve user**, **apply side effects**, **create session**. One transaction, no retry loop.

```go
func (b *Backend) OAuthLogin(ctx context.Context, p OAuthLoginParams) (*OAuthLoginResult, error) {
    p.Email = strings.ToLower(strings.TrimSpace(p.Email))

    tx, err := b.pool.Begin(ctx)
    // ...
    q := db.New(tx)

    // Phase 1: Resolve user.
    user, path, err := b.resolveOAuthUser(ctx, q, p)

    // Phase 2: Side effects based on resolution path.
    err = b.applyOAuthSideEffects(ctx, tx, q, user, p, path)

    // Phase 3: Create session (shared with Login).
    sess, err := b.createSession(ctx, tx, CreateSessionParams{...})

    tx.Commit(ctx)
    // return result
}
```

#### Resolution paths

```go
const (
    pathExistingOAuth  = "existing_oauth"
    pathLinkedExisting = "linked_existing"
    pathReactivated    = "reactivated"
    pathNewUser        = "new_user"
)
```

#### `resolveOAuthUser`

Determines who this user is without mutating state (except the new-user insert):

1. `GetOAuthAccount(provider, provider_id)` — if found, load user by ID. If soft-deleted, return `pathReactivated`. Otherwise `pathExistingOAuth`.
2. `GetUserByEmailForUpdate(email)` — if found and soft-deleted, return `pathReactivated`. Otherwise `pathLinkedExisting`. The `FOR UPDATE` lock prevents races with concurrent logins for the same email.
3. `CreateOAuthUserOrNoop(email, display_name)` — if row returned, `pathNewUser`. If no row (lost race), fall through.
4. `GetUserByEmailForUpdate(email)` — the conflicting transaction committed, so the row exists. If soft-deleted, `pathReactivated`. Otherwise `pathLinkedExisting`. If `ErrNoRows` (other tx rolled back), return an error — the caller can retry at the HTTP level.

No retry loop. Step 4 is a deterministic fallback, not a recursive retry.

#### `applyOAuthSideEffects`

Explicit switch on the resolution path:

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

1. `oauthLoginWithRetry` — replaced by linear `resolveOAuthUser` + `FOR UPDATE`.
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
- **Resolution and mutation are separated.** `resolveOAuthUser` determines the path; `applyOAuthSideEffects` applies the right mutations. Each can be reasoned about independently.
- **Every path's side effects are explicit.** A switch statement with four cases, each listing exactly what it does.
