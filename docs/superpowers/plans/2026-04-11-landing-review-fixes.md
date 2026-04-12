# Landing Page Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all review findings from three deep-dive Opus reviews, refactor routes.go middleware chain, and create OG images.

**Architecture:** Seven independent tasks touching different parts of the codebase. Backend tasks refactor the HTTP middleware chain (CSRF exemption, SecurityHeaders hoisting, cookie rename). Frontend tasks fix performance (replay memoization, transcript memoization), UX (error recovery, empty states, responsive TOC), and code quality (duplicates, type casts, accessibility).

**Tech Stack:** Go, React, TypeScript, Tailwind CSS, SVG/PNG

**Verification commands:** After every task:
- Backend changes: `make test-backend`
- Frontend changes: `make test-frontend`
- Final verification: `make test`

**Working directory:** `/Users/btc/Projects/src/drill` on branch `feature/sabermatic-landing`

---

### Task 1: routes.go refactoring — CSRF exemption, SecurityHeaders, cookie, parameter type

**Files:**
- Modify: `internal/handler/routes.go`
- Create: `internal/handler/csrf_test.go`

- [ ] **Step 1: Extract `isCSRFExempt` function**

In `internal/handler/routes.go`, add this function before `csrfMiddleware`:

```go
// isCSRFExempt returns true for paths that handle their own request
// authentication and don't need CSRF protection.
func isCSRFExempt(path, method string, connectPrefixes []string) bool {
	// Stripe webhook — signature-verified by the handler.
	if path == "/api/webhooks/stripe" && method == http.MethodPost {
		return true
	}
	// River UI — application/json bodies; auth checked by requireAuth middleware.
	if strings.HasPrefix(path, "/admin/jobs/") || path == "/admin/jobs" {
		return true
	}
	// ConnectRPC — custom Content-Type prevents cross-origin form submissions.
	for _, prefix := range connectPrefixes {
		if strings.HasPrefix(path, "/"+prefix+"/") {
			return true
		}
	}
	return false
}
```

Then simplify `csrfMiddleware` to use it. Replace the three `if` blocks (lines 197-217) with:

```go
if isCSRFExempt(r.URL.Path, r.Method, connectPrefixes) {
    next.ServeHTTP(w, r)
    return
}
```

Remove the TODO comment on line 205.

- [ ] **Step 2: Write `isCSRFExempt` tests**

Create `internal/handler/csrf_test.go`:

```go
package handler

import (
	"net/http"
	"testing"
)

func TestIsCSRFExempt(t *testing.T) {
	// Use literal prefixes to avoid importing rpc (heavy dependency chain).
	prefixes := []string{"drill.v1.SampleService", "drill.v1.AuthService"}

	tests := []struct {
		name   string
		path   string
		method string
		want   bool
	}{
		{"stripe webhook POST", "/api/webhooks/stripe", http.MethodPost, true},
		{"stripe webhook GET", "/api/webhooks/stripe", http.MethodGet, false},
		{"riverui root", "/admin/jobs", http.MethodGet, true},
		{"riverui subpath", "/admin/jobs/queues", http.MethodGet, true},
		{"connect service", "/drill.v1.SampleService/GetSampleSession", http.MethodPost, true},
		{"connect auth", "/drill.v1.AuthService/Login", http.MethodPost, true},
		{"health endpoint", "/api/health", http.MethodGet, false},
		{"spa root", "/", http.MethodGet, false},
		{"random POST", "/api/foo", http.MethodPost, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isCSRFExempt(tt.path, tt.method, prefixes)
			if got != tt.want {
				t.Errorf("isCSRFExempt(%q, %q) = %v, want %v", tt.path, tt.method, got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Hoist SecurityHeaders to NewHandler**

In `NewHandler`, change the return from:

```go
return csrfMiddleware(otelHandler, csrfKey, secureCookies), nil
```

To:

```go
return SecurityHeaders(secureCookies, csrfMiddleware(otelHandler, csrfKey, secureCookies)), nil
```

In `csrfMiddleware`, remove the `SecurityHeaders(...)` wrapping. Change:

```go
return SecurityHeaders(secureCookies, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

To:

```go
return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
```

And change the closing from `}))` to `})`.

Keep `secureCookies` in the `csrfMiddleware` signature — it's still used for `csrf.Secure()` and `PlaintextHTTPRequest()`.

- [ ] **Step 4: Rename CSRF cookie**

In `csrfMiddleware`, change:

```go
csrf.CookieName("drill_csrf"),
```

To:

```go
csrf.CookieName("sabermatic_csrf"),
```

- [ ] **Step 5: Change NewHandler parameter type**

Change the signature from:

```go
func NewHandler(b *backend.Backend, spaFS embed.FS) (http.Handler, error)
```

To:

```go
func NewHandler(b *backend.Backend, spaFS fs.FS) (http.Handler, error)
```

Remove `"embed"` from imports if no longer used (check — `embed` may still be needed elsewhere in the file; if not, remove it).

- [ ] **Step 6: Escape all OG tag values and add `</head>` warning**

Add `"log/slog"` to the file-level import block of `routes.go`. In `SPAHandler`, before the OG pre-computation loop, add:

```go
if !strings.Contains(indexHTML, "</head>") {
    slog.Warn("index.html missing </head> — OG tags will not be injected")
}
```

In the OG tag `fmt.Sprintf`, escape all 6 arguments:

```go
tags := fmt.Sprintf(
    `<meta property="og:title" content="%s">`+
        `<meta property="og:description" content="%s">`+
        `<meta property="og:type" content="website">`+
        `<meta property="og:url" content="%s%s">`+
        `<meta property="og:image" content="%s%s">`,
    html.EscapeString(og.title), html.EscapeString(og.description),
    html.EscapeString(baseURL), html.EscapeString(path),
    html.EscapeString(baseURL), html.EscapeString(og.image),
)
```

- [ ] **Step 7: Verify**

Run: `make test-backend`

- [ ] **Step 8: Commit**

```bash
git add internal/handler/routes.go internal/handler/csrf_test.go
git commit -m "refactor: extract isCSRFExempt, hoist SecurityHeaders, rename CSRF cookie, harden OG tags"
```

---

### Task 2: Replay engine memoization and documentation

**Files:**
- Modify: `web/src/components/replay/engine.ts`
- Modify: `web/src/pages/session/transcript.tsx`
- Create: `web/src/components/replay/utils.ts`

- [ ] **Step 1: Memoize replay options at the call site**

In `web/src/pages/session/transcript.tsx`, the `useReplayEngine` call creates an inline options object every render. Wrap in `useMemo`:

Add `useMemo` to the existing React import.

Replace the current `useReplayEngine(...)` call (lines 262-273) with:

```tsx
const replayOptions = useMemo(
  () =>
    replayMode && messages && session?.startTime
      ? {
          messages,
          sessionStartedAt: session.startTime,
          sessionEndedAt: session.endTime,
          annotationSeqs: (evaluation?.annotations ?? []).map(
            (a) => a.messageSeq,
          ),
        }
      : null,
  [replayMode, messages, session?.startTime, session?.endTime, evaluation?.annotations],
);

const replay = useReplayEngine(replayOptions);
```

- [ ] **Step 2: Document the latest-ref pattern in engine.ts**

In `web/src/components/replay/engine.ts`, add a comment above the three dependency-less `useEffect` calls. Before line 80:

```ts
// Latest-ref pattern: runs after every render to keep refs in sync.
// Do not add a dependency array — the tick callback must always
// read current options/timings without stale closures.
```

- [ ] **Step 3: Memoize `annotationsBySeq` in transcript.tsx**

In `web/src/pages/session/transcript.tsx`, the `annotationsBySeq` computation (currently after the early return at line 296) runs every render. **IMPORTANT:** `useMemo` is a hook and cannot be called after an early return (Rules of Hooks). Move it ABOVE the early return, into the hooks section (around line 260, after the other hooks). It must handle the case where annotations are undefined:

```tsx
// Place this in the hooks section, BEFORE the `if (!messages) return` early return:
const annotationsBySeq = useMemo(
  () =>
    (evaluation?.annotations ?? []).reduce<Map<number, Annotation[]>>(
      (acc, ann) => {
        const existing = acc.get(ann.messageSeq) ?? [];
        acc.set(ann.messageSeq, [...existing, ann]);
        return acc;
      },
      new Map(),
    ),
  [evaluation?.annotations],
);
```

After the early return, the existing `const annotations = evaluation?.annotations ?? [];` line stays (it's used by display logic), but remove the old `annotationsBySeq` reduce computation since it's now memoized above.

- [ ] **Step 4: Extract duplicate `formatTime` to utils.ts**

Create `web/src/components/replay/utils.ts`:

```ts
export function formatTime(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}
```

In `web/src/components/replay/controls.tsx`, remove the local `formatTime` function and add:

```ts
import { formatTime } from "./utils";
```

In `web/src/components/replay/timeline.tsx`, remove the local `formatTime` function and add:

```ts
import { formatTime } from "./utils";
```

- [ ] **Step 5: Verify**

Run: `make test-frontend`

- [ ] **Step 6: Commit**

```bash
git add web/src/components/replay/ web/src/pages/session/transcript.tsx
git commit -m "fix: memoize replay options and annotations, extract formatTime, document ref pattern"
```

---

### Task 3: ConditionalHome error recovery

**Files:**
- Modify: `web/src/hooks/use-auth.ts`
- Modify: `web/src/app.tsx`

- [ ] **Step 1: Expose refetch and error from useOptionalAuth**

In `web/src/hooks/use-auth.ts`, update `useOptionalAuth`:

```ts
export function useOptionalAuth() {
  const { data, isLoading, error, refetch } = useQuery(getMe, {}, { retry: false });
  const user = data?.user;
  const isAuthError = error instanceof ConnectError && error.code === Code.Unauthenticated;
  return { user, isLoading, isAuthenticated: !!user, isAuthError, error, refetch };
}
```

- [ ] **Step 2: Add retry button and navigation to ConditionalHome**

In `web/src/app.tsx`, update `ConditionalHome` to destructure `refetch` and `error`:

```tsx
function ConditionalHome() {
  const { isAuthenticated, isLoading, isAuthError, error, refetch } = useOptionalAuth();
  if (isLoading) return <Loading />;
  if (isAuthenticated) {
    return (
      <AppLayout>
        <Home />
      </AppLayout>
    );
  }
  if (isAuthError) {
    return <><PublicHeader /><Landing /></>;
  }
  if (error) {
    console.error("ConditionalHome: unexpected error", error);
  }
  return (
    <div className="flex min-h-screen flex-col items-center justify-center gap-4">
      <p className="text-sm text-muted-foreground">Something went wrong. Please try again later.</p>
      <div className="flex gap-3">
        <Button onClick={() => refetch()} type="button">
          Try again
        </Button>
        <Button variant="outline" asChild>
          <Link to="/about">Go to about page</Link>
        </Button>
      </div>
    </div>
  );
}
```

Add `Link` to the react-router-dom import if not already there. Add `import { Button } from "@/components/ui/button";`.

- [ ] **Step 3: Verify**

Run: `make test-frontend`

- [ ] **Step 4: Commit**

```bash
git add web/src/hooks/use-auth.ts web/src/app.tsx
git commit -m "fix: add retry button and about link to ConditionalHome error state"
```

---

### Task 4: Frontend component fixes (empty card, scores cast, retry guard, TOC)

**Files:**
- Modify: `web/src/pages/landing/sample-session.tsx`
- Modify: `web/src/pages/landing/scoring.tsx`
- Modify: `web/src/pages/session/overview.tsx`
- Modify: `web/src/pages/session/deep-dive.tsx`

- [ ] **Step 1: Fix SampleSessionLink empty card**

In `web/src/pages/landing/sample-session.tsx`, add an early return before the JSX:

After line 11 (`const evaluation = evalData?.evaluation;`), add:

```tsx
if (!session) return null;
```

- [ ] **Step 2: Add scores type cast in scoring.tsx**

In `web/src/pages/landing/scoring.tsx`, add type import:

```tsx
import type { EvaluationScores } from "@/pb/drill/v1/evaluation_pb";
```

Change `scores[dim.key]` accesses (lines 63, 67) to:

```tsx
scores[dim.key as keyof EvaluationScores] as number
```

- [ ] **Step 3: Guard retry button in sample mode**

In `web/src/pages/session/overview.tsx`, the evaluation-failed block (lines 162-177) renders a retry button unconditionally. Wrap it with a `dataSource` check:

```tsx
if (session.status === SessionStatus.EVALUATION_FAILED) {
  return (
    <div className="flex flex-col items-center gap-4 py-16 text-center">
      <p className="text-sm text-muted-foreground">Evaluation could not be completed.</p>
      {dataSource === "api" && (
        <Button
          variant="outline"
          onClick={() => retryMutation.mutate({ sessionId })}
          disabled={retryMutation.isPending}
          type="button"
        >
          {retryMutation.isPending ? "Retrying..." : "Retry evaluation"}
        </Button>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Make TOC responsive on mobile**

In `web/src/pages/session/deep-dive.tsx`, find the `TOC` component's `<nav>` (line 113). Change:

```tsx
<nav className="w-48 shrink-0 sticky top-6 self-start">
```

To:

```tsx
<nav className="hidden lg:block w-48 shrink-0 sticky top-6 self-start">
```

Find the outer flex wrapper (line 280):

```tsx
<div className="flex gap-10 max-w-5xl">
```

Change to:

```tsx
<div className="flex flex-col lg:flex-row lg:gap-10 max-w-5xl">
```

- [ ] **Step 5: Verify**

Run: `make test-frontend`

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/landing/sample-session.tsx web/src/pages/landing/scoring.tsx web/src/pages/session/overview.tsx web/src/pages/session/deep-dive.tsx
git commit -m "fix: empty card guard, scores cast, sample retry guard, responsive TOC"
```

---

### Task 5: Accessibility and formatting NITs

**Files:**
- Modify: `web/src/components/public-header.tsx`
- Modify: `web/src/pages/session/transcript.tsx`
- Modify: `web/src/components/replay/timeline.tsx`
- Modify: `web/src/pages/landing/hero.tsx`
- Modify: `web/src/pages/landing/sample-session.tsx`
- Modify: `web/src/pages/sample.tsx`

- [ ] **Step 1: Add nav wrapper to PublicHeader**

In `web/src/components/public-header.tsx`, wrap the login/signup links in a `<nav>`:

```tsx
<nav aria-label="Public navigation" className="flex items-center gap-4">
  <Link to="/login" ...>Log in</Link>
  <Link to="/signup" ...>Sign up</Link>
</nav>
```

Replace the current `<div className="flex items-center gap-4">` with the `<nav>`.

- [ ] **Step 2: Add focus-visible styles to replay controls**

In `web/src/pages/session/transcript.tsx`, find the replay toggle button (around line 362). Add focus-visible classes:

```tsx
className={cn(
  "rounded-md px-3 py-1.5 text-xs font-medium transition-colors focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
  ...
)}
```

In `web/src/components/replay/timeline.tsx`, find the slider `<div>` with `role="slider"`. Add:

```tsx
className="... focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
```

- [ ] **Step 3: Fix import formatting**

In `web/src/pages/landing/hero.tsx`, `web/src/pages/landing/sample-session.tsx`, and `web/src/pages/sample.tsx`, fix missing spaces after commas in imports. For example:

`import { useSampleEvaluation,useSampleSession }` → `import { useSampleEvaluation, useSampleSession }`

Run ESLint autofix: `cd web && npx eslint --fix src/pages/landing/hero.tsx src/pages/landing/sample-session.tsx src/pages/sample.tsx`

- [ ] **Step 4: Verify**

Run: `make test-frontend`

- [ ] **Step 5: Commit**

```bash
git add web/src/components/public-header.tsx web/src/pages/session/transcript.tsx web/src/components/replay/timeline.tsx web/src/pages/landing/hero.tsx web/src/pages/landing/sample-session.tsx web/src/pages/sample.tsx
git commit -m "fix: PublicHeader nav aria, focus-visible on replay, import formatting"
```

---

### Task 6: OG images

**Files:**
- Create: `web/public/og-landing.svg`
- Create: `web/public/og-sample.svg`
- Create: `web/public/og-landing.png`
- Create: `web/public/og-sample.png`

- [ ] **Step 1: Create og-landing.svg**

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

- [ ] **Step 2: Create og-sample.svg**

Same as above but last text line reads `sample evaluation`.

- [ ] **Step 3: Convert to PNG**

Check which conversion tools are available:

```bash
which rsvg-convert 2>/dev/null && echo "rsvg-convert available" || echo "not available"
```

If `rsvg-convert` is available:
```bash
rsvg-convert -w 1200 -h 630 web/public/og-landing.svg -o web/public/og-landing.png
rsvg-convert -w 1200 -h 630 web/public/og-sample.svg -o web/public/og-sample.png
```

If not, use a small inline Node script with `@resvg/resvg-js` (zero native dependencies):
```bash
cd web && npx -y @resvg/resvg-js-cli ../web/public/og-landing.svg -o ../web/public/og-landing.png --width 1200 --height 630
```

Or use the Playwright MCP `browser_navigate` + `browser_take_screenshot` to open each SVG as a `file://` URL at 1200x630 viewport and screenshot.

- [ ] **Step 4: Verify PNGs exist and are reasonable size**

```bash
ls -la web/public/og-*.png
file web/public/og-*.png
```

Expected: Two PNG files, each a few KB.

- [ ] **Step 5: Commit**

```bash
git add web/public/og-landing.svg web/public/og-sample.svg web/public/og-landing.png web/public/og-sample.png
git commit -m "feat: add OG images for landing and sample pages"
```

---

### Task 7: File GitHub issues for pre-existing main findings

- [ ] **Step 1: File 5 issues**

Use `gh issue create` for each:

1. **useRequireAuth redirects on all errors** — should only redirect on `Code.Unauthenticated`, not 500s or network failures
2. **/storage/ path bypasses SecurityHeaders in local dev** — local storage file server wraps the handler but doesn't inherit SecurityHeaders or CSRF
3. **Auth hook retry config inconsistency** — `useOptionalAuth`: retry false, `useRequireAuth`: retry 1 (from QueryClient default), `useMe`: retry false
4. **useRequireAuth redirect loses query string** — uses `location.pathname` only, should include `location.search`
5. **PublicHeader not auth-aware** — logged-in users on `/about` see Log in / Sign up buttons

---

### Task 8: Final verification

- [ ] **Step 1: Full test suite**

Run: `make test`
Expected: All checks pass.

- [ ] **Step 2: Push**

```bash
git push
```
