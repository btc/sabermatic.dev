# Baseball Mark Integration

**Status:** Draft — pending user review
**Author:** @briantigerchow
**Date:** 2026-04-22

## Goal

Bring the existing Sabermatic baseball mark (currently used only as a favicon and PWA icon) into the live UI. Give it a distinctive, memorable role across web, email, and social-share surfaces.

## Design direction

**The ball is the `.` in `[.DEV]`.**

On the web, the baseball renders as an inline SVG that sits between the `[` and `DEV]` brackets, replacing the period in the wordmark `Sabermatic[.DEV]`. The ball scales with whatever font size its parent uses, so one component works from 13px nav text up to 140px hero type. This is the "B" treatment selected during brainstorming.

Emails use a different treatment (PNG mark left of plain-text wordmark) because SVG support across email clients is unreliable.

## Scope

| Surface | Treatment | Notes |
|---|---|---|
| `PublicHeader` (landing nav) | B — inline ball | Cascades from `BrandName` |
| `Hero` (landing) | B — inline ball | Hero currently uses hand-rolled markup; switch to `<BrandName/>` |
| `AuthLayout` (login, signup, forgot-password, reset-password, verify-email) | B — inline ball | Cascades from `BrandName` |
| `AppLayout` header + loading splash | B — inline ball | Cascades from `BrandName` |
| `error-fallback` | B — inline ball | Cascades from `BrandName` |
| Email wrapper (`internal/email/templates/wrapper.html`) | PNG mark + text wordmark | New asset URL plumbed through `branding.LogoURL` |
| `og-landing.svg` + `og-landing.png` | B — inline ball, wordmark-as-hero layout | Regenerate PNG via `rsvg-convert` |
| `og-sample.svg` + `og-sample.png` | B — inline ball, wordmark-as-hero layout | Regenerate PNG via `rsvg-convert` |
| `index.html` | Add `<meta property="og:image|og:title|og:description">` | Finally wires up the OG assets, which currently ship but aren't referenced |

### Out of scope

- **Favicon** — a tilted whole ball reads better at 16px than "ball as punctuation inside brackets" ever could. Leave the existing `favicon.svg` alone.
- **PWA icons** (mark-192/256/512) — these are the standalone mark, same reasoning.
- **Dark-mode-specific ball variant** — the ball's own fills (`#fef3c7`) and strokes (`#92400e`, `#b45309`) hold on both light (cream `#faf5ef`) and dark near-black backgrounds. No theme branching.
- **Favicon-in-emoji / Unicode replacements** in `<title>` / `manifest.json` "name" — not possible for text surfaces.

## Components

### `<BallMark/>` — new inline SVG primitive

Location: `web/src/components/ball-mark.tsx`

Responsibilities:
- Render the baseball as a single inline SVG
- Size in `em` units so it scales with the parent font size
- Self-colored (amber fill, umber strokes) — no theme branching
- `aria-hidden="true"` (accessible name comes from the wrapping `BrandName`)

Sizing baseline: `0.55em` wide, tuned during implementation so:
- At 13px nav text, the ball reads as a heavy, slightly-oversized period
- At 140px hero text, the ball reads as a proper crest without dominating the wordmark

Vertical alignment: `translateY` tuned to sit on the visual baseline at small sizes and rise toward x-height center at hero sizes. Exact values set during implementation with screenshot comparison.

Motion (nice-to-have, not required to ship):
- Static by default
- Hover: slow 360° rotation over ~2s, applied **only in the hero** (small nav ball rotating would be noisy)
- Gated by `@media (hover: hover) and (prefers-reduced-motion: no-preference)`
- If it feels off in practice, drop it

### `<BrandName/>` — updated

Location: `web/src/components/brand-name.tsx` (existing)

New markup:
```tsx
<span className={cn("whitespace-nowrap", className)} aria-label="Sabermatic.DEV">
  Sabermatic<span className="opacity-60">[<BallMark />DEV]</span>
</span>
```

`aria-label` ensures screen readers say "Sabermatic dot DEV" instead of "Sabermatic open-bracket ball-image DEV close-bracket."

### `Hero` — switch to `<BrandName/>`

Location: `web/src/pages/landing/hero.tsx`

The hero currently hand-rolls the wordmark markup (`sabermatic<span class="...">[.DEV]</span>`). Replace with `<BrandName/>` + the existing size classes, so the hero inherits B and we have a single source of truth.

### Email wrapper — add PNG mark

Location: `internal/email/templates/wrapper.html`

Header row becomes a table-in-table for Outlook baseline alignment:

```html
<td style="padding:32px 32px 0 32px;">
  <table role="presentation" cellpadding="0" cellspacing="0"><tr>
    <td style="padding-right:10px;vertical-align:middle;">
      <img src="{{.LogoURL}}" width="28" height="28" alt=""
           style="display:block;border:0;">
    </td>
    <td style="vertical-align:middle;">
      <p style="margin:0;font-size:14px;font-weight:600;letter-spacing:0.05em;color:#a1a1aa;">
        {{.AppName}}
      </p>
    </td>
  </tr></table>
</td>
```

Asset: reuse existing `web/public/mark-256.png`. Displayed at 28×28, retina-ready from a 256px source.

### `branding.LogoURL` — new config field

Location: `internal/email/template.go` (and whichever package defines `branding.AppName`)

Derive `LogoURL` from the existing public base URL (same source as password-reset links): `{publicBaseURL}/mark-256.png`.

Local dev: `http://localhost:8080/mark-256.png`
Prod: `https://sabermatic.dev/mark-256.png`

Both publicly fetchable, no auth required.

### OG images

Files: `web/public/og-landing.svg`, `web/public/og-landing.png`, `web/public/og-sample.svg`, `web/public/og-sample.png`

Current layout: ball stacked above wordmark + tagline. Replace with the B-treatment wordmark centered on the 1200×630 canvas, tagline below:

```
                Sabermatic[⚾DEV]          ← wordmark with inline ball, ~100–120px
         data-driven system design prep   ← tagline, 22px (landing) / "sample evaluation" (sample)
```

- Ball sized ~0.6em, baseline tuned to read as the period dot
- Font family stays `ui-sans-serif, system-ui, sans-serif`
- Regenerate PNGs once via `rsvg-convert -w 1200 -h 630 og-landing.svg -o og-landing.png` (and same for sample). Commit both SVG + PNG.

### `index.html` — wire up OG meta

Location: `web/index.html`

Add after the existing `<meta>` block:

```html
<meta property="og:title" content="Sabermatic[.DEV]" />
<meta property="og:description" content="data-driven system design prep" />
<meta property="og:image" content="/og-landing.png" />
<meta property="og:type" content="website" />
<meta name="twitter:card" content="summary_large_image" />
<meta name="twitter:image" content="/og-landing.png" />
```

The `/sample` route doesn't have per-route `<meta>` overrides today; that's a separate concern. For now, both routes will share `og-landing.png` from the static `index.html`, which is fine — `og-sample.png` ships but is dormant until routing-aware OG is added.

## Accessibility

- `<BrandName/>` wrapper: `aria-label="Sabermatic.DEV"`
- `<BallMark/>` SVG: `aria-hidden="true"`, no `<title>` / `<desc>`
- Email `<img alt="">`: decorative; wordmark text carries the name
- Hero `<h1 id="hero-heading">`: accessible name flows through `<BrandName/>`'s label

## Motion

Static baseline. Optional hero hover rotation (2s) gated by `(hover: hover) and (prefers-reduced-motion: no-preference)`. Drop if noisy in practice.

## Testing

| Layer | Test |
|---|---|
| Frontend unit | `BrandName` renders inline SVG; `aria-label` present; `[` and `DEV]` text present |
| Frontend unit | `AuthLayout`, `PublicHeader`, `Hero`, `error-fallback` snapshots updated |
| Backend unit | `internal/email/template_test.go` asserts wrapper contains `<img src="…/mark-256.png" width="28" height="28">` |
| Backend unit | `LogoURL` derivation from `publicBaseURL` config (new test) |
| Manual visual | `/`, `/login`, `/signup`, `/forgot-password`, the authed app loading splash, force an error-boundary render. Screenshot each for the PR. |
| Manual visual | Regenerate OG PNGs; visually diff with previous versions |
| Email smoke | Render one email via existing `send_email_test.go` fixtures and inspect the HTML output for the `<img>` tag and resolved `LogoURL` |

Full CI: `make test` must pass.

## Rollout

Single PR, commits sequenced for easy review:

1. Add `<BallMark/>` + update `<BrandName/>` + tests
2. Hero switches to `<BrandName/>` + size classes
3. Email: add `LogoURL` to `branding`, update `wrapper.html`, update tests
4. Regenerate `og-landing.svg|png` and `og-sample.svg|png` with B treatment
5. Wire up OG + Twitter `<meta>` tags in `index.html`
6. Verify: `make test` + manual screen sweep + email render smoke

## Risks / open questions

- **Ball sizing at small text** (13px nav): may need to render slightly larger than `0.55em` for visual weight. Tune during implementation with screenshots.
- **Baseline alignment** in Safari vs. Chrome: `translateY` values may differ slightly. Test both.
- **Email client PNG rendering**: all tested clients (Gmail, Apple Mail, Outlook 2021, iOS Mail) support PNG `<img>` fine. Unverified: legacy Outlook, Yahoo. Accept the risk — worst case the PNG doesn't render and the wordmark text still appears.
- **OG PNG regeneration**: no automated build step. One-time manual `rsvg-convert`. If we iterate on the OG design in future, document the regeneration command in the PR description.
