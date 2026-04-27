# 2026-04-26 — OAuth error visibility on /login and /signup

Surface OAuth callback errors to the user instead of swallowing them silently.

## Motivation

`internal/handler/oauth.go:73,89` redirects to `/login?error=oauth_failed` when
`gothic.CompleteUserAuth` fails (provider error, state mismatch, network blip
to Google's userinfo endpoint), and to `/login?error=internal` when
`b.OAuthLogin` returns an error (DB blip, contention, anything in the
transaction). Both redirects work — but neither `web/src/pages/auth/login.tsx`
nor `web/src/pages/auth/signup.tsx` reads the `error` query parameter.

Confirmed by code reading: only `searchParams.get("redirect")` is consumed in
either page; a grep over `web/src` finds zero readers of the OAuth error
codes. The user therefore sees a clean-looking login page after a failed
OAuth attempt with no indication that anything went wrong, naturally re-clicks
"Sign in with Google", and the second attempt usually succeeds because the
underlying transient has cleared. This presents to the user as "first time
fails sometimes, retry works" — a perceived race that is actually a
visibility bug.

This change does NOT attempt to fix the underlying transient backend errors.
It only makes them visible. Knowing whether a given failure is `oauth_failed`
vs `internal` vs "user landed on `/` with no error param at all" is the
disambiguating data we need to pick the next root cause to chase. The
visibility fix is independently correct regardless of what we do next.

## Out of scope

- Reinstating retry logic in `internal/backend/oauth.go` `OAuthLogin`. The
  recent commit `281cec1` deliberately removed the retry loop. Whether to add
  narrow retry on identifiable transient pgx errors (connection reset,
  serialization failure, deadline exceeded) is a separate question that
  depends on what the next failure's error code turns out to be.
- Changing `ConditionalHome`'s silent fallthrough to `<Landing />` on a
  `getMe` error (commit `ec21538`). That is the secondary suspect — separate
  spec if logs end up implicating it.
- Preserving the `redirect` cookie across an OAuth failure. The cookie has a
  5-minute TTL and is left in place by the failure-redirect path, so the
  next OAuth attempt will still honor it. No change needed.
- Changing the backend error codes themselves. We work with what the backend
  already emits.

## Design

### Component

A small presentational component, `OAuthError`, that takes a query-param
value and renders a friendly inline message. Co-located with the auth pages
that use it.

`web/src/pages/auth/oauth-error.tsx` (new):

- Accepts a single `code: string | null` prop.
- If `code` is `null`, renders nothing.
- Maps known codes to specific messages:
  - `oauth_failed` → "Sign-in didn't complete. Please try again."
  - `internal` → "Something went wrong on our end. Please try again."
- Any other (unknown) non-null code falls back to the same message as
  `oauth_failed` so that a future backend code is not silently dropped.
- Renders the message in a `<p role="alert">` with the same orange-muted
  styling already used for the inline form error in `login.tsx:101-103`
  (`text-sm text-orange-500`). `role="alert"` causes screen readers to
  announce the message.

### Integration

In `web/src/pages/auth/login.tsx` and `web/src/pages/auth/signup.tsx`:

- Read `searchParams.get("error")` (the `searchParams` object is already in
  scope in both pages).
- Render `<OAuthError code={searchParams.get("error")} />` immediately above
  the Google + GitHub OAuth buttons. This places the message contextually
  next to the action it concerns; the existing form-submission error stays
  in its current position next to the password field.

No URL cleanup. Refreshing a page that has `?error=…` in the URL will re-show
the message — that is acceptable: the only path that puts the param in the
URL is an actual failed callback, and a stale message on refresh is far less
costly than a silent failure.

### Tests

`web/src/pages/auth/__tests__/oauth-error.test.tsx` (new):

- `code={null}` → renders nothing (assert `container` is empty).
- `code="oauth_failed"` → renders "Sign-in didn't complete. Please try
  again." inside an element with `role="alert"`.
- `code="internal"` → renders "Something went wrong on our end. Please try
  again." inside an element with `role="alert"`.
- `code="future_unknown_code"` → renders the same fallback message as
  `oauth_failed` (locks in the "don't silently drop unknown codes" behavior).

The component is pure presentation, so no router or query-client mocking is
needed. Tests live alongside the component file under
`web/src/pages/auth/__tests__/`.

No new tests are added to `login.tsx` or `signup.tsx`. Both pages already
pull connect-query mutations (`useLogin`, `useSignup`) and adding render
tests for them would require mocking the connect-rpc surface — disproportionate
for a one-line JSX insertion. The `OAuthError` component itself carries the
test coverage.

## Verification

- `cd web && npx vitest run src/pages/auth/__tests__/oauth-error.test.tsx` —
  all four tests pass.
- `cd web && npx vitest run` — full suite passes; total count rises by four.
- `cd web && npx tsc -b` — clean.
- `cd web && npm run lint` — no new errors or warnings.
- Manual browser check (production or local): visit `/login?error=oauth_failed`
  and confirm the message appears above the Google button. Repeat with
  `/login?error=internal` and `/signup?error=oauth_failed`.

## Risks

- Low. The change adds a single optional element to two pages. If the
  component throws or mis-renders, the surrounding page still works.
- The friendly messages may not match every future failure mode if the
  backend adds new error codes. The fallback message handles that case
  gracefully (shown rather than dropped).
- Adds ~30 lines of code (1 component + 1 test file + 2 one-line JSX
  insertions in existing pages). No dependency changes.
