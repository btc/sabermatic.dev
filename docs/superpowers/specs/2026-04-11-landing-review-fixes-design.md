# Landing Page Review Fixes & Routes Cleanup

Address all findings from three deep-dive Opus reviews of the `feature/sabermatic-landing` branch, plus routes.go refactoring opportunities. File separate GitHub issues for pre-existing main issues.

## Replay engine fixes

### Unstable useMemo dependency

`web/src/components/replay/engine.ts`: `useMemo` for `timings` depends on `options` — an object literal created inline by the caller (`transcript.tsx`) every render, defeating memoization. Fix: memoize the `options` object at the call site in `transcript.tsx` using `useMemo` with stable dependencies (e.g., `[replayMode, messages, session?.startTime, session?.endTime, evaluation?.annotations]`). The engine's `useMemo` for `timings` then works correctly because its dependency (`options`) is now referentially stable.

### useEffect without dependency arrays

Same file: three `useEffect` calls update refs (`optionsRef`, `timingsRef`, `tickRef`) with no dependency array. This is the "latest ref" pattern — it runs after every render to ensure refs always hold current values. The pattern is correct and intentional for the animation loop (prevents stale closures). Fix: do NOT add dependency arrays (that would break the pattern by making refs stale). Instead, add a comment explaining the intent:

```ts
// Latest-ref pattern: runs after every render to keep refs in sync.
// Do not add a dependency array — the tick callback must always
// read current options/timings without stale closures.
```

### ConditionalHome error state

`web/src/app.tsx`: the fallthrough error state shows "Something went wrong" with no way to recover. The fallthrough represents non-401 errors (500s, network failures, or unexpected states where data is undefined with no error). Fix: expose `refetch` and `error` from `useOptionalAuth`, add a retry button that calls `refetch()`, add a link to `/about`, and log the error to console for debugging:

```tsx
// In the fallthrough branch:
<Button onClick={() => refetch()}>Try again</Button>
<Link to="/about">Go to about page</Link>
```

Update `useOptionalAuth` in `web/src/hooks/use-auth.ts` to return `refetch` and `error` alongside existing fields.

## Transcript memoization

`web/src/pages/session/transcript.tsx`: `annotationsBySeq` Map is rebuilt every render. Wrap in `useMemo`. Use `evaluation?.annotations` as the dependency (not the derived `annotations` variable, which creates a new `[]` reference on every render when undefined):

```tsx
const annotations = evaluation?.annotations ?? [];
const annotationsBySeq = useMemo(
  () => annotations.reduce<Map<number, Annotation[]>>(...),
  [evaluation?.annotations],
);
```

## SampleSessionLink empty card

`web/src/pages/landing/sample-session.tsx`: renders a visible empty card shell (border, background, padding) while session data loads. The entire component should return `null` when no session data is available — same pattern as other landing sections (scoring, strengths-gaps, etc. all guard with `if (!data) return null`).

## Type narrowing on scores

`web/src/pages/landing/scoring.tsx`: `scores[dim.key]` dynamic access. `DIMENSIONS` is typed `as const` which already narrows `dim.key` correctly, so this compiles without a cast. Add explicit cast for consistency with `overview.tsx`: `scores[dim.key as keyof EvaluationScores] as number`.

`web/src/pages/session/overview.tsx`: already has the cast at line 86. No change needed here.

## routes.go refactoring

### CSRF exemption extraction

Extract the growing `if/else` chain in `csrfMiddleware` into a standalone `isCSRFExempt(path, method string, connectPrefixes []string) bool` function. The three current exemptions (Stripe webhook, RiverUI, ConnectRPC) consolidate into one readable predicate. Remove the TODO comment — the extraction addresses it.

Add unit tests for `isCSRFExempt` covering: Stripe webhook POST (exempt), Stripe webhook GET (not exempt), RiverUI paths (exempt), ConnectRPC paths (exempt), regular paths (not exempt). This is the most important behavioral contract in the middleware chain and currently has zero test coverage.

### SecurityHeaders hoisted to top level

Currently `SecurityHeaders` wraps inside `csrfMiddleware`, creating unexpected coupling. Hoist it to `NewHandler` so it wraps the entire handler chain:

```
NewHandler chain: SecurityHeaders(secureCookies, csrfMiddleware(otelHandler, csrfKey, secureCookies))
```

`csrfMiddleware` keeps its `secureCookies` parameter — it's used for `csrf.Secure(secureCookies)` and `csrf.PlaintextHTTPRequest()`, not just SecurityHeaders. The only change is removing the `SecurityHeaders(...)` wrapping from inside `csrfMiddleware` and adding it in `NewHandler`.

### Cookie rename

`csrf.CookieName("drill_csrf")` → `csrf.CookieName("sabermatic_csrf")`.

Migration impact: on the first POST after deployment, existing users' browsers will send the old `drill_csrf` cookie. The middleware won't recognize it (it now looks for `sabermatic_csrf`), so the first mutating request will fail with a 403 CSRF error. The user's next page load will set the new cookie and subsequent requests work. This is a one-time disruption per user.

Acceptable tradeoff — the failure is self-healing on page refresh. No grace period needed.

### NewHandler parameter type

Change `spaFS embed.FS` to `spaFS fs.FS` in `NewHandler` for consistency with `SPAHandler` and testability. The caller in `main.go` passes `drill.WebFS` (an `embed.FS` which implements `fs.FS`), so no caller changes needed.

## OG tag hardening

### Escape all interpolated values

`internal/handler/routes.go`: `baseURL`, `path`, and `og.image` are not passed through `html.EscapeString()`. All values are currently developer-controlled constants (config, map keys, struct fields), so the risk is negligible today. Apply escaping to all 6 format arguments as defense-in-depth.

### Missing `</head>` fallback

If `index.html` doesn't contain `</head>`, OG tags are silently not injected. Add a `slog.Warn` during `SPAHandler` init if `</head>` is absent. Don't error — the SPA still works, just without OG tags.

## OG images

Create two OG images (1200x630) matching the favicon aesthetic. The favicon is an SVG with "[.D]" in amber monospace (`#b45309` brackets, `#92400e` text) on transparent background.

OG images use the same palette on a warm cream background (`#faf5ef` or similar):

**`og-landing.svg` / `og-landing.png`:**
```svg
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1200 630">
  <rect width="1200" height="630" fill="#faf5ef"/>
  <text x="600" y="280" text-anchor="middle" font-family="ui-monospace,'Cascadia Code','Fira Code',monospace" font-size="64" fill="#92400e">
    <tspan fill="#b45309" font-weight="500">[</tspan><tspan font-weight="700">.D</tspan><tspan fill="#b45309" font-weight="500">]</tspan>
  </text>
  <text x="600" y="350" text-anchor="middle" font-family="ui-sans-serif,system-ui,sans-serif" font-size="36" font-weight="300" fill="#57534e">
    Sabermatic
  </text>
  <text x="600" y="400" text-anchor="middle" font-family="ui-sans-serif,system-ui,sans-serif" font-size="20" fill="#a8a29e">
    data-driven system design prep
  </text>
</svg>
```

**`og-sample.svg` / `og-sample.png`:**
Same as above but tagline reads "sample evaluation".

Convert SVGs to PNGs via Playwright screenshot or `rsvg-convert`. Commit both SVGs and PNGs to `web/public/`.

## Deep-dive TOC responsive

`web/src/pages/session/deep-dive.tsx`: TOC `<nav>` has fixed `w-48` with no responsive breakpoint. Add `hidden lg:block` to the `<nav>` element inside the `TOC` component (not a wrapper — `TOC` already returns `null` for empty headings). Change the outer flex wrapper from `flex gap-10` to `flex-col lg:flex-row lg:gap-10` so the content column gets full width on small screens.

## Duplicate formatTime

`web/src/components/replay/controls.tsx` and `timeline.tsx` both define identical `formatTime`. Extract to `web/src/components/replay/utils.ts`. Import from both files.

## Sample mode retry guard

`web/src/pages/session/overview.tsx`: the evaluation-failed branch renders a retry button even when `dataSource === "sample"`. Guard with `dataSource === "api"` — only show the retry UI for authenticated sessions. For sample mode, show a static message without the button.

## NITs

- `web/src/components/public-header.tsx`: wrap the login/signup links in `<nav aria-label="Public navigation">`
- Replay button in `transcript.tsx` and timeline slider: add `focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2` for keyboard focus visibility
- Import formatting: fix missing spaces after commas in `hero.tsx`, `sample-session.tsx`, `sample.tsx`

## Separate GitHub issues (pre-existing on main)

File issues for these — not fixed on this branch:

1. `useRequireAuth` redirects to `/login` on ALL errors including 500s — should only redirect on `Code.Unauthenticated`
2. `/storage/` path in local dev bypasses SecurityHeaders, CSRF, and OTel
3. Auth hook retry config inconsistency (`useOptionalAuth`: retry false, `useRequireAuth`: retry 1, `useMe`: retry false)
4. `useRequireAuth` redirect loses query string (`location.search` not preserved)
5. Logged-in users on `/about` see Log in / Sign up buttons (PublicHeader not auth-aware)

## Testing

Every fix verified by `make test` (test-protos, test-frontend, test-backend, golangci-lint). The routes.go refactoring in particular must pass `make test-backend` after each change since it touches the HTTP middleware chain. The `isCSRFExempt` extraction must include unit tests as part of this work.
