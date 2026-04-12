# HTTP Package Refactor & CSRF Removal — Design

**Date:** 2026-04-12
**Status:** Approved (brainstorm)
**Scope:** `internal/handler/`, `internal/auth/middleware.go`, `internal/rpc/register.go`, `cmd/drill/main.go`, frontend CSRF plumbing

## Goal

Clean up `internal/handler/routes.go` and the surrounding HTTP layer. Remove dead CSRF middleware that protects no routes, sharpen package boundaries, eliminate package overloading (`handler/auth.go`), and consolidate scattered HTTP routing into a single coherent package.

## Background

`internal/handler/routes.go` (233 lines) mixes four concerns: handler assembly (`NewHandler`), route registration (`RegisterRoutes`), SPA serving with OG-tag injection (`SPAHandler` + `ogRoutes`), and CSRF middleware (`csrfMiddleware` + `isCSRFExempt`). The package also contains a misnamed `auth.go` (just a JSON helper and a dead error response) and split files where `security.go` holds a single middleware function adjacent to `auth/middleware.go` which holds two more middleware functions.

The CSRF middleware is operationally dead: every registered non-GET route is in the exemption list (Stripe webhook, `/admin/jobs/`) or Connect-prefixed (Connect's `Connect-Protocol-Version` custom header forces a CORS preflight, which the absence of permissive CORS config then blocks; see Security model below for the full argument). The frontend dutifully sends `X-CSRF-Token` headers for REST mutations, but the only REST mutation wired up (`POST /api/questions` in `web/src/api/queries.ts`) is not registered server-side — it's dead client code. CSRF here is theater that obscures the real security model.

In addition, `cmd/drill/main.go` wraps the HTTP handler in a second mux to register `/storage/` for local-dev file serving, leaking HTTP routing into `main`.

## Decisions

### Security model

**Remove CSRF middleware entirely.** Browser-based CSRF protection comes from three properties of the current architecture, none of which require the CSRF middleware:

1. **Connect's `Connect-Protocol-Version` custom request header** forces a CORS preflight on any cross-origin Connect request. (Connect unary JSON uses `application/json`, which is itself CORS-simple — the custom header is what triggers preflight, not the content type.) Same-origin deployment with no permissive CORS config then blocks the preflight. HTML `<form>` cannot send custom headers at all, so form-based CSRF against Connect is impossible.
2. **Same-origin SPA deployment** with no permissive CORS configured means cross-origin credentialed requests against any endpoint are blocked by the browser.
3. **`SameSite=Lax` session cookies** prevent classical cross-site form-driven CSRF as defense-in-depth.

The Stripe webhook is signature-verified (`internal/handler/billing.go:37` via `webhook.ConstructEventWithOptions`). OAuth callbacks are GETs protected by the `state` parameter — validation is delegated to goth's session-backed check inside `gothic.CompleteUserAuth` (`internal/handler/oauth.go:48`); the state cookie inherits the OAuth session cookie's SameSite/Secure flags configured at `internal/auth/oauth.go`. There are no cookie-authenticated REST mutation endpoints to protect.

If a future cookie-auth REST mutation endpoint is added, the developer must consciously add a `WithCSRF` wrapper at that route. To make this discoverable, `server.go` will carry a comment near `RegisterRoutes` explaining the security model and the opt-in path.

### Package & file shape

Keep `internal/handler/` as the package name. Renaming to `http` would shadow stdlib `net/http`; `drillhttp` was considered (matching the `drilotel` precedent) but `handler` is already in place, used consistently across the repo, and matches the single-word convention of every other internal package.

**Final layout for `internal/handler/`** (after all migration steps below):

| Source | Test |
|---|---|
| `server.go` — `NewHandler(b, spaFS) (http.Handler, error)`; mounts RPC + REST + storage routes; wraps with `otelhttp` and `SecurityHeaders` | — |
| `spa.go` — `SPAHandler`, `ogRoute`, `ogRoutes`, OG injection | `spa_test.go` (renamed from `routes_test.go`) |
| `middleware.go` — `SecurityHeaders`, `RequireAuth`, `RequireAdmin` | `middleware_test.go` (merged from `security_test.go` and `auth/middleware_test.go`) |
| `oauth.go` — `OAuthStart`, `OAuthCallback` | `oauth_test.go`, `oauth_flow_test.go` |
| `health.go` — `Health` | `health_test.go` |
| `stripe.go` — `PostStripeWebhook` (renamed from `billing.go`) | `stripe_test.go` (new) |
| — | `main_test.go` |

**Deleted:**
- `routes.go` (split into `server.go` and `spa.go`)
- `security.go` (`SecurityHeaders` moves into `middleware.go`)
- `auth.go` (`writeJSON` and `writePaidBalanceRequired` are dead post-Connect migration; verify no callers and delete)
- `csrf_test.go`
- `auth/middleware.go` and `auth/middleware_test.go` (moved to `handler/`)

**Public API of `handler`:** `NewHandler`, `SPAHandler`, `SecurityHeaders`, `RequireAuth`, `RequireAdmin`, `Health`, `OAuthStart`, `OAuthCallback`, `PostStripeWebhook`. `RegisterRoutes` is no longer public.

### Single entry point: collapse `NewHandler` and `RegisterRoutes`

Today `RegisterRoutes` is public so 8 test sites can build a bare mux without the OTel + SecurityHeaders chain. Collapse to a single `NewHandler` and migrate test sites. Higher-fidelity tests (real middleware chain), one entry point, removes the question "which do I use?" `RegisterRoutes` becomes an unexported `registerRoutes` helper inside `server.go` if the body is non-trivial.

### Storage handler consolidation

The local-dev storage mux currently lives in `cmd/drill/main.go` (lines 88-96), wrapping the HTTP handler in a second mux. Move into `handler.NewHandler` by reading `cfg.Storage` from `b.Config()`. `registerRoutes` mounts `/storage/` as a real route when `cfg.Storage.Backend == "local"`. One mux, one place.

### Auth package boundary

Keep `internal/auth/` as the identity primitives package: `sessions.go`, `tokens.go`, `passwords.go`, `oauth.go`, `keys.go`. Move `middleware.go` (HTTP middleware) to `handler/middleware.go` — it operates on `http.Request`, mounts onto routes, and its natural neighbors are `SecurityHeaders` and the route registration.

`auth.DeriveKey` has callers for `oauth-state` and `hmac-tokens` (verified). `keys.go` stays. Only the `csrf` purpose call site disappears.

## Migration order

Each step ends with `make test` green. Each step is independently revertable.

### Step 1 — Delete CSRF surface

- Delete `internal/handler/csrf_test.go`.
- Delete `csrfMiddleware`, `isCSRFExempt` from `routes.go`.
- Simplify the `NewHandler` middleware chain to `SecurityHeaders(otelHandler)` (drop the CSRF wrap).
- Delete `auth.DeriveKey(..., "csrf")` call in `routes.go`.
- In `internal/handler/oauth_test.go`: delete the 3 `csrfKey := auth.DeriveKey(..., "csrf")` lines AND the surrounding CSRF-wrap setup blocks AND the `X-CSRF-Token` echo assertions (lines ~87-118 — verify exact range during impl). Tests should call the handler under test directly.
- Remove `gorilla/csrf` from `go.mod`; run `go mod tidy`.
- Frontend: remove `getCsrfToken`, `mutationHeaders` CSRF logic, and the `Content-Type` branch reshuffling in `web/src/api/client.ts`. Remove `window.__csrfToken` declaration from `web/src/vite-env.d.ts` and assignment in `web/src/main.tsx`. Delete the dead `useCreateQuestion` from `web/src/api/queries.ts` (boy-scout cleanup).
- Update `web/src/api/__tests__/client.test.ts` (remove CSRF assertions).
- Delete or stub `docs/csrf-audit.md` (228-line document describing the now-removed implementation). Replace with a one-line stub pointing to this spec.
- Note: existing `sabermatic_csrf` cookies in client browsers will expire naturally; no cleanup action required.
- Verify: `make test` + browser smoke (login, session create, billing checkout, OAuth login).

### Step 2 — Delete `ConnectPathPrefixes()`

- Remove function from `internal/rpc/register.go` (only caller was CSRF, gone in step 1).
- Verify: `go build ./...` + `make test`.

### Step 3 — Move storage mux into `NewHandler`

- Verify `Backend.Config()` exposes `Storage.Backend` and `Storage.LocalDir` (used in `cmd/drill/main.go:93` today).
- Add `/storage/` registration inside `RegisterRoutes` conditional on `b.Config().Storage.Backend == "local"`.
- Delete the wrapping mux block in `cmd/drill/main.go` (lines 88-96).
- Verify: local dev (`make dev`) serves uploaded files in browser; confirm audio playback and image rendering still work under the production CSP (`security.go:14`: `default-src 'self'; img-src 'self' data: https://storage.googleapis.com; connect-src 'self' wss:; ...`). `'self'` covers local `/storage/` paths, but verify `<audio>`/`<img>` elements don't hit any `media-src` or `connect-src` violation in the browser console.

### Step 4 — Move `auth/middleware.go` to `handler/middleware.go`

- Move source and tests.
- Merge `handler/security_test.go` content into the new `middleware_test.go`.
- Delete `handler/security.go` (move `SecurityHeaders` body into `middleware.go`).
- Update import sites.
- Verify: `make test`.

### Step 5 — Split `routes.go`

- Extract `SPAHandler`, `ogRoute`, `ogRoutes`, OG injection into `spa.go`.
- Rename `routes_test.go` → `spa_test.go`.
- Rename `routes.go` → `server.go`. It now contains only `NewHandler` + `RegisterRoutes`.
- Add the security-model comment near `RegisterRoutes` explaining why there is no CSRF middleware and how to add `WithCSRF` if a future REST mutation endpoint needs it.
- Verify: `make test`.

### Step 6 — Collapse `NewHandler` + `RegisterRoutes`

- Migrate 8 test sites that call `handler.RegisterRoutes(mux, b)` to call `handler.NewHandler(b, fstest.MapFS{...minimal index.html...})` instead. Tests accept the full middleware chain (SecurityHeaders headers in responses; OTel no-op spans).
- Delete public `RegisterRoutes`. Body becomes unexported `registerRoutes` inside `server.go`.
- **Test-runtime check:** `oauth_flow_test.go` calls `RegisterRoutes` 5 times across subtests; `testutil` is reused across many packages. After migration, time the handler test packages (`go test ./internal/handler/... -count=1`) and compare to a pre-Step-6 baseline. If the regression is meaningful (>20% wall-clock), introduce a `handler.newTestHandler(b)` helper that skips `otelhttp.NewMiddleware` (still keeps SecurityHeaders, since those have no per-request cost). If the regression is negligible, leave the single entry point.
- Verify: `make test`.

### Step 7 — Rename `billing.go` → `stripe.go`; delete `auth.go`; add Stripe webhook test

- Rename `billing.go` → `stripe.go`.
- Verify `writeJSON` and `writePaidBalanceRequired` have no callers; delete `handler/auth.go`.
- Add `stripe_test.go` covering: signature-verify happy path (returns 200 and calls `HandleStripeWebhook`), signature-verify failure (returns 400, no handler call), missing webhook secret (returns 200 with logged error — deliberate: avoids Stripe retry storms when secret is misconfigured; webhook failures should fail loud in logs, not via HTTP error). Use `webhook.GenerateTestSignedPayload` (`stripe-go/v82@v82.5.1 webhook/client.go:314` — verified present) for the signed body.
- Verify: `make test`.

## Verification

- **Per step:** `make test` (full CI: buf lint, codegen check, `tsc -b`, eslint, vitest, `go test -race`).
- **After steps 1, 3, 6** (steps that change runtime routing): browser smoke testing — login, session create, billing checkout, OAuth login, Stripe webhook via `stripe listen`.
- **Final:** subagent review rounds (Opus, sonnet) until clean per project process.

## Risks

- **CSRF removal blast radius.** A future cookie-auth REST mutation endpoint added without `WithCSRF` would be vulnerable. Mitigated by the security-model comment in `server.go` (step 5).
- **Test migration in step 6** is mechanical but touches files not fully read during brainstorm. Watch for hidden setup logic in `oauth_flow_test.go` and `testutil/testutil.go`.
- **Frontend smoke gap.** Removing `X-CSRF-Token` from request headers must not break any code path that reads it from the request. Verified: backend has no `r.Header.Get("X-CSRF-Token")` callers outside `gorilla/csrf`.
- **Stale references in historical plan docs.** Plans under `docs/superpowers/plans/` (e.g., `2026-04-02-phase3b-oauth-csrf.md`) reference the CSRF middleware being removed. These are historical records of completed work and are intentionally left as-is.
- **Step independence.** Steps 1-7 are individually revertable: each ends on green CI and modifies a disjoint slice. Steps 4 and 5 both edit files in `handler/` but touch different files (`middleware.go`/`security.go` vs `routes.go`/`spa.go`), so reverting either does not break the other.

## Out of Scope

- Refactoring `auth/oauth.go` complexity (`oauthLoginWithRetry`) — separate planned refactor.
- Touching `internal/rpc/*` services (only `register.go` for `ConnectPathPrefixes` deletion).
- Further reorganizing `internal/auth/` (sessions/tokens/passwords/oauth/keys stay as-is).
- Consolidating `oauth_test.go` + `oauth_flow_test.go` (separate decision; flagged for later).
- Adding any new functionality.
- Adding opt-in `WithCSRF` helper now (YAGNI; add when first cookie-auth REST mutation appears).
