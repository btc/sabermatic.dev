# Landing Page Rebase & Rebrand

Rebase the `feature/growth-landing` branch onto current main (main is 80 commits ahead of the branch), adapt to the Sabermatic[.DEV] rebrand, simplify handler wiring, and finalize routing.

## Routing

- `/` — `ConditionalHome`: landing page if unauthenticated, authenticated dashboard if logged in. Unchanged from current branch behavior.
- `/about` — landing page always, no auth check. New route, same landing page component as `/`. For direct linking and SEO.
- `/sample` — sample session viewer with overview/transcript/deep-dive tabs. Public, no auth.
- `/login`, `/signup` — auth pages (unchanged).

`useOptionalAuth` remains for `ConditionalHome` — it distinguishes 401 (show landing page) from 5xx (show error message). This error-handling nuance must survive the rebase when `ConditionalHome` is added to main's routing structure. `useRequireAuth` redirect target stays `/login` (not `/about`).

## Public header nav

New `PublicHeader` component (e.g., `web/src/components/public-header.tsx`) used on both `/about` and `/sample`:

```
[Sabermatic[.DEV]]                    Log in  [Sign up]
```

- Brand logo (`<BrandName />`) links to `/`
- "Log in" — text link to `/login`
- "Sign up" — primary button to `/signup`

The `<BrandName />` component uses `whitespace-nowrap` and renders `[.DEV]` at `opacity-60`. In the hero `<h1>`, pass a `className` prop to override font size/weight to match the hero styling (`text-5xl font-light tracking-tight`).

The current `sample.tsx` has an inline header with hardcoded "sabermetric". Replace with `PublicHeader`.

The landing page hero and all landing sections sit below `PublicHeader` on `/about`.

### Sample page bottom CTA

Add a contextual CTA at the bottom of `/sample` session content (after the tab content area):

> "Want feedback on your own design?" → Sign up button

Catches visitors who scrolled through the sample evaluation at peak interest.

## Branding

Replace all "sabermetric" / "Sabermetric" references with Sabermatic[.DEV] branding:

- `web/src/pages/landing/hero.tsx` — hardcoded "sabermetric" → `<BrandName className="..." />` with hero-appropriate sizing
- `web/src/pages/sample.tsx` — inline header → `PublicHeader` (covers branding)
- `internal/handler/routes.go` OG tags — "Sabermetric" → use `branding.AppName` for titles
- `internal/handler/routes.go` OG URLs — `sabermetric.dev` → `sabermatic.dev`
- `internal/handler/routes_test.go` — new file from landing branch; update assertions to use "Sabermatic" and "sabermatic.dev"

Use `<BrandName />` from `web/src/components/brand-name.tsx` (already on main) wherever the brand name appears in UI. Use `branding.AppName` from `internal/branding/branding.go` for backend strings (OG tags).

## OG tags

The `ogRoutes` map in `routes.go` needs entries for all public routes:

- `/` — "Sabermatic[.DEV]", "data-driven system design prep"
- `/about` — same OG tags as `/` (same content)
- `/sample` — "Sabermatic[.DEV] — sample evaluation", "See a real system design interview evaluated across 5 dimensions"

Add `/about` entry to the existing `ogRoutes` map. Test in `routes_test.go`.

## Handler signature simplification

`NewHandler` simplifies to:

```go
func NewHandler(b *backend.Backend, spaFS embed.FS) (http.Handler, error)
```

Derive everything from `b.Config()` inside:
- `baseURL` from `b.Config().Auth.BaseURL`
- `csrfKey` from `auth.DeriveKey(b.Config().Auth.TokenSecret, "csrf")`
- `secureCookies` from `b.Config().Auth.SecureCookies()`

`RegisterRoutes` already has signature `(mux *http.ServeMux, b *backend.Backend) error` on both branches — no change needed. SampleService accessed via `b.SampleService` (introduced in squash commit #1).

`cmd/drill/main.go` call becomes `handler.NewHandler(b, drill.WebFS)`. The local-storage file server block from main (serving stored files via HTTP in dev mode) must be preserved in main.go — it exists on main but not on the landing branch.

## Squash-rebase strategy

Squash the 44 landing branch commits into 4 logical commits before rebasing onto main. Pages created in commits #2-3 should already use proto types from the start (consolidate the iterative REST→proto migration that happened on the branch).

1. **Proto + backend** — SampleService proto, fixtures (protojson format), RPC handler, `SampleService` field on `Backend`, embed.go with provenance, tests, `routes_test.go` for OG tags
2. **Frontend infrastructure** — queries.ts (ConnectRPC), sample-queries.ts (ConnectRPC), use-auth.ts (ConnectRPC), types.ts (stripped to Question only), replay engine (proto Timestamp)
3. **Landing page + sample page** — all 10 landing sections using proto types directly, sample page, session detail dataSource context, app routing with ConditionalHome, `useOptionalAuth` hook
4. **Session detail pages** — overview/transcript/deep-dive updated to proto types and ConnectRPC response unwrapping

### Per-file conflict resolution

Expected conflicts during rebase and how to resolve each:

**`internal/backend/backend.go`** — Most complex conflict. Main added `riverUI`, `closeRiverUI`, `RiverUIHandler()`, changed `storage.Store` types and constructor signatures (e.g., `NewLocal` takes `cfg` not `cfg.Storage.LocalDir`), added `PublicBucket`. Landing branch added `SampleService` field and construction. Resolution: take main's struct/constructor as base, add `SampleService` field and `NewSampleService()` call.

**`cmd/drill/main.go`** — Main has 4-arg `NewHandler(b, drill.WebFS, csrfKey, cfg.Auth.SecureCookies())` plus local-storage serving block. Landing has 3-arg `NewHandler(b, drill.WebFS, cfg)`. Resolution: use 2-arg `NewHandler(b, drill.WebFS)`, keep main's local-storage serving block, remove csrfKey derivation from main.go (moves into NewHandler).

**`web/src/app.tsx`** — Main has all routes behind `AppLayout` with no `ConditionalHome`, no landing page, no `/sample` route. Landing has `ConditionalHome`, landing page import, sample routes. Resolution: take main's structure as base, add `ConditionalHome`, landing page imports, `/about` route, `/sample` routes. Keep main's `NotFound` route. Keep main's lazy imports for all authenticated pages.

**`web/src/pages/home.tsx`** — Main completely rewrote this (image grid, bar chart, ghost tiles, coach card). Landing has old version. Resolution: keep main's version entirely.

**`web/src/layouts/app-layout.tsx`** — Main redesigned nav (BrandName logo, Sessions link, avatar dropdown). Resolution: keep main's version entirely.

**Proto files** — Main added `summary` to `coach.proto`, `image_url` to `question.proto`, `question_image_url` fields. Resolution: take main's protos, keep landing's `sample.proto`. Run `buf generate` after rebase.

**`internal/handler/routes.go`** — Main has `NewHandler(b, spaFS, csrfKey, secureCookies)` 4-arg signature without OG tags. Landing has OG tags, `b.Config()` approach. Resolution: take landing's OG tag injection and `b.Config()` approach, keep main's CSRF middleware structure and admin route registration (main uses `mux.Handle` with riverUI handler at `/admin/jobs/`, not the landing branch's placeholder `mux.HandleFunc("GET /admin/jobs", ...)`). Note: `SPAHandler` signature differs — main has `SPAHandler(fsys embed.FS)` (1 arg, no OG), landing has `SPAHandler(fsys fs.FS, baseURL string)` (2 args, OG injection). Take landing's `SPAHandler` implementation; wire `baseURL` from `b.Config().Auth.BaseURL` inside `NewHandler`. The `fs.FS` parameter type is satisfied by `embed.FS` so the type change compiles.

### Post-rebase adaptation commit

After the 4 squashed commits are rebased:
- Branding updates (hero → BrandName, OG tags → sabermatic.dev)
- `/about` route addition in `app.tsx`
- `PublicHeader` component creation
- `/sample` page uses `PublicHeader`, adds bottom CTA
- OG tags entry for `/about`
- Handler signature simplification to 2 args (`b.Config()` approach)
- Coach `summary` field in sample fixture (for parity with main's coach card)

## What doesn't change

- Landing page section content, styling, animations, scroll-reveal
- Sample session fixture data (v0 session 27)
- SampleService proto and backend handler
- ConnectRPC hook architecture
- Replay engine functionality

## Testing

- `go build ./...` — clean
- `go test ./...` — all pass including SampleService tests
- `cd web && npx tsc -b` — clean
- `make test` — passes (except pre-existing interview.proto lint)
- Manual: load `/about`, verify all 10 sections render with correct branding
- Manual: load `/sample`, verify header nav, session tabs, bottom CTA
- Manual: load `/` unauthenticated, verify landing page renders
- Manual: login, verify `/` shows dashboard
- Automated: `routes_test.go` covers OG tags for `/`, `/about`, and `/sample`
