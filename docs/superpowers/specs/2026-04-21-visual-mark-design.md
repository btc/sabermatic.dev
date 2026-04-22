# Sabermatic[.DEV] Visual Mark

_2026-04-21_

Codify the Sabermatic[.DEV] visual identity: a tilted, stitched baseball rendered in the amber colorway of the landing page. Ship the mark across all platform surfaces that currently render the interim `[.D]` text glyph committed in `24971fc`.

## The mark

The mark is a baseball, tilted `-28°`, with two amber seam arcs meeting at an asymmetric junction slightly below and left of center. Three variants exist, each tuned to its rendering context:

- **`mark.svg`** — high-fidelity master. 14 seam stitches, fine stroke widths, used for large rasterizations (PNG variants, press-kit, OG card scale).
- **`favicon.svg`** — simplified small-size form. No stitches (sub-pixel at 16–32px), chunkier strokes. Used for `<link rel="icon">`.
- **`og-landing.svg` / `og-sample.svg`** — OG card composition. Embeds the favicon's simplified seam geometry plus a distinct 8-stitch accent pattern, tuned for legibility through aggressive compression by social-platform unfurlers (Slack, Twitter/X, iMessage) that downsample previews to 300–600 px.

### Symbolism

Sabermatic derives from sabermetrics — Bill James's term for empirical baseball analytics (the Society for American Baseball Research, SABR). The mark makes the etymology visual without naming it. Tilt introduces motion and prevents the mark from reading as a clock, coin, or generic circular badge. The asymmetric seam junction (seam control point offset 2 units left, 1 unit down from circle center on a 32-unit viewBox) makes the mark feel hand-crafted — a deliberate counterpoint to the geometric precision of a typographic mark.

The amber colorway is Tailwind's amber scale on a parchment background, evoking vintage leather baseball, old-book binding, and the "analytical rigor on the surface, monk/devotion/craft underneath" register from `2026-04-03-naming-and-branding-design.md`. Intentionally not SaaS-corporate blue/teal.

### Colorway

| Role | Hex | Tailwind |
|---|---|---|
| Ball interior | `#fef3c7` | amber-100 |
| Seam arcs | `#b45309` | amber-700 |
| Ball outline + stitches | `#92400e` | amber-800 |
| Parchment (backgrounds) | `#faf5ef` | custom, not in amber scale |

### Geometry

All three variants share the same `viewBox="0 0 32 32"` coordinate system:

- Outer group rotated `-28°` about center `(16, 16)`
- Ball: `<circle cx="16" cy="16" r="12">`
- Seams: two quadratic Bézier paths with control point `(14, 17)` — the asymmetric junction

Stroke weights and stitch detail vary by rendering context. In `mark.svg`: seam stroke `1.0`, ball outline `1.3`, stitch stroke `0.38`, 14 short segments (7 per side) approximately perpendicular to the seam tangents. In `favicon.svg`: seam stroke `2`, ball outline `2.5`, no stitches — at 16–32 px display sizes, fine stitches render as sub-pixel noise. In the OG variants: favicon seams are re-used (stroke `2`, round linecap) plus an 8-stitch accent pattern (stroke `0.55`, opacity `0.85`) that reads cleanly at typical OG-card downsample sizes.

## File inventory

`mark.svg` is the canonical master for PNG rasterization. `favicon.svg` is its simplified small-size counterpart. The OG SVGs are composed assets (mark + wordmark + tagline) with their own stitch geometry as described above.

| File | Role | Notes |
|---|---|---|
| `web/public/mark.svg` | Canonical master | 32-unit viewBox rendered at 512×512. Stroke widths 1.3 / 1.0 / 0.38. Includes 14 stitch marks. |
| `web/public/favicon.svg` | Simplified small-size form | Stroke widths 2.5 / 2. Stitches omitted. Referenced by `<link rel="icon">`. |
| `web/public/mark-180.png` | iOS home-screen icon | 180×180, rasterized from `mark.svg` with transparent background. Apple touch-icon spec. |
| `web/public/mark-192.png` | Android manifest icon | 192×192, rasterized from `mark.svg`. PWA manifest minimum. |
| `web/public/mark-256.png` | High-density manifest | 256×256, rasterized from `mark.svg`. |
| `web/public/mark-512.png` | PWA manifest max | 512×512, rasterized from `mark.svg`. |
| `web/public/mark-1024.png` | Source / press-kit | 1024×1024, rasterized from `mark.svg`. |
| `web/public/og-landing.{svg,png}` | Open Graph card — landing | Mark at top center (scale 5.625 = 180 px on 1200×630), wordmark + tagline below. Seams use favicon geometry; 8-stitch accent pattern for unfurler legibility. |
| `web/public/og-sample.{svg,png}` | Open Graph card — sample | Same composition, different tagline. |

### Regenerating PNGs

Mark PNGs are rasterized from `mark.svg`. Use any SVG-to-PNG tool that respects stroke width at non-integer values (e.g., `rsvg-convert`, `resvg`, or a headless Chromium screenshot). OG PNGs are rasterized from their respective SVGs. All PNGs are committed as static assets; regeneration is manual and infrequent.

### Provenance

Mark V1 authored 2026-04-12 as the visual half of the Sabermatic[.DEV] rebrand. The text half landed in:

- `bbded9b` — apply Sabermatic[.DEV] branding across frontend
- `f2f410f` — branding constants and BrandName component
- `c6fe3b0` — styled email templates
- `a8be7af` — PublicHeader, /about route
- `4c0b8c4` — merge feature/sabermatic-landing

The prior `[.D]` text glyph committed in `24971fc` was an interim stand-in. This spec is the retroactive written record of the V1 visual design work.

## Wiring

The mark currently lives uncommitted in the working tree. This spec commits it and completes the platform surface wiring that the textual rebrand didn't cover.

### Content-swap surfaces (already wired)

| Surface | Files swapped | Code unchanged |
|---|---|---|
| Browser favicon | `web/public/favicon.svg` | `web/index.html:5` `<link rel="icon">` already references `/favicon.svg`. |
| OG cards | `web/public/og-landing.{svg,png}`, `web/public/og-sample.{svg,png}` | `internal/handler/spa.go` OG URL map still points at `/og-landing.png` and `/og-sample.png`. |

### New wiring

Add to `web/index.html` `<head>`:

```html
<link rel="apple-touch-icon" href="/mark-180.png" />
<link rel="manifest" href="/manifest.json" />
<meta name="theme-color" content="#faf5ef" />
```

Create `web/public/manifest.json`:

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
    { "src": "/mark-512.png", "sizes": "512x512", "type": "image/png", "purpose": "any" }
  ]
}
```

Rationale for `theme-color` = `background_color` = `#faf5ef` (parchment): matches the landing page background, so the iOS Safari status bar and Android Chrome address bar blend into the page chrome. Keeps the amber bold-color energy reserved for the mark itself. Consistent with the "quiet confidence" register.

### Serving notes

- **Manifest MIME type.** The `.json` extension is used rather than `.webmanifest`. Go's `net/http` default MIME handler serves `application/json`, which all current user agents accept for `<link rel="manifest">`. The strict form (`application/manifest+json`) would require explicit `mime.AddExtensionType` registration; out of scope here since no current validator fails on the looser form.
- **CSP.** `internal/handler/middleware.go` sets `default-src 'self'` with no explicit `manifest-src`. Per CSP Level 3, `manifest-src` falls back to `default-src`, which permits same-origin `/manifest.json`. No CSP change needed.
- **Vite embed.** `web.go` embeds `web/dist`; Vite copies `web/public/*` to `dist/` at build time. No build-config change needed — the new files ship via the existing embed.
- **Apple touch-icon transparency.** `mark-180.png` is rasterized with a transparent background. iOS composites against the user's home-screen wallpaper and applies its own rounded-corner mask; Android does the same for manifest icons. Accepted trade-off: preserves the mark's silhouette over arbitrary wallpapers. If user feedback flags the floating-baseball look as unfinished, re-rasterize onto a `#faf5ef` parchment-filled canvas.

## Out of scope

- **Maskable icons** (`purpose: "any maskable"`). Android's maskable-icon spec expects an **opaque full-canvas background** so the mask shape (circle, squircle, rounded-rect) is visually filled. Our PNGs have transparent backgrounds; declaring `maskable` would leave transparency around the ball wherever the mask doesn't reach. Ball geometry itself (r=12 in 32-unit viewBox = 75% of canvas) already fits within the 80% safe zone, so the content is maskable-ready — but generating a true maskable variant requires re-rasterizing with a parchment-filled background. Defer until add-to-home-screen conversion matters.
- **Windows tile icon** (`browserconfig.xml`). Low ROI in 2026.
- **Safari pinned-tab SVG** (`<link rel="mask-icon">`). Requires a separate monochrome asset. Very low usage. Skip.
- **In-app mark usage** — nav bar, loading screens, 404 page. The existing rebrand spec (`2026-04-10-landing-rebase-rebrand-design.md`) intentionally uses text-only `<BrandName />` in the nav. Introducing the visual mark in-app is a separate UX decision.
- **Monochrome / dark-mode variants.** No dark-mode site yet; revisit when the app ships a dark theme.
- **`web/public/icons.svg`** — a separate sprite of third-party social icons (Bluesky et al.). Unrelated to the brand mark; retained as-is.

## Verification

After wiring is in place:

1. **Favicon** — load `/` in a fresh browser tab. Verify the tab icon shows the tilted baseball, not the `[.D]` text glyph.
2. **Apple touch icon** — on iOS Safari, Share → Add to Home Screen. Verify the home-screen icon shows the mark.
3. **PWA manifest** — open Chrome DevTools → Application → Manifest. Verify name, icons, and theme-color render without errors or warnings. Confirm DevTools Console shows no CSP violations on `/manifest.json`.
4. **Theme-color** — on iOS Safari and Android Chrome, verify the address bar tints to `#faf5ef` on `/`.
5. **OG cards** — post the site URL into Slack, Twitter/X, and iMessage. Verify preview cards show the new mark + wordmark. Force unfurl refresh if cached previews are stale (Slack: `/slack debug unfurl <url>`; Twitter: cards validator).
6. **Lighthouse** — run a Lighthouse audit on `/`; accessibility and best-practices scores should not regress from baseline.

**Automated tests.** `make test` baseline continues to pass: `spa_test.go` asserts on OG metadata strings that are path-based (`/og-landing.png`, `/og-sample.png`), and no paths change. The new `manifest.json` and icon references in `index.html` have no existing test coverage; no new backend tests are required.

## Files changed by implementation

**New files committed as-is (currently untracked in working tree):**
- `web/public/mark.svg`
- `web/public/mark-180.png`
- `web/public/mark-192.png`
- `web/public/mark-256.png`
- `web/public/mark-512.png`
- `web/public/mark-1024.png`

**Modified files committed as-is (currently dirty in working tree):**
- `web/public/favicon.svg`
- `web/public/og-landing.svg`
- `web/public/og-landing.png`
- `web/public/og-sample.svg`
- `web/public/og-sample.png`

**New files created by implementation:**
- `web/public/manifest.json`

**Modified by implementation:**
- `web/index.html` — add `apple-touch-icon`, `manifest`, `theme-color` tags
