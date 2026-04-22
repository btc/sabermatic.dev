# Sabermatic[.DEV] Visual Mark Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship the Sabermatic[.DEV] baseball mark: commit the uncommitted asset files that swap the interim `[.D]` text glyph for the tilted-baseball visual, then wire the PWA manifest, Apple touch-icon, and theme-color tags into `web/index.html`.

**Architecture:** Two coherent commits. Commit 1 is a pure asset drop — the 5 modified assets (favicon + OG images) swap content on surfaces that are already wired; the 6 new mark assets (mark.svg master + 5 PNG variants) are staged for the wiring in commit 2. Commit 2 creates `web/public/manifest.json` and adds three `<head>` tags to `web/index.html` (apple-touch-icon, manifest, theme-color). Vite's `publicDir` copies `web/public/*` to `web/dist/` at build time; Go's `embed.FS` at `web.go` ships the whole `web/dist/` tree. No backend code changes; no new tests (existing `spa_test.go` OG-URL assertions use paths that don't change).

**Tech Stack:** React + Vite SPA at `web/`; Go backend with `embed.FS` serving static assets via `SPAHandler` (`internal/handler/spa.go`); no new dependencies.

---

## File Structure

**Commit 1 — asset drop (11 files, all already in the working tree):**

- `web/public/favicon.svg` — modified; eye-looking text glyph → baseball SVG (simplified form, no stitches)
- `web/public/og-landing.svg`, `og-landing.png` — modified; update branding + mark
- `web/public/og-sample.svg`, `og-sample.png` — modified; update branding + mark
- `web/public/mark.svg` — new; canonical master (detailed, with stitches)
- `web/public/mark-180.png` — new; Apple touch-icon (180×180)
- `web/public/mark-192.png` — new; Android manifest min (192×192)
- `web/public/mark-256.png` — new; high-density manifest (256×256)
- `web/public/mark-512.png` — new; PWA manifest max (512×512)
- `web/public/mark-1024.png` — new; press-kit / source (1024×1024)

**Commit 2 — PWA wiring (2 files):**

- `web/public/manifest.json` — new; PWA manifest (name, icons, theme_color, etc.)
- `web/index.html` — modified; add `apple-touch-icon`, `manifest`, `theme-color` tags in `<head>`

**Untouched:** no backend, no test files, no other frontend files. `internal/handler/spa.go` already serves `web/public/*` paths via its static-file branch; no Go code change.

---

### Task 1: Commit Sabermatic[.DEV] visual mark assets

**Files:**
- Modify (swap content): `web/public/favicon.svg`, `web/public/og-landing.svg`, `web/public/og-landing.png`, `web/public/og-sample.svg`, `web/public/og-sample.png`
- Create (already in working tree, untracked): `web/public/mark.svg`, `web/public/mark-180.png`, `web/public/mark-192.png`, `web/public/mark-256.png`, `web/public/mark-512.png`, `web/public/mark-1024.png`
- Test: none (static assets; content change doesn't break existing tests)

- [ ] **Step 1: Verify working tree state matches the plan**

Run:

```bash
git status --porcelain | grep 'web/public/'
```

Expected output (order may vary):

```
 M web/public/favicon.svg
 M web/public/og-landing.png
 M web/public/og-landing.svg
 M web/public/og-sample.png
 M web/public/og-sample.svg
?? web/public/mark-1024.png
?? web/public/mark-180.png
?? web/public/mark-192.png
?? web/public/mark-256.png
?? web/public/mark-512.png
?? web/public/mark.svg
```

If any file is missing or an unexpected asset is present, stop and reconcile with the spec before proceeding.

- [ ] **Step 2: Spot-check the favicon swap**

Run:

```bash
grep -c '\[.D\]\|\[\.DEV\]\|text-anchor' web/public/favicon.svg
```

Expected output: `0` (the new favicon has no text content; the `[.D]` glyph is gone).

Run:

```bash
grep -c 'circle\|rotate' web/public/favicon.svg
```

Expected output: `2` or higher (the new favicon has a `<circle>` and a `rotate` transform).

- [ ] **Step 3: Spot-check the mark master has stitches**

Run:

```bash
grep -c '<line' web/public/mark.svg
```

Expected output: `14` (the mark master has 14 stitch marks; see spec "Geometry" section).

- [ ] **Step 4: Spot-check the OG SVGs include the new wordmark**

Run:

```bash
grep -c 'Sabermatic' web/public/og-landing.svg web/public/og-sample.svg
```

Expected: each file contains at least one `Sabermatic` occurrence. Total count ≥ 2.

- [ ] **Step 5: Confirm PNG file sizes are not zero**

Run:

```bash
find web/public -name 'mark-*.png' -o -name 'og-*.png' | xargs ls -l
```

All files should show non-zero byte sizes. Rough expectations from spec: `mark-180.png` ~10 KB, `mark-512.png` ~32 KB, `mark-1024.png` ~69 KB, `og-landing.png` ~28 KB, `og-sample.png` ~26 KB. Don't block on exact bytes, just non-zero.

- [ ] **Step 6: Stage the assets explicitly (not `git add -A`)**

Run:

```bash
git add web/public/favicon.svg \
        web/public/og-landing.svg web/public/og-landing.png \
        web/public/og-sample.svg web/public/og-sample.png \
        web/public/mark.svg \
        web/public/mark-180.png web/public/mark-192.png \
        web/public/mark-256.png web/public/mark-512.png \
        web/public/mark-1024.png
```

- [ ] **Step 7: Verify the staged file list**

Run:

```bash
git diff --cached --name-only
```

Expected output (exactly 11 files):

```
web/public/favicon.svg
web/public/mark-1024.png
web/public/mark-180.png
web/public/mark-192.png
web/public/mark-256.png
web/public/mark-512.png
web/public/mark.svg
web/public/og-landing.png
web/public/og-landing.svg
web/public/og-sample.png
web/public/og-sample.svg
```

- [ ] **Step 8: Commit**

Run:

```bash
git commit -m "$(cat <<'EOF'
feat(web): Sabermatic[.DEV] visual mark assets

Swap the interim [.D] text glyph for the tilted-baseball mark across
already-wired surfaces (favicon, OG landing, OG sample) and add the
icon variants that commit 2 will reference. See
docs/superpowers/specs/2026-04-21-visual-mark-design.md for design
rationale, geometry, and colorway.
EOF
)"
```

- [ ] **Step 9: Verify the commit landed**

Run:

```bash
git log --oneline -1
git show --stat HEAD | tail -15
```

Expected: HEAD shows 11 files changed. Commit subject begins `feat(web): Sabermatic[.DEV] visual mark assets`.

- [ ] **Step 10: Run existing tests (sanity baseline)**

Run:

```bash
make test
```

Expected: full CI passes (`buf lint`, codegen check, `tsc -b`, frontend lint + vitest, `go test -race`). This task changed only static assets; nothing should regress. If `make test` fails, the failure is not caused by this task — investigate but do not proceed to Task 2 until baseline is green.

---

### Task 2: Wire PWA manifest, Apple touch-icon, and theme-color

**Files:**
- Create: `web/public/manifest.json`
- Modify: `web/index.html` (`<head>` section, currently lines 4–7)
- Test: none (existing `internal/handler/spa_test.go` OG-tag assertions remain path-based and unchanged; no new test coverage required per spec)

- [ ] **Step 1: Create `web/public/manifest.json` with exact spec contents**

Write the file:

```json
{
  "name": "Sabermatic[.DEV]",
  "short_name": "Sabermatic",
  "description": "data-driven system design prep",
  "start_url": "/",
  "display": "standalone",
  "background_color": "#faf5ef",
  "theme_color": "#faf5ef",
  "icons": [
    { "src": "/mark-192.png", "sizes": "192x192", "type": "image/png", "purpose": "any" },
    { "src": "/mark-256.png", "sizes": "256x256", "type": "image/png", "purpose": "any" },
    { "src": "/mark-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any" }
  ]
}
```

- [ ] **Step 2: Validate manifest.json parses as JSON**

Run:

```bash
python3 -c "import json; json.load(open('web/public/manifest.json'))"
```

Expected: no output, exit code 0. If it prints a `json.decoder.JSONDecodeError`, fix the file.

- [ ] **Step 3: Verify manifest contents match the spec exactly**

Run:

```bash
grep -c '"src": "/mark-' web/public/manifest.json
```

Expected output: `3` (the three icon entries: 192, 256, 512).

Run:

```bash
grep -E '"name"|"short_name"|"theme_color"' web/public/manifest.json
```

Expected output lines:

```
  "name": "Sabermatic[.DEV]",
  "short_name": "Sabermatic",
  "theme_color": "#faf5ef",
```

- [ ] **Step 4: Read current `web/index.html`**

Run:

```bash
cat web/index.html
```

Expected current content (13 lines):

```html
<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Sabermatic[.DEV]</title>
  </head>
  <body>
    <div id="root"></div>
    <script type="module" src="/src/main.tsx"></script>
  </body>
</html>
```

- [ ] **Step 5: Edit `web/index.html` — add three new `<head>` tags**

Replace the `<head>` block. Old:

```html
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title>Sabermatic[.DEV]</title>
  </head>
```

New (link tags grouped together, meta tags grouped together):

```html
  <head>
    <meta charset="UTF-8" />
    <link rel="icon" type="image/svg+xml" href="/favicon.svg" />
    <link rel="apple-touch-icon" sizes="180x180" href="/mark-180.png" />
    <link rel="manifest" href="/manifest.json" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <meta name="theme-color" content="#faf5ef" />
    <title>Sabermatic[.DEV]</title>
  </head>
```

- [ ] **Step 6: Verify the edit**

Run:

```bash
cat web/index.html
```

Expected: the new `<head>` block above. Exactly 16 lines total for the file.

Run:

```bash
grep -c 'apple-touch-icon\|rel="manifest"\|theme-color' web/index.html
```

Expected output: `3`.

- [ ] **Step 7: Run frontend typecheck (sanity)**

Run:

```bash
cd web && npx tsc -b
```

Expected: clean (no errors). This catches accidental malformed HTML that breaks Vite's index parse (rare but possible).

Return to repo root:

```bash
cd ..
```

- [ ] **Step 8: Start the dev server and verify the page loads**

Run (in one terminal):

```bash
make dev
```

Wait for Vite to print `VITE v... ready in ...ms` and for `air` to compile the backend. In a second terminal, run:

```bash
curl -sS -o /dev/null -w "%{http_code}\n" http://localhost:8080/
```

Expected output: `200`.

Run:

```bash
curl -sS http://localhost:8080/manifest.json | head -5
```

Expected: the first 5 lines of the JSON document, starting with `{` and `"name": "Sabermatic[.DEV]"`.

Run:

```bash
curl -sS -I http://localhost:8080/manifest.json | grep -i content-type
```

Expected: `Content-Type: application/json` (per `mime.TypeByExtension(".json")`; see spec "Serving notes"). If you get `application/octet-stream` or another value, investigate before proceeding.

Run:

```bash
curl -sS -I http://localhost:8080/mark-180.png | grep -i content-type
```

Expected: `Content-Type: image/png`.

Stop the dev server (Ctrl-C in the first terminal).

- [ ] **Step 9: Stage both files**

Run:

```bash
git add web/public/manifest.json web/index.html
```

- [ ] **Step 10: Verify staged file list**

Run:

```bash
git diff --cached --name-only
```

Expected output (exactly 2 files):

```
web/index.html
web/public/manifest.json
```

- [ ] **Step 11: Commit**

Run:

```bash
git commit -m "$(cat <<'EOF'
feat(web): wire PWA manifest, apple-touch-icon, theme-color

Complete the visual half of the Sabermatic[.DEV] rebrand. Add
web/public/manifest.json (name, short_name, description, start_url,
display, theme_color, icons 192/256/512) and three web/index.html head
tags (apple-touch-icon at 180x180, manifest link, theme-color meta).
See docs/superpowers/specs/2026-04-21-visual-mark-design.md for
rationale on theme-color=background_color=#faf5ef (parchment), Android
status-bar auto-invert trade-off, and why maskable icons are deferred.
EOF
)"
```

- [ ] **Step 12: Verify the commit landed**

Run:

```bash
git log --oneline -2
git show --stat HEAD | tail -10
```

Expected: HEAD shows 2 files changed, `web/index.html` and `web/public/manifest.json`.

---

### Task 3: Verify end-to-end

**Files:** none changed.

This task validates the implementation against the spec's Verification section before the work is considered done.

- [ ] **Step 1: Run full CI baseline**

Run:

```bash
make test
```

Expected: full pipeline passes — `buf lint`, generated-code drift check, frontend typecheck, ESLint, vitest, Go tests with `-race`. No test file changed, so `spa_test.go` continues to assert on the unchanged OG image paths `/og-landing.png` and `/og-sample.png` and should pass.

If `make test` fails, read the failure carefully — the only legitimate risk is `spa_test.go` drift, which shouldn't happen since no OG URL path changed. Any other failure is unrelated to this implementation.

- [ ] **Step 2: Frontend build sanity**

Run:

```bash
cd web && npm run build
```

Expected: Vite produces `web/dist/` with `index.html`, all `mark-*.png`, `mark.svg`, `favicon.svg`, `og-landing.{png,svg}`, `og-sample.{png,svg}`, and `manifest.json` copied from `web/public/`.

Run:

```bash
ls dist | grep -E 'mark|favicon|og-|manifest'
```

Expected output (12 entries):

```
favicon.svg
manifest.json
mark-1024.png
mark-180.png
mark-192.png
mark-256.png
mark-512.png
mark.svg
og-landing.png
og-landing.svg
og-sample.png
og-sample.svg
```

Return to repo root:

```bash
cd ..
```

- [ ] **Step 3: Manual browser checks — favicon + manifest**

Start the dev server:

```bash
make dev
```

Open http://localhost:8080/ in a browser. Verify:

- **Tab favicon** renders the tilted baseball (cream circle, amber stroke, two arcs, no text). If the tab still shows the `[.D]` text glyph, hard-refresh (Cmd-Shift-R / Ctrl-Shift-R) to bust the browser's favicon cache.
- Open DevTools → **Application** → **Manifest**. Verify:
  - Name: `Sabermatic[.DEV]`
  - Short name: `Sabermatic`
  - Theme color: `#faf5ef`
  - Background color: `#faf5ef`
  - Icons: 3 entries (192, 256, 512), all `image/png`, `purpose: any`, loaded without errors
- Open DevTools → **Console**. Expected: no CSP violation messages related to `/manifest.json`, no 404 for any `/mark-*.png`.

Stop the dev server.

- [ ] **Step 4: Manual browser checks — OG cards**

With the dev server running, navigate to `/` and `/sample`. Open DevTools → **Elements** and inspect the `<head>` meta tags:

```html
<meta property="og:image" content="https://sabermatic.dev/og-landing.png" />
```

(The URL reflects production baseURL; in local dev, the host portion may be `localhost:8080`.)

Open the OG image directly in a new tab: http://localhost:8080/og-landing.png. Verify:

- Parchment `#faf5ef` background
- Baseball mark at top center
- Wordmark "Sabermatic" with `[.DEV]` in reduced opacity below the mark
- Tagline "data-driven system design prep" below the wordmark

Repeat for http://localhost:8080/og-sample.png (tagline differs per spec).

- [ ] **Step 5: Optional — social-platform preview check**

Per spec's Verification step 5, post the production landing URL (once deployed) into Slack, Twitter/X, and iMessage. Verify the unfurl preview shows the new mark + wordmark. Force unfurl refresh if stale:

- Slack: DM the URL to yourself — a fresh DM triggers a fresh unfurl.
- Twitter: use the cards validator at `cards-dev.twitter.com/validator`.

Mark this step complete if the implementation is deploying to production in this branch; otherwise skip and revisit when deploying.

- [ ] **Step 6: Optional — Lighthouse baseline**

With the dev server running, open http://localhost:8080/ in Chrome, DevTools → **Lighthouse**, run an audit. Accessibility and Best Practices scores should not regress from a pre-implementation baseline. The new manifest should also unlock a higher **PWA** section score (installable, manifest present, theme-color present). No hard pass threshold here — this is a baseline check.

- [ ] **Step 7: Stop dev server, confirm final state**

Stop `make dev` (Ctrl-C). Run:

```bash
git status
git log --oneline -3
```

Expected:
- Working tree clean (`nothing to commit, working tree clean`)
- HEAD and HEAD~1 are the two new commits from this plan
- HEAD~2 is the previous commit on main (unrelated)

Implementation complete.

---

## Summary

Two commits, three tasks. Commit 1 is a pure asset drop (11 files). Commit 2 is the PWA wiring (2 files). Task 3 is verification only — no code change.

No automated tests added; the spec's "Verification" section covers manual browser checks. `make test` baseline continues to pass.

If anything surprises the implementing agent — a missing file in step 1 of Task 1, an unexpected `make test` failure that isn't asset-related, a `Content-Type` mismatch on `/manifest.json` — stop and consult the spec at `docs/superpowers/specs/2026-04-21-visual-mark-design.md` or escalate to the user before improvising.
