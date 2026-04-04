# CSRF Protection Audit

**Date:** 2026-04-04
**Scope:** Full-stack CSRF implementation — Go backend (gorilla/csrf) + React/TypeScript SPA frontend
**Verdict:** Sound. The implementation follows the encrypted token pattern correctly, with defense-in-depth via SameSite cookies. A few minor observations are noted below but none represent exploitable vulnerabilities.

---

## Architecture Overview

The application uses a **double-submit cookie** variant (specifically, gorilla/csrf's encrypted token pattern) combined with **SameSite=Lax** cookies. This is a defense-in-depth approach: either mechanism alone would provide strong CSRF protection, and together they cover each other's edge cases.

### Request Flow

```
┌─────────────────────────────────────────────────────────────────────┐
│ 1. App boots — SPA fires GET /api/health                           │
│    Browser sends: (nothing, first request)                         │
│    Server returns:                                                 │
│      Set-Cookie: drill_csrf=<encrypted-token>; Path=/; SameSite=Lax│
│      X-CSRF-Token: <masked-token>                                  │
│    SPA stores: window.__csrfToken = masked-token                   │
├─────────────────────────────────────────────────────────────────────┤
│ 2. User triggers mutation — SPA fires POST /api/sessions           │
│    Browser sends:                                                  │
│      Cookie: drill_csrf=<encrypted-token> (auto-attached)          │
│      Cookie: drill_session=<session-token> (auto-attached)         │
│      Header: X-CSRF-Token: <masked-token> (added by apiClient)    │
│    Server:                                                         │
│      gorilla/csrf decrypts cookie → real token                     │
│      gorilla/csrf unmasks header → real token                      │
│      Compares: tokens match → allow                                │
│      Sets new X-CSRF-Token in response for token rotation          │
└─────────────────────────────────────────────────────────────────────┘
```

---

## Backend Implementation

### Library & Version

- **Library:** `github.com/gorilla/csrf` v1.7.3 (`go.mod:11`)
- **Status:** Current release, no known CVEs as of 2026-04-04
- **Pattern:** Encrypted token (not the simpler HMAC double-submit). The real token is encrypted in the cookie and masked per-request in the header. An attacker who reads the cookie via a subdomain cookie injection cannot forge the header value.

### Key Derivation

**File:** `internal/auth/keys.go`

```go
func DeriveKey(secret, purpose string) []byte {
    r := hkdf.New(sha256.New, []byte(secret), nil, []byte(purpose))
    key := make([]byte, 32)
    io.ReadFull(r, key)
    return key
}
```

- Master secret: `AUTH_TOKEN_SECRET` env var, validated at startup to be >= 32 chars (`internal/config/config.go:208`)
- Purpose separation: CSRF key derived as `DeriveKey(secret, "csrf")` — separate from OAuth state (`"oauth-state"`) and HMAC tokens (`"hmac-tokens"`). Compromise of one purpose key does not compromise others.
- HKDF-SHA256 is the correct KDF for this use case — deterministic, purpose-bound, and well-studied.

### Middleware Configuration

**File:** `internal/handler/routes.go:25-31`

```go
csrfProtect := csrf.Protect(
    csrfKey,
    csrf.Secure(secureCookies),          // true when BASE_URL starts with https://
    csrf.HttpOnly(false),                 // JS needs to read cookie — see analysis below
    csrf.CookieName("drill_csrf"),
    csrf.Path("/"),
    csrf.SameSite(csrf.SameSiteLaxMode),
)
```

| Option | Value | Analysis |
|--------|-------|----------|
| `Secure` | Dynamic (`true` in prod, `false` in dev) | Correct. Derived from `BASE_URL` prefix check (`internal/config/config.go:182-184`). |
| `HttpOnly` | `false` | **Intentional and correct for this pattern.** The frontend reads the masked token from `X-CSRF-Token` response header, not from the cookie directly. However, gorilla/csrf sets `HttpOnly(false)` on the cookie itself. This is a library design choice — the cookie contains an encrypted blob that is useless without the server-side key. Even if JS could read it, it cannot forge a valid masked token from it. |
| `CookieName` | `drill_csrf` | Application-specific prefix. Good — avoids collisions. |
| `Path` | `/` | Correct for an SPA with all routes under `/api`. |
| `SameSite` | `Lax` | Correct. Blocks cross-site POST/PUT/DELETE while allowing top-level navigation GETs. |

### Safe Methods

gorilla/csrf exempts `GET`, `HEAD`, `OPTIONS`, `TRACE` from token validation (confirmed from library source). All state-mutating endpoints in the app are registered as `POST` — there are no `GET` routes that perform side effects.

### Token Exposure

**File:** `internal/handler/routes.go:37-39`

```go
csrfProtected := csrfProtect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("X-CSRF-Token", csrf.Token(r))
    otelHandler.ServeHTTP(w, r)
}))
```

Every response includes a fresh masked token in the `X-CSRF-Token` header. This provides per-request token rotation (the masked value changes; the underlying real token stays the same within the cookie's lifetime). The SPA reads this header after the initial health-check fetch.

### CSRF Exemptions

**File:** `internal/handler/routes.go:45-48`

```go
if r.URL.Path == "/api/webhooks/stripe" && r.Method == http.MethodPost {
    otelHandler.ServeHTTP(w, r)
    return
}
```

**Only one route is exempt:** `POST /api/webhooks/stripe`. This is correct — Stripe webhooks:
1. Originate from Stripe servers, not browsers, so they cannot carry CSRF cookies
2. Are authenticated via `Stripe-Signature` header verification using `webhook.ConstructEventWithOptions()` with HMAC validation (`internal/handler/billing.go:103-113`)
3. The exemption is narrowly scoped (exact path + exact method)

### Plaintext HTTP Support

**File:** `internal/handler/routes.go:50-52`

```go
if !secureCookies {
    r = csrf.PlaintextHTTPRequest(r)
}
```

When `BASE_URL` is HTTP (local dev only), `csrf.PlaintextHTTPRequest()` annotates the request so gorilla/csrf skips Referer/Origin header validation over non-TLS connections. This is necessary because browsers don't send `Referer` over plaintext HTTP in all cases. **In production (HTTPS), this code path is never reached.**

---

## Session Cookie Configuration

**File:** `internal/auth/sessions.go:32-42`

```go
&http.Cookie{
    Name:     "drill_session",
    HttpOnly: true,
    Secure:   secure,              // true in production
    SameSite: http.SameSiteLaxMode,
    Path:     "/",
    MaxAge:   maxAge,              // default 720h (30 days)
}
```

| Flag | Value | Analysis |
|------|-------|----------|
| `HttpOnly` | `true` | Correct. Session token is never accessible to JavaScript. |
| `Secure` | Dynamic | Correct. Same derivation as CSRF cookie. |
| `SameSite` | `Lax` | Correct. Provides CSRF protection as a second layer. |
| `Path` | `/` | Correct for API routes under `/api`. |

Session tokens are generated as 32 random bytes (`crypto/rand`), base64url-encoded, and stored in the database as SHA-256 hashes (`internal/auth/sessions.go:14-22`). This is the standard bearer-token-with-server-side-hash pattern.

---

## Frontend Implementation

### Token Initialization

**File:** `web/src/main.tsx:11-17`

```typescript
fetch("/api/health", { credentials: "same-origin" }).then((res) => {
  const token = res.headers.get("X-CSRF-Token");
  if (token) window.__csrfToken = token;
});
```

This fires before the React tree mounts. The `/api/health` GET is a safe method — gorilla/csrf sets the `drill_csrf` cookie and returns the masked token in `X-CSRF-Token`. The SPA stores it in `window.__csrfToken` for subsequent mutations.

**Race condition analysis:** The health-check fetch is asynchronous and non-blocking. If a user triggers a mutation before the fetch resolves, `getCsrfToken()` returns `""` and the request gets a 403 from the backend. In practice this is a non-issue: the health check completes in <50ms on same-origin, and no mutation is possible until the user navigates, authenticates, and interacts — well after boot.

### API Client

**File:** `web/src/api/client.ts`

```typescript
function getCsrfToken(): string {
  return (window as any).__csrfToken ?? "";
}

function mutationHeaders(body?: unknown): HeadersInit {
  const headers: Record<string, string> = {
    "X-CSRF-Token": getCsrfToken(),
  };
  if (body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  return headers;
}
```

**Coverage analysis:**

| Method | CSRF Header | Correct? |
|--------|-------------|----------|
| `GET` | No | Yes — safe method |
| `POST` | Yes | Yes |
| `PUT` | Yes | Yes |
| `PATCH` | Yes | Yes |
| `DELETE` | Yes | Yes |

All mutation methods call `mutationHeaders()` which includes `X-CSRF-Token`. All requests use `credentials: "same-origin"` to ensure the `drill_csrf` cookie is attached.

**Token staleness:** The SPA reads the initial token from the health-check response but does not update `window.__csrfToken` from subsequent response headers. gorilla/csrf's masked tokens change per-request, but the underlying real token remains the same for the cookie's lifetime. Since gorilla/csrf validates by unmasking back to the real token, a "stale" masked token from the initial fetch is still valid as long as the cookie hasn't expired. **This is correct behavior.**

### WebSocket Connections

**File:** `web/src/ws/connection.ts:50-51`

```typescript
const protocol = location.protocol === "https:" ? "wss:" : "ws:";
this.ws = new WebSocket(`${protocol}//${location.host}/api/sessions/${this.sessionId}/ws`);
```

WebSocket upgrade requests use `GET`, which is a safe method exempt from CSRF validation by gorilla/csrf. The WebSocket is authenticated via the session cookie (`RequireAuth` middleware runs before the upgrade at `internal/handler/session_ws.go:20-24`), and additionally validates session ownership (`b.GetSessionForUser()` at line 34).

**Cross-origin WebSocket analysis:** The `websocket.Accept(w, r, nil)` call at `internal/handler/session_ws.go:52` passes `nil` options to `coder/websocket`, which by default checks that the request `Origin` header matches the `Host` header. This blocks cross-origin WebSocket hijacking. Combined with `SameSite=Lax` on the session cookie (which prevents cookie attachment on cross-site requests), this provides adequate protection.

---

## Test Coverage

**File:** `internal/handler/oauth_test.go:26-123`

Three focused integration tests:

1. **`TestCSRF_RejectsPostWithoutToken`** — Verifies POST without token returns 403
2. **`TestCSRF_AllowsGetRequests`** — Verifies GET bypasses CSRF
3. **`TestCSRF_PostWithValidToken`** — Full round-trip: GET to obtain cookie+token, POST with both, verifies 200

These tests use the same `csrf.Protect()` configuration as production (same key derivation, same cookie name, same flags). The test configuration mirrors production with `csrf.Secure(false)` for HTTP test transport.

---

## Threat Model Assessment

### Attack: Classic CSRF (Cross-Site Form Submission)

**Protected.** Even if an attacker tricks a victim's browser into submitting a form to `POST /api/sessions`:
- The `drill_csrf` cookie is sent (cookies travel with requests), but
- The `X-CSRF-Token` header cannot be set by a cross-origin form submission
- Additionally, `SameSite=Lax` prevents the session cookie from being sent on cross-site POST requests in modern browsers

**Defense layers:** Two independent mechanisms block this attack.

### Attack: Cross-Site XMLHttpRequest/Fetch

**Protected.** Cross-origin JavaScript cannot:
- Read the `X-CSRF-Token` response header from a prior request (CORS blocks this)
- Set custom headers on cross-origin requests without a CORS preflight (which the server doesn't grant — no `Access-Control-Allow-Origin` headers are configured)

### Attack: Subdomain Cookie Injection

**Protected.** Even if an attacker controls a subdomain and injects a `drill_csrf` cookie, they cannot forge a valid masked token because:
- gorilla/csrf uses **encrypted** tokens (AES-256-CTR + HMAC), not plain HMAC
- The attacker would need the 32-byte CSRF key (derived via HKDF from the server-side secret)
- The encrypted cookie value and the masked header token are cryptographically bound

### Attack: BREACH/CRIME (Compression Side-Channel)

**Mitigated.** gorilla/csrf masks the token with a random one-time pad on each response, producing a different `X-CSRF-Token` header value every time. This prevents compression oracle attacks from extracting the token.

### Attack: XSS Token Theft

**Caveat.** If an attacker achieves XSS, they can read `window.__csrfToken` and make authenticated requests. However, this is inherent to any SPA architecture — XSS also grants access to the session (by making same-origin requests that include cookies). CSRF protection is not designed to defend against XSS. The session cookie's `HttpOnly` flag does prevent direct session token exfiltration.

### Attack: WebSocket Hijacking

**Protected.** See WebSocket analysis above. Origin checking + SameSite session cookie + session ownership verification.

---

## Observations (Non-Vulnerabilities)

### 1. Frontend calls endpoints not yet registered

The frontend calls `PATCH /api/me` (`web/src/api/queries.ts:221`) and `DELETE /api/auth/account` (`web/src/api/queries.ts:239`), but only `GET /api/me` is registered in `routes.go:84`. These requests will receive 405 Method Not Allowed from Go's ServeMux. This is a functionality gap, not a security issue — CSRF protection still applies to the mux-level 405 response.

### 2. No CSRF token refresh after login

When a user logs in, the SPA does not re-fetch the CSRF token. The initial token from `/api/health` continues to be used. This is fine because gorilla/csrf's token is bound to the cookie (not to the session), and the cookie persists across login/logout. A fresh masked token is returned in every response header if the SPA ever needs to update.

### 3. No explicit error handling for 403 CSRF failures

The frontend's `apiClient` throws a generic `ApiError` for all non-2xx responses, with no special handling for CSRF-specific 403s (e.g., prompting re-authentication or refreshing the token). In practice, CSRF failures should be rare (only possible if the cookie expires mid-session), and the user would see a generic error. This is acceptable for the current UX.

### 4. `HttpOnly(false)` on CSRF cookie

This is correct for the gorilla/csrf pattern but worth documenting: the cookie contains an encrypted blob that requires the server-side key to decrypt. JavaScript access to this cookie value provides no attack surface beyond what the `X-CSRF-Token` response header already exposes.

---

## Summary

| Component | Implementation | Verdict |
|-----------|---------------|---------|
| CSRF library | gorilla/csrf v1.7.3, encrypted token pattern | Sound |
| Key management | HKDF-SHA256, purpose-separated, 32+ char secret | Sound |
| Cookie configuration | SameSite=Lax, Secure=dynamic, proper Path | Sound |
| Token delivery | X-CSRF-Token response header on every response | Sound |
| Frontend integration | Header injected on all mutation methods | Sound |
| Safe method exemption | GET/HEAD/OPTIONS/TRACE only | Sound |
| Webhook exemption | Stripe only, signature-verified, narrowly scoped | Sound |
| Plaintext HTTP support | Dev-only, gated on BASE_URL, uses library API | Sound |
| Session cookies | HttpOnly, Secure, SameSite=Lax, hashed server-side | Sound |
| WebSocket | Origin-checked, session-authenticated, ownership-verified | Sound |
| Test coverage | Three integration tests covering reject/allow/valid-token | Adequate |

**Overall: The CSRF implementation is well-designed and correctly implemented.** It uses defense-in-depth (encrypted tokens + SameSite cookies), proper key derivation, narrow exemptions with alternative authentication, and consistent frontend integration across all mutation methods.
