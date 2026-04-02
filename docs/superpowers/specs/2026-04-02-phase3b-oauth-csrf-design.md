# Phase 3b: OAuth + CSRF — Design Spec

**Date**: 2026-04-02
**Status**: Draft
**Depends on**: Phase 3a (Core Auth) — merged to main
**Purpose**: Add Google and GitHub OAuth login, automatic account linking, CSRF protection, HKDF key derivation, and a SessionCookie helper to complete the auth system.

---

## 1. Overview

Phase 3a delivered email/password auth with sessions, middleware, email verification, and password reset. Phase 3b completes auth by adding:

1. **OAuth login** (Google + GitHub) via [Goth](https://github.com/markbates/goth)
2. **CSRF protection** via [gorilla/csrf](https://github.com/gorilla/csrf)
3. **HKDF key derivation** — purpose-specific keys from a single secret
4. **SessionCookie helper** to eliminate repeated cookie construction
5. **Account linking** — OAuth email matching an existing user auto-links
6. **Profile completion** — OAuth users with no display name are redirected to complete their profile

No new database migration. The `oauth_accounts` table already exists in `001_initial.up.sql`.

---

## 2. Dependencies

| Library | Purpose |
|---|---|
| `github.com/markbates/goth` | Multi-provider OAuth2 (Google, GitHub, 30+ others available) |
| `github.com/gorilla/csrf` | Double-submit cookie CSRF middleware |
| `golang.org/x/crypto/hkdf` | Key derivation (already transitive via `x/crypto`) |
| `golang.org/x/oauth2` | Transitive via Goth |

Goth uses `gorilla/sessions` internally for temporary OAuth flow state (state parameter stored in an encrypted cookie for ~60 seconds during the redirect/callback cycle). This does not conflict with our `auth_sessions` system — they serve different purposes.

---

## 3. Key Derivation (HKDF)

`Auth.TokenSecret` is the single master secret. Three subsystems need keys: HMAC verification tokens (Phase 3a), Goth session cookie encryption, and CSRF token authentication. Using the same raw secret for all three is a security anti-pattern — a vulnerability in one system could leak information relevant to the others.

HKDF derives purpose-specific 32-byte keys from the master secret:

```go
// internal/auth/keys.go
func DeriveKey(secret, purpose string) []byte {
    r := hkdf.New(sha256.New, []byte(secret), nil, []byte(purpose))
    key := make([]byte, 32)
    if _, err := io.ReadFull(r, key); err != nil {
        panic("hkdf: " + err.Error()) // only fails if secret is empty
    }
    return key
}
```

Derived at startup:

| Purpose string | Consumer |
|---|---|
| `"hmac-tokens"` | `auth.NewTokenSigner` (email verify, password reset) |
| `"oauth-state"` | `gothic.Store` (Goth CookieStore) |
| `"csrf"` | `csrf.Protect` |

The existing `auth.NewTokenSigner` changes from accepting the raw secret to accepting the derived key. This is a small breaking change in the internal API — no external impact.

---

## 4. OAuth Flow

### 4.1 Sequence

```
Browser                    Server                         Provider
  │                          │                               │
  │  GET /api/auth/oauth/    │                               │
  │      google              │                               │
  │ ─────────────────────>   │                               │
  │                          │  gothic.BeginAuthHandler()    │
  │                          │  - generates state param      │
  │                          │  - stores in Goth cookie      │
  │  302 → provider auth URL │                               │
  │ <─────────────────────   │                               │
  │                          │                               │
  │  ─────────────────────────────────────────────────────>  │
  │                          │        user authenticates     │
  │  <─────────────────────────────────────────────────────  │
  │  302 → /api/auth/oauth/  │                               │
  │        google/callback   │                               │
  │        ?code=...&state=..│                               │
  │ ─────────────────────>   │                               │
  │                          │  gothic.CompleteUserAuth()    │
  │                          │  - validates state from cookie│
  │                          │  - exchanges code for token   │
  │                          │  - fetches user profile       │
  │                          │                               │
  │                          │  Backend.OAuthLogin()         │
  │                          │  - find or create user        │
  │                          │  - link oauth_accounts        │
  │                          │  - create auth_session        │
  │                          │                               │
  │  Set-Cookie: drill_session                               │
  │  302 → {BaseURL}/dashboard                               │
  │     OR → {BaseURL}/complete-profile (if no display name) │
  │ <─────────────────────   │                               │
```

### 4.2 Find-or-Create Logic (`Backend.OAuthLogin`)

Called after Goth returns the authenticated provider user. All within a single transaction:

1. **Look up `oauth_accounts`** by `(provider, provider_id)`.
   - Found → load the linked user → create session → done.

2. **Look up `users`** by email (normalized, lowercase).
   - Found, `deleted_at IS NULL` → insert `oauth_accounts` row linking to this user → if user's `email_verified` is false, set it to true (provider verified it) → create session → done.
   - Found, `deleted_at IS NOT NULL` → reactivate: clear `deleted_at`, set `email_verified = true` → insert `oauth_accounts` row → create session → done. (User is actively logging in — honor the intent.)

3. **Neither found** → insert new `users` row (`email_verified = true`, `password_hash = NULL`, `display_name` from provider or empty string) → insert `oauth_accounts` row → create session → done.

**Race condition handling:** If the `oauth_accounts` INSERT hits a unique violation on `(provider, provider_id)` (concurrent callback for same user), catch `isDuplicateKeyError`, re-query `GetOAuthAccount`, and proceed with step 1.

Session creation reuses the same `auth.GenerateSessionToken()` + `CreateAuthSession` query from Phase 3a.

**Display name is not updated on subsequent OAuth logins.** The provider name is only used at account creation. Users can change their display name in-app later.

### 4.3 OAuthLoginResult

```go
type OAuthLoginResult struct {
    UserID      uuid.UUID
    Email       string
    Token       string
    NeedsProfile bool  // true when display_name is empty
}
```

The handler checks `NeedsProfile` to decide the redirect target:
- `true` → `{BaseURL}/complete-profile`
- `false` → `{BaseURL}/dashboard`

### 4.4 Provider Configuration

```go
type OAuth struct {
    GoogleClientID     string `env:"OAUTH_GOOGLE_CLIENT_ID"`
    GoogleClientSecret string `env:"OAUTH_GOOGLE_CLIENT_SECRET"`
    GitHubClientID     string `env:"OAUTH_GITHUB_CLIENT_ID"`
    GitHubClientSecret string `env:"OAUTH_GITHUB_CLIENT_SECRET"`
}
```

All four fields are optional. If a provider's client ID is empty, its routes return 404. OAuth is opt-in per deployment — local dev and tests work without configuring any provider.

Added to `config.Config` as `OAuth OAuth` field. No validation — empty means disabled.

### 4.5 Goth Provider Setup

At server startup, if provider credentials are configured:

```go
providers := []goth.Provider{}
if cfg.OAuth.GoogleClientID != "" {
    providers = append(providers, google.New(
        cfg.OAuth.GoogleClientID,
        cfg.OAuth.GoogleClientSecret,
        cfg.Auth.BaseURL+"/api/auth/oauth/google/callback",
        "email", "profile",
    ))
}
if cfg.OAuth.GitHubClientID != "" {
    providers = append(providers, github.New(
        cfg.OAuth.GitHubClientID,
        cfg.OAuth.GitHubClientSecret,
        cfg.Auth.BaseURL+"/api/auth/oauth/github/callback",
        "user:email",
    ))
}
goth.UseProviders(providers...)

// Gothic store — uses HKDF-derived key, not raw secret
oauthKey := auth.DeriveKey(cfg.Auth.TokenSecret, "oauth-state")
gothic.Store = sessions.NewCookieStore(oauthKey)
```

**Goth uses package-level global state** (`goth.UseProviders`, `gothic.Store`). This is a known constraint of the library. Mitigations:
- Initialization is confined to startup (called once in `main.go`)
- OAuth handler tests must NOT use `t.Parallel()` due to shared global state
- Backend.OAuthLogin tests (the business logic) are independent of Goth and can run in parallel

### 4.6 Display Name from Providers

- **Google**: `goth.User.Name` (populated from `profile` scope)
- **GitHub**: `goth.User.Name`, falling back to `goth.User.NickName` if name is empty

If both are empty (GitHub allows this), `display_name` is set to empty string. The `users` table allows this (`TEXT NOT NULL` — empty string satisfies the constraint). The handler redirects to `/complete-profile` where the user provides their name.

### 4.7 Routes

```
GET /api/auth/oauth/{provider}          → OAuthStart handler
GET /api/auth/oauth/{provider}/callback → OAuthCallback handler
```

`{provider}` is `google` or `github`. Any other value → 404.

### 4.8 Error Handling

- Provider returns error (user denied, provider down) → redirect to `{BaseURL}/login?error=oauth_failed`
- `CompleteUserAuth` fails (state mismatch, code exchange fails) → redirect to `{BaseURL}/login?error=oauth_failed`
- `Backend.OAuthLogin` fails (DB error) → redirect to `{BaseURL}/login?error=internal`

All errors redirect rather than returning JSON — the OAuth flow is browser-driven, not API-driven. Error details logged server-side.

### 4.9 Audit Logging

`Backend.OAuthLogin` logs the outcome at `slog.Info` level:

```go
slog.Info("oauth login",
    "provider", provider,
    "email", email,
    "path", "new_user" | "linked_existing" | "existing_oauth" | "reactivated",
    "user_id", userID,
)
```

This satisfies FR-006 (all actions tied to user identity).

---

## 5. CSRF Protection

### 5.1 Approach

`gorilla/csrf` implements the double-submit cookie pattern:

- Sets a `drill_csrf` cookie (not HttpOnly — JS must read it) with a masked token
- State-changing requests (POST, PUT, PATCH, DELETE) must include `X-CSRF-Token` header with the token value
- The masked token changes on every request; gorilla/csrf unmasks both sides for comparison
- Token comparison is timing-safe

### 5.2 Configuration

```go
csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
csrfMiddleware := csrf.Protect(
    csrfKey,
    csrf.Secure(cfg.Auth.SecureCookies()),
    csrf.HttpOnly(false),    // SPA must read cookie via JS
    csrf.CookieName("drill_csrf"),
    csrf.Path("/"),
    csrf.SameSite(csrf.SameSiteLaxMode),
)
```

### 5.3 Exempt Routes

These routes are excluded from CSRF validation:

| Route | Reason |
|---|---|
| `GET *` | Safe method, no state change |
| `POST /api/webhooks/stripe` | Uses Stripe signature verification |
| `GET /api/health` | No auth, no state |

gorilla/csrf already skips GET/HEAD/OPTIONS/TRACE. OAuth callbacks are GET requests (provider redirects with `?code=&state=`), so they're naturally exempt.

For the Stripe webhook (Phase 9), the handler will be registered on a sub-mux without CSRF middleware.

### 5.4 Frontend Integration

The SPA reads the `drill_csrf` cookie value and includes it as `X-CSRF-Token` on every mutating request:

```js
// Frontend: read CSRF token from cookie, include in headers
const csrfToken = document.cookie.match(/drill_csrf=([^;]+)/)?.[1];
fetch('/api/...', {
    method: 'POST',
    headers: { 'X-CSRF-Token': csrfToken },
    ...
});
```

### 5.5 Middleware Wiring

CSRF wraps the entire mux. Applied in `main.go`, not in `RegisterRoutes`:

```go
mux := http.NewServeMux()
handler.RegisterRoutes(mux, b)
protected := csrfMiddleware(mux)
srv := &http.Server{Handler: protected}
```

---

## 6. SessionCookie Helper

Eliminate repeated cookie construction across Login, Logout, and OAuthCallback handlers.

Added to `internal/auth/sessions.go`:

```go
// SessionCookie builds the standard session cookie. Pass an empty token
// and maxAge -1 to create a deletion cookie (logout).
func SessionCookie(token string, maxAge int, secure bool) *http.Cookie {
    return &http.Cookie{
        Name:     SessionCookieName,
        Value:    token,
        Path:     "/",
        HttpOnly: true,
        Secure:   secure,
        SameSite: http.SameSiteLaxMode,
        MaxAge:   maxAge,
    }
}
```

Login, Logout, and OAuthCallback all call `auth.SessionCookie(...)` instead of constructing `http.Cookie` literals. The existing comment in `handler/auth.go` ("HTTP concern — handler owns cookie construction") is updated to reference the helper.

---

## 7. SQL Queries

New file: `sql/queries/oauth_accounts.sql`

```sql
-- name: GetOAuthAccount :one
SELECT * FROM oauth_accounts
WHERE provider = $1 AND provider_id = $2;

-- name: CreateOAuthAccount :one
INSERT INTO oauth_accounts (user_id, provider, provider_id)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetOAuthAccountsByUser :many
SELECT * FROM oauth_accounts
WHERE user_id = $1;
```

New queries in `sql/queries/users.sql`:

```sql
-- name: CreateOAuthUser :one
INSERT INTO users (email, email_verified, display_name)
VALUES ($1, TRUE, $2)
RETURNING *;

-- name: ReactivateUser :exec
UPDATE users SET deleted_at = NULL, email_verified = TRUE, updated_at = NOW()
WHERE id = $1;

-- name: GetUserByEmailIncludingDeleted :one
SELECT * FROM users
WHERE email = $1;
```

`CreateOAuthUser` is separate from `CreateUser` because OAuth users have no password hash and are email-verified at creation. `GetUserByEmailIncludingDeleted` is needed for the soft-delete reactivation path (step 2 of find-or-create).

---

## 8. File Layout

```
internal/
├── auth/
│   ├── keys.go                # DeriveKey (HKDF)
│   ├── keys_test.go
│   ├── sessions.go            # Modified: add SessionCookie helper
│   ├── tokens.go              # Modified: accept derived key instead of raw secret
│   ├── oauth.go               # Goth provider setup, gothic store config
│   └── oauth_test.go
├── backend/
│   ├── auth.go                # Modified: use derived key for TokenSigner, use SessionCookie
│   ├── oauth.go               # OAuthLogin (find-or-create + session creation)
│   └── errors.go              # Modified: add ErrOAuthProviderDisabled
├── handler/
│   ├── auth.go                # Modified: use auth.SessionCookie
│   ├── oauth.go               # OAuthStart, OAuthCallback handlers
│   ├── oauth_test.go
│   └── routes.go              # Modified: add OAuth routes
├── config/
│   └── config.go              # Modified: add OAuth struct
sql/queries/
├── oauth_accounts.sql         # New: OAuth account queries
└── users.sql                  # Modified: add CreateOAuthUser, ReactivateUser, GetUserByEmailIncludingDeleted
```

---

## 9. Testing Strategy

### 9.1 Backend.OAuthLogin — Integration Tests (testcontainers)

Test the find-or-create logic against real Postgres:

| Case | Setup | Expected |
|---|---|---|
| New user, no email match | Empty DB | New user + oauth_account created, email_verified = true, NeedsProfile = false (if name provided) |
| New user, empty display name | Empty DB, provider returns no name | New user created with empty display_name, NeedsProfile = true |
| Existing oauth_account | User + oauth_account exist | Login to existing user, no new rows |
| Email match, no oauth_account | User exists (from email/password signup) | oauth_account linked, email_verified set to true |
| Email match, different provider | User has Google linked, logs in with GitHub | Second oauth_account linked to same user |
| Soft-deleted user, email match | User with deleted_at set | User reactivated (deleted_at cleared), oauth_account linked |
| Duplicate provider_id (race) | Concurrent inserts | Catch unique violation, re-query, return existing |

### 9.2 OAuth Handlers — HTTP Tests

**Constraint: OAuth handler tests must NOT use `t.Parallel()`** due to Goth's global state.

| Case | Expected |
|---|---|
| GET /api/auth/oauth/google | 302 redirect to Google auth URL |
| GET /api/auth/oauth/invalid | 404 |
| Callback success, has display name | Session cookie set, 302 to /dashboard |
| Callback success, no display name | Session cookie set, 302 to /complete-profile |
| Callback with provider error | 302 to /login?error=oauth_failed |
| Provider not configured | 404 |

### 9.3 CSRF — HTTP Tests

| Case | Expected |
|---|---|
| POST without CSRF token | 403 |
| POST with valid CSRF token | passes through |
| GET request | No CSRF check |

### 9.4 HKDF Key Derivation — Unit Tests

| Case | Expected |
|---|---|
| Same secret + same purpose | Same key |
| Same secret + different purpose | Different keys |
| Output is 32 bytes | len(key) == 32 |

### 9.5 SessionCookie — Unit Tests

Verify cookie attributes (HttpOnly, Secure, SameSite, Path, MaxAge) for login and logout cases.

---

## 10. What This Does NOT Include

- **Frontend login/signup pages** — deferred to a later UI phase
- **Profile completion page** — frontend for `/complete-profile` is deferred; the redirect target is established here
- **Account unlinking** — not in the functional requirements
- **OAuth token refresh** — we only use OAuth for initial authentication, not ongoing API access
- **Additional providers** — only Google and GitHub per FR-001; Goth makes adding more trivial later
- **Rate limiting on OAuth endpoints** — deferred to abuse prevention work
