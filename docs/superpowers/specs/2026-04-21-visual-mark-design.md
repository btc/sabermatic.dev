# Sabermatic[.DEV] Visual Mark

Codify the Sabermatic[.DEV] visual identity: a tilted, stitched baseball rendered in the amber colorway of the landing page. Ship the mark across all platform surfaces that currently render the interim `[.D]` text glyph committed in `24971fc`.

## The mark

The mark is a baseball, tilted `-28°`, with two amber seam arcs meeting at an asymmetric junction slightly below and left of center. Stitch marks along the seams are present in the high-fidelity rendering and omitted from the small-size favicon form.

### Symbolism

Sabermatic derives from sabermetrics — Bill James's term for empirical baseball analytics (SABR, Society for American Baseball Research). The mark makes the etymology visual without naming it. Tilt introduces motion and prevents the mark from reading as a clock, coin, or generic circular badge. The asymmetric seam junction (seam control point offset 2 units left, 1 unit down from circle center on a 32-unit viewBox) makes the mark feel hand-crafted — a deliberate counterpoint to the geometric precision of a typographic mark.

The amber colorway is Tailwind's amber scale on a parchment background, evoking vintage leather baseball, old-book binding, and the "analytical rigor on the surface, monk/devotion/craft underneath" register from `2026-04-03-naming-and-branding-design.md`. Intentionally not SaaS-corporate blue/teal.

### Colorway

| Role | Hex | Tailwind |
|---|---|---|
| Ball interior | `#fef3c7` | amber-100 |
| Seam arcs | `#b45309` | amber-700 |
| Ball outline + stitches | `#92400e` | amber-800 |
| Parchment (backgrounds) | `#faf5ef` | — |

### Geometry

Both `mark.svg` and `favicon.svg` share the same `viewBox="0 0 32 32"` coordinate system:

- Outer group rotated `-28°` about center `(16, 16)`
- Ball: `<circle cx="16" cy="16" r="12">`
- Seams: two quadratic Bézier paths with control point `(14, 17)` — the asymmetric junction
- Stitch marks (mark.svg only): 14 short segments, 7 per side, perpendicular to the seam tangents

The two forms differ in stroke weight and detail, tuned to their rendering size. See file inventory below.

## File inventory

`mark.svg` is the canonical master. `favicon.svg` is a simplified derivative for ≤32px rendering. PNG variants are rasterized from `mark.svg`.

| File | Role | Notes |
|---|---|---|
| `web/public/mark.svg` | Canonical master | 32-unit viewBox rendered to 512×512. Stroke widths 1.3 / 1.0 / 0.38. Includes 14 stitch marks. |
| `web/public/favicon.svg` | Simplified small-size form | Stroke widths 2.5 / 2. Stitches omitted (sub-pixel at 16–32px). Referenced by `<link rel="icon">`. |
| `web/public/mark-180.png` | iOS home-screen icon | 180×180. Apple touch-icon spec. |
| `web/public/mark-192.png` | Android manifest icon | 192×192. PWA manifest minimum. |
| `web/public/mark-256.png` | High-density manifest | 256×256. |
| `web/public/mark-512.png` | PWA manifest max | 512×512. |
| `web/public/mark-1024.png` | Source / press-kit | 1024×1024. |
| `web/public/og-landing.{svg,png}` | Open Graph card — landing page | Mark at top center, wordmark + tagline below. 1200×630. |
| `web/public/og-sample.{svg,png}` | Open Graph card — sample page | Same composition, different tagline. 1200×630. |

### Regenerating PNGs

Mark PNGs are rasterized from `mark.svg`. Use any SVG-to-PNG tool that respects stroke width at non-integer values (e.g., `rsvg-convert`, `resvg`, or a headless Chromium screenshot). OG PNGs are rasterized from their respective SVGs.

No build-time automation — PNGs are committed as static assets. Regeneration is manual and infrequent.

### Provenance

Mark V1 authored 2026-04-12 as the visual half of the Sabermatic[.DEV] rebrand (text half landed in `bbded9b`, `f2f410f`, `c6fe3b0`, `a8be7af`, `4c0b8c4`). The prior `[.D]` text glyph committed in `24971fc` was an interim stand-in. This spec is the retroactive written record of the V1 design work.

## Wiring

The mark currently lives uncommitted in the working tree. This spec commits it and completes the platform surface wiring that the textual rebrand didn't cover.

### Already wired (content-swap only)

| Surface | File | Change |
|---|---|---|
| Browser favicon | `web/index.html:5` | Unchanged — `<link rel="icon" type="image/svg+xml" href="/favicon.svg">` already references the file; just the file content changes. |
| OG cards | `internal/handler/spa.go` | Unchanged — OG URLs already point at `/og-landing.png` and `/og-sample.png`; just the file content changes. |

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

## Out of scope

- **Maskable icons.** The mark has ~12.5% edge padding (r=12 in a 32-unit viewBox), borderline for Android's 10% safe zone. Declaring `purpose: "any maskable"` would over-claim; shipping a dedicated maskable variant would require re-rendering `mark.svg` at a smaller center scale. Skip until add-to-home-screen conversion matters enough to justify the extra asset.
- **Windows tile icon** (`browserconfig.xml`). Low ROI in 2026.
- **Safari pinned-tab SVG** (`<link rel="mask-icon">`). Requires a separate monochrome asset. Very low usage. Skip.
- **In-app mark usage** — nav bar, loading screens, 404 page. The existing rebrand spec (`2026-04-10-landing-rebase-rebrand-design.md`) intentionally uses text-only `<BrandName />` in the nav. Introducing the visual mark in-app is a separate UX decision.
- **Monochrome / dark-mode variants.** No dark-mode site yet; revisit when the app ships a dark theme.

## Verification

After wiring is in place:

1. **Favicon** — load `/` in a fresh browser tab. Verify the tab icon shows the tilted baseball (not the `[.D]` text glyph).
2. **Apple touch icon** — on iOS Safari, Share → Add to Home Screen. Verify the home-screen icon shows the mark.
3. **PWA manifest** — open Chrome DevTools → Application → Manifest. Verify name, icons, and theme-color render without errors.
4. **Theme-color** — on iOS Safari and Android Chrome, verify the address bar tints to `#faf5ef` on `/`.
5. **OG cards** — post the site URL into Slack, Twitter, and iMessage. Verify preview cards show the new mark + wordmark. Use `https://cards-dev.twitter.com/validator` and Slack's unfurl refresh (`/debug url ...`) if cached previews are stale.
6. **Accessibility sanity** — the mark has no text; `<link rel="icon">` is decorative. No ARIA changes needed.

No automated tests. Existing `spa_test.go` OG-tag assertions continue to pass (the test checks metadata, not image content).

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
