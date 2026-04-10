# Landing Page Rebase & Rebrand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate the landing page onto main with Sabermatic[.DEV] branding, simplified handler wiring, and `/about` routing with PublicHeader.

**Architecture:** Create `feature/sabermatic-landing` from main. Copy the end-state files from the old landing branch (`feature/growth-landing` at `.worktrees/growth-landing`) into logical commits, editing main's versions of shared files rather than overwriting them. Then apply adaptations (branding, routing, PublicHeader, handler simplification).

**Tech Stack:** Go, ConnectRPC, Protocol Buffers, React, TanStack Query, Tailwind CSS

**Prerequisite:** The Makefile decomposition from `docs/superpowers/plans/2026-04-10-frontend-lint-test-hardening.md` (Task 4) must be done first. It creates `make test-protos`, `make test-frontend`, `make test-backend` targets.

**Verification commands:** After every task that modifies code, run the appropriate target:
- Backend changes: `make test-backend` (go test with -race)
- Frontend changes: `make test-frontend` (tsc -p tsconfig.app.json, eslint, vitest)
- Proto changes: `make test-protos` (buf lint, buf generate, dirty check)
- Final verification: `make test` (runs all of the above + go mod tidy)

---

### Task 1: Create feature branch and commit backend (proto + handler + fixtures)

**Context:** Create a new `feature/sabermatic-landing` branch from main. Copy backend files from the old landing branch tip at `.worktrees/growth-landing/`. Edit main's existing files (like `backend.go`, `register.go`) to add landing features rather than overwriting them.

**Files from old branch to bring over:**
- `pb/drill/v1/sample.proto` (new)
- `internal/sample/embed.go` (modify — add FixtureFS export + provenance comment)
- `internal/sample/fixtures/*.json` (new — 4 fixture files in protojson format)
- `internal/rpc/sample/server.go` (new)
- `internal/rpc/sample/server_test.go` (new)
- `internal/handler/routes_test.go` (new — OG tag tests)

**Files on main to edit:**
- `internal/backend/backend.go` — add `SampleService` field + construction
- `internal/rpc/register.go` — add SampleService registration
- `internal/handler/routes.go` — add OG tag injection to `SPAHandler`, add `baseURL` parameter

- [ ] **Step 1: Create branch**

```bash
git checkout -b feature/sabermatic-landing main
```

- [ ] **Step 2: Copy new files from old branch**

```bash
cp .worktrees/growth-landing/pb/drill/v1/sample.proto pb/drill/v1/sample.proto
mkdir -p internal/rpc/sample
cp .worktrees/growth-landing/internal/rpc/sample/server.go internal/rpc/sample/server.go
cp .worktrees/growth-landing/internal/rpc/sample/server_test.go internal/rpc/sample/server_test.go
cp .worktrees/growth-landing/internal/sample/fixtures/*.json internal/sample/fixtures/
```

- [ ] **Step 3: Update `internal/sample/embed.go`**

Read the current file. Add the `FixtureFS()` export and provenance comment from the old branch. The file should become:

```go
package sample

import "embed"

// Fixture data extracted from v0 prototype, session 27 ("Chat System").
// Text-only — no audio files (all audio_url fields are null).
// To regenerate: run a session in v0, export via extract-sample script.
//
//go:embed fixtures/*.json
var fixtureFS embed.FS

// FixtureFS returns the embedded fixture filesystem.
func FixtureFS() embed.FS {
	return fixtureFS
}
```

- [ ] **Step 4: Add SampleService to Backend**

Read `internal/backend/backend.go`. Add to the imports:
```go
samplesvc "github.com/btc/drill/internal/rpc/sample"
```

Add an exported field to the `Backend` struct:
```go
SampleService *samplesvc.SampleService
```

In `New()`, after the storage/gemini/pool setup but before the River client, add:
```go
ss, err := samplesvc.NewSampleService()
if err != nil {
    return nil, fmt.Errorf("sample service: %w", err)
}
```

Add `SampleService: ss,` to the return struct literal.

- [ ] **Step 5: Add SampleService to `register.go`**

Read `internal/rpc/register.go`. Add import:
```go
samplerpc "github.com/btc/drill/internal/rpc/sample"
```

Add to `ConnectPathPrefixes`:
```go
drillv1connect.SampleServiceName,
```

Add registration with `publicOpts` (after the AuthService line):
```go
mux.Handle(drillv1connect.NewSampleServiceHandler(samplerpc.NewServer(b.SampleService), publicOpts))
```

- [ ] **Step 6: Add OG tag injection to `routes.go`**

Read `internal/handler/routes.go`. Read the old branch's version at `.worktrees/growth-landing/internal/handler/routes.go` for reference. The key additions to main's `routes.go`:

Add `"strconv"` and `"github.com/btc/drill/internal/branding"` to imports.

Add the `ogRoute` struct and `ogRoutes` map (after `RegisterRoutes`, before `SPAHandler`):
```go
type ogRoute struct {
	title       string
	description string
	image       string
}

var ogRoutes = map[string]ogRoute{
	"/": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/about": {
		title:       branding.AppName,
		description: "data-driven system design prep",
		image:       "/og-landing.png",
	},
	"/sample": {
		title:       branding.AppName + " — sample evaluation",
		description: "See a real system design interview evaluated across 5 dimensions",
		image:       "/og-sample.png",
	},
}
```

Change `SPAHandler` signature from `(fsys embed.FS)` to `(fsys fs.FS, baseURL string)`. Add OG tag pre-computation and injection logic from the old branch. The function reads `index.html` at init, pre-computes OG-injected HTML for each route in `ogRoutes`, and serves the pre-computed version for matching paths.

In `NewHandler`, update the `SPAHandler` call to pass `baseURL`. For now, hardcode `""` — Task 4 will wire `b.Config().Auth.BaseURL`.

- [ ] **Step 7: Copy routes_test.go**

```bash
cp .worktrees/growth-landing/internal/handler/routes_test.go internal/handler/routes_test.go
```

Review it — update any "sabermetric" references to "sabermatic" and use `branding.AppName`. Add test case for `/about`. (This can also be deferred to Task 5 if the file copies cleanly.)

- [ ] **Step 8: Run `buf generate`**

```bash
buf generate
```

- [ ] **Step 9: Verify**

Run: `make test-backend`
Expected: All tests pass including SampleService tests and OG tag tests.

- [ ] **Step 10: Commit**

```bash
git add pb/drill/v1/sample.proto internal/pb/ web/src/pb/ internal/sample/ internal/rpc/sample/ internal/backend/backend.go internal/rpc/register.go internal/handler/routes.go internal/handler/routes_test.go
git commit -m "feat: add SampleService proto, handler, fixtures, and OG tag injection"
```

---

### Task 2: Frontend infrastructure (queries, hooks, types)

**Files from old branch to bring over (copy directly — these are new or fully rewritten):**
- `web/src/api/queries.ts`
- `web/src/api/sample-queries.ts`
- `web/src/api/types.ts`
- `web/src/hooks/use-auth.ts`
- `web/src/components/replay/engine.ts`
- `web/src/components/replay/controls.tsx`
- `web/src/components/replay/timeline.tsx`

- [ ] **Step 1: Copy files**

```bash
cp .worktrees/growth-landing/web/src/api/queries.ts web/src/api/queries.ts
cp .worktrees/growth-landing/web/src/api/sample-queries.ts web/src/api/sample-queries.ts
cp .worktrees/growth-landing/web/src/api/types.ts web/src/api/types.ts
cp .worktrees/growth-landing/web/src/hooks/use-auth.ts web/src/hooks/use-auth.ts
mkdir -p web/src/components/replay
cp .worktrees/growth-landing/web/src/components/replay/engine.ts web/src/components/replay/engine.ts
cp .worktrees/growth-landing/web/src/components/replay/controls.tsx web/src/components/replay/controls.tsx
cp .worktrees/growth-landing/web/src/components/replay/timeline.tsx web/src/components/replay/timeline.tsx
```

- [ ] **Step 2: Verify frontend compiles**

Run: `make test-frontend`
Expected: All checks pass. If there are import errors (e.g., old branch references types that don't exist on main's generated proto), fix them.

- [ ] **Step 3: Commit**

```bash
git add web/src/api/ web/src/hooks/use-auth.ts web/src/components/replay/
git commit -m "feat: add ConnectRPC query hooks, sample queries, replay engine"
```

---

### Task 3: Landing page, sample page, and session detail pages

**Files from old branch — new files (copy directly):**
- `web/src/pages/landing/` (entire directory — index.tsx, hero.tsx, scoring.tsx, annotations.tsx, deep-dive.tsx, strengths-gaps.tsx, coaching.tsx, voice-pipeline.tsx, sample-session.tsx, credits.tsx, cta-repeat.tsx)
- `web/src/pages/sample.tsx`
- `web/src/hooks/use-scroll-reveal.ts`

**Files from old branch — modified versions of files that exist on main:**
- `web/src/pages/session/layout.tsx` (adds dataSource context)
- `web/src/pages/session/overview.tsx` (proto types + dataSource)
- `web/src/pages/session/transcript.tsx` (proto types + dataSource + replay)
- `web/src/pages/session/deep-dive.tsx` (proto types + dataSource)
- `web/src/app.tsx` (add ConditionalHome, landing imports, sample routes)

- [ ] **Step 1: Copy new files**

```bash
mkdir -p web/src/pages/landing
cp .worktrees/growth-landing/web/src/pages/landing/*.tsx web/src/pages/landing/
cp .worktrees/growth-landing/web/src/pages/sample.tsx web/src/pages/sample.tsx
cp .worktrees/growth-landing/web/src/hooks/use-scroll-reveal.ts web/src/hooks/use-scroll-reveal.ts
```

- [ ] **Step 2: Copy session detail pages**

These are modified versions — the old branch has proto types and dataSource support. Copy them:

```bash
cp .worktrees/growth-landing/web/src/pages/session/layout.tsx web/src/pages/session/layout.tsx
cp .worktrees/growth-landing/web/src/pages/session/overview.tsx web/src/pages/session/overview.tsx
cp .worktrees/growth-landing/web/src/pages/session/transcript.tsx web/src/pages/session/transcript.tsx
cp .worktrees/growth-landing/web/src/pages/session/deep-dive.tsx web/src/pages/session/deep-dive.tsx
```

- [ ] **Step 3: Update `app.tsx`**

Read main's current `app.tsx`. Add the landing branch features to it — do NOT overwrite:

Add imports:
```tsx
import { useOptionalAuth } from "@/hooks/use-auth";

const Landing = lazy(() => import("@/pages/landing"));
const SampleSession = lazy(() => import("@/pages/sample"));
```

Add `ConditionalHome` component (before `App`). This uses `useOptionalAuth` to distinguish 401 (show landing) from 5xx (show error):
```tsx
function Loading() {
  return <div className="flex h-screen items-center justify-center text-muted-foreground">Loading...</div>;
}

function ConditionalHome() {
  const { isAuthenticated, isLoading, isAuthError } = useOptionalAuth();
  if (isLoading) return <Loading />;
  if (isAuthenticated) {
    return (
      <AppLayout>
        <Home />
      </AppLayout>
    );
  }
  if (isAuthError) {
    return <Landing />;
  }
  return (
    <div className="flex min-h-screen items-center justify-center">
      <p className="text-sm text-muted-foreground">Something went wrong. Please try again later.</p>
    </div>
  );
}
```

Update routes — move `"/"` out of `AppLayout`, add sample routes:
```tsx
<Routes>
  {/* Public — no layout */}
  <Route path="/login" element={<Login />} />
  <Route path="/signup" element={<Signup />} />
  <Route path="/forgot-password" element={<ForgotPassword />} />
  <Route path="/reset-password" element={<ResetPassword />} />
  <Route path="/verify-email" element={<VerifyEmail />} />
  <Route path="/sample" element={<SampleSession />}>
    <Route index element={<Overview />} />
    <Route path="transcript" element={<TranscriptPage />} />
    <Route path="deep-dive" element={<DeepDive />} />
  </Route>

  {/* Root — conditional: landing (unauth) or app layout (auth) */}
  <Route path="/" element={<ConditionalHome />} />

  {/* App — top bar layout (all require auth) */}
  <Route element={<AppLayout />}>
    <Route path="/sessions/new" element={<SessionConfig />} />
    <Route path="/sessions/:id" element={<SessionLayout />}>
      <Route index element={<Navigate to="overview" replace />} />
      <Route path="overview" element={<Overview />} />
      <Route path="transcript" element={<TranscriptPage />} />
      <Route path="deep-dive" element={<DeepDive />} />
    </Route>
    <Route path="/history" element={<History />} />
    <Route path="/settings" element={<Settings />} />
    <Route path="/settings/billing" element={<Settings />} />
  </Route>

  {/* Interview — immersive layout */}
  <Route element={<ImmersiveLayout />}>
    <Route path="/sessions/:id/interview" element={<Interview />} />
  </Route>

  {/* Catch-all — 404 */}
  <Route path="*" element={<NotFound />} />
</Routes>
```

- [ ] **Step 4: Verify**

Run: `make test-frontend`

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/landing/ web/src/pages/sample.tsx web/src/hooks/use-scroll-reveal.ts web/src/pages/session/ web/src/app.tsx
git commit -m "feat: add landing page, sample session viewer, session detail dataSource"
```

---

### Task 4: Handler signature simplification

**Files:**
- Modify: `internal/handler/routes.go`
- Modify: `cmd/drill/main.go`
- Modify: test files that call `NewHandler`

- [ ] **Step 1: Simplify `NewHandler` signature**

Read `internal/handler/routes.go`. Change from:
```go
func NewHandler(b *backend.Backend, spaFS embed.FS, csrfKey []byte, secureCookies bool) (http.Handler, error)
```

To:
```go
func NewHandler(b *backend.Backend, spaFS embed.FS) (http.Handler, error)
```

Inside `NewHandler`, add at the top:
```go
cfg := b.Config()
baseURL := cfg.Auth.BaseURL
csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
secureCookies := cfg.Auth.SecureCookies()
```

Pass `baseURL` to `SPAHandler(spaFS, baseURL)`. Pass `csrfKey` and `secureCookies` to `csrfMiddleware`.

Add import `"github.com/btc/drill/internal/auth"` if not present.

- [ ] **Step 2: Update `cmd/drill/main.go`**

Change from:
```go
csrfKey := auth.DeriveKey(cfg.Auth.TokenSecret, "csrf")
h, err := handler.NewHandler(b, drill.WebFS, csrfKey, cfg.Auth.SecureCookies())
```

To:
```go
h, err := handler.NewHandler(b, drill.WebFS)
```

Keep the `auth` import (still used for oauth state key).

- [ ] **Step 3: Update test callers**

```bash
grep -rn 'NewHandler' internal/handler/ internal/testutil/
```

Update each call to the 2-arg signature `NewHandler(b, spaFS)`.

- [ ] **Step 4: Verify**

Run: `make test-backend`

- [ ] **Step 5: Commit**

```bash
git add internal/handler/routes.go cmd/drill/main.go internal/handler/*_test.go internal/testutil/
git commit -m "refactor: simplify NewHandler to derive config from Backend"
```

---

### Task 5: Branding and PublicHeader

**Files:**
- Create: `web/src/components/public-header.tsx`
- Modify: `web/src/pages/landing/hero.tsx`
- Modify: `web/src/pages/sample.tsx`
- Modify: `web/src/app.tsx`
- Modify: `internal/handler/routes.go` (OG tag content — if not already done in Task 1)
- Modify: `internal/handler/routes_test.go` (assertions — if not already done in Task 1)

- [ ] **Step 1: Create `PublicHeader` component**

Create `web/src/components/public-header.tsx`:
```tsx
import { Link } from "react-router-dom";
import { BrandName } from "@/components/brand-name";

export function PublicHeader() {
  return (
    <header className="border-b border-border">
      <div className="mx-auto flex h-12 max-w-5xl items-center justify-between px-4">
        <Link to="/" className="text-sm font-semibold tracking-wider text-muted-foreground">
          <BrandName />
        </Link>
        <div className="flex items-center gap-4">
          <Link
            to="/login"
            className="text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            Log in
          </Link>
          <Link
            to="/signup"
            className="rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground"
          >
            Sign up
          </Link>
        </div>
      </div>
    </header>
  );
}
```

- [ ] **Step 2: Update hero.tsx branding**

Read `web/src/pages/landing/hero.tsx`. Replace hardcoded "sabermetric" with:
```tsx
import { BrandName } from "@/components/brand-name";
```

In the hero `<h1>`:
```tsx
<BrandName className="text-5xl font-light tracking-tight sm:text-7xl" />
```

- [ ] **Step 3: Update sample.tsx**

Replace the inline header with `<PublicHeader />`. Add bottom CTA:
```tsx
import { PublicHeader } from "@/components/public-header";
```

Replace `<header>...</header>` with `<PublicHeader />`.

Add after the `<Outlet />` div:
```tsx
<div className="border-t border-border mt-12 py-12 text-center">
  <p className="text-lg text-foreground mb-4">Want feedback on your own design?</p>
  <Link
    to="/signup"
    className="rounded-md bg-primary px-6 py-2.5 text-sm font-medium text-primary-foreground"
  >
    Sign up
  </Link>
</div>
```

- [ ] **Step 4: Add `/about` route to app.tsx**

The `Landing` import should already exist from Task 3. Add the route (public, outside `AppLayout`):
```tsx
<Route path="/about" element={<><PublicHeader /><Landing /></>} />
```

Import `PublicHeader` in app.tsx:
```tsx
import { PublicHeader } from "@/components/public-header";
```

- [ ] **Step 5: Update OG tags branding**

If OG tags still reference "Sabermetric" or "sabermetric.dev" (check `internal/handler/routes.go`), update to use `branding.AppName` and `sabermatic.dev`. Similarly update assertions in `routes_test.go`. Add test case for `/about` if not already present.

- [ ] **Step 6: Verify no stale branding**

```bash
grep -rn 'sabermetric\|Sabermetric' web/src/ internal/ --include='*.tsx' --include='*.ts' --include='*.go' | grep -v node_modules | grep -v '.pb.'
```
Expected: No results.

- [ ] **Step 7: Verify**

Run: `make test-backend && make test-frontend`

- [ ] **Step 8: Commit**

```bash
git add web/src/components/public-header.tsx web/src/pages/landing/hero.tsx web/src/pages/sample.tsx web/src/app.tsx internal/handler/routes.go internal/handler/routes_test.go
git commit -m "feat: Sabermatic branding, PublicHeader, /about route, sample CTA"
```

---

### Task 6: Final verification

- [ ] **Step 1: Full test suite**

Run: `make test`
Expected: All checks pass (except pre-existing interview.proto buf lint).

- [ ] **Step 2: Verify no bare REST references**

```bash
grep -rn 'apiClient\.\(get\|post\|patch\|delete\)' web/src/api/queries.ts
```
Expected: Only `useCreateQuestion` POST.

```bash
grep -rn '/api/sample/' web/src/
```
Expected: No results.

- [ ] **Step 3: Push and create PR**

```bash
git push -u origin feature/sabermatic-landing
```

Create PR targeting main.
