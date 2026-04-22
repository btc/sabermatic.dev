# Baseball Mark Integration

**Status:** Draft — pending user review (post review round 1)
**Author:** @briantigerchow
**Date:** 2026-04-22

## Goal

Bring the existing Sabermatic baseball mark (currently used only as a favicon and PWA icon) into the live UI. Give it a distinctive, memorable role across web, email, and social-share surfaces.

## Design direction

**The ball is the `.` in `[.DEV]`** (the "B" treatment selected during brainstorming).

On the web, the baseball renders as an inline SVG that sits between the `[` and `DEV]` brackets, replacing the period in the wordmark `Sabermatic[.DEV]`. The ball scales with whatever font size its parent uses, so one component works from 13px nav text up to 140px hero type.

**Emails use a different treatment** (PNG mark left of plain-text wordmark) because SVG support across email clients is unreliable. In emails, the wordmark remains `Sabermatic[.DEV]` with a period — the mark is adjacent, not inline.

**OG images use the B treatment**, but the SVG must be hand-geometried (see OG section) because `rsvg-convert` doesn't understand `em`-relative inline SVG.

## Scope

| Surface | Treatment | Driver |
|---|---|---|
| `PublicHeader` (landing nav) | B — inline ball | Cascades from `BrandName` |
| `Hero` (landing) | B — inline ball | **Hand-rolled; embeds `<BallMark/>` directly** (see Hero section for why) |
| `AuthLayout` (login, signup, forgot-password, reset-password, verify-email) | B — inline ball | Cascades from `BrandName` |
| `AppLayout` header + loading splash | B — inline ball | Cascades from `BrandName` |
| `error-fallback` | B — inline ball | Cascades from `BrandName` |
| Email wrapper (`internal/email/templates/wrapper.html`) | PNG mark + text wordmark | `TemplateData.LogoURL`, new `RenderEmail` signature |
| `og-landing.svg` + `og-landing.png` | B — inline ball, wordmark-as-hero layout | Hand-edited SVG + regenerated PNG |
| `og-sample.svg` + `og-sample.png` | Same layout as og-landing; only the tagline text differs ("sample evaluation" vs "data-driven system design prep") | Hand-edited SVG + regenerated PNG |
| `internal/handler/spa.go` (server-side OG injection) | Add `twitter:card` + `twitter:image` | Server already injects `og:*` tags per route |

### Out of scope

- **Favicon** (`favicon.svg`) — a tilted whole ball reads better at 16px than "ball as punctuation inside brackets" ever could. Leave as-is.
- **PWA icons** (mark-192/256/512) — standalone mark, same reasoning.
- **Dark-mode-specific ball variant** — the ball's own fills (`#fef3c7`) and strokes (`#92400e`, `#b45309`) hold on both light (`#faf5ef`) and dark near-black backgrounds. No theme branching.
- **Text-only surfaces** (`<title>`, `manifest.json.name`, `og:title`, `branding.AppName`) — can't render SVG. Continue using `Sabermatic[.DEV]` literal text.
- **`index.html` OG meta tags** — already handled server-side by `spa.go:45`. Do not add to `index.html`; would produce duplicate tags on routes with OG injection and emit landing metadata on unrelated routes (`/login`, `/history`) via the SPA fallback.

### Deliberate design decisions

- **Brackets in all text contexts** — `Sabermatic[.DEV]` is the canonical brand form. Do not strip brackets for link previews or Twitter cards; consistency beats rendered polish.
- **Hero stays hand-rolled** — see Hero section.
- **Screen readers announce "Sabermatic dot DEV"** — use literal "dot" in `aria-label`, not a period glyph.

## Components

### `<BallMark/>` — new inline SVG primitive

Location: `web/src/components/ball-mark.tsx`

**Geometry:** Use the detailed geometry from `web/public/mark.svg` (rotated `-28°` CCW, with 14 seam tick lines). Not the simplified `favicon.svg` (no ticks). The hero is the primary showcase; tick detail should be present at large sizes. At small sizes (≤16px effective ball diameter) the ticks will muddy together visually — accepted.

**Sizing:** `width: 0.55em; height: 0.55em;` plus `display: inline-block`. The em-relative size makes a single component work from 13px up to 140px.

**DOM structure — two nodes, specific responsibilities:**

```tsx
<span className="ball-mark-wrap">   {/* owns baseline translateY */}
  <svg className={props.className}> {/* caller className lands here — e.g., group-hover animate-spin */}
    ...
  </svg>
</span>
```

**Why a wrapper span:** Tailwind's `animate-spin` sets `transform: rotate(...)` via CSS animation and replaces any existing `transform` on the same element. If baseline `translateY` sat on the `<svg>`, it would disappear during the spin keyframes, making the ball jump. The wrapper span owns `translateY`; the inner `<svg>` owns caller-supplied animation. The two transforms compose correctly because they live on separate elements.

**Vertical alignment:** `transform: translateY(0.05em)` on the wrapper span (starting value). Acceptance: at `text-sm` (14px) in the nav, the ball reads as a heavy period, with its top roughly at cap-height of the `[` bracket. At 140px hero text, the ball sits below the cap-height of the brackets, reading as a large round punctuation mark. Fine-tune against `[` and `]` bracket glyph metrics in Inter/system-ui during implementation; commit screenshots at 14px, 40px, 140px to the PR.

**Color:** amber fill `#fef3c7`, umber strokes `#92400e` and `#b45309`. Self-colored — holds on cream + dark backgrounds without theme branching.

**API:**
```tsx
interface BallMarkProps {
  /** Applied to the inner <svg>. Use for hover animation, etc.
   *  Do NOT use for transforms — they will clobber the spin animation.
   *  Use props.style if you need to override wrapper positioning. */
  className?: string;
}
```

Keep the component prop-thin. Motion is triggered by a CSS class the *caller* applies, not a `spin` prop, so the ball primitive stays agnostic.

**Accessibility:** `aria-hidden="true"` on the `<svg>`, no `<title>` / `<desc>`. Wrapper span has no ARIA role.

### `<BrandName/>` — updated

Location: `web/src/components/brand-name.tsx` (existing)

```tsx
<span
  role="img"
  aria-label="Sabermatic dot DEV"
  className={cn("whitespace-nowrap", className)}
>
  Sabermatic<span className="opacity-60" aria-hidden="true">[<BallMark />DEV]</span>
</span>
```

**Why `role="img"`:** A plain `<span>` has no role, so `aria-label` is ignored by most screen readers. `role="img"` makes the span a self-contained accessible unit whose name is the label.

**Why `aria-label="Sabermatic dot DEV"`:** Screen readers inconsistently announce `.` as "period", "point", or nothing. "dot" is explicit and matches how the brand would be spoken aloud.

**Why `aria-hidden="true"` on the inner span (defense-in-depth):** `role="img"` on the outer span should treat descendants as presentational, but some readers (notably JAWS) still read through; `aria-hidden` guarantees children aren't re-announced after the label.

**Opacity decision:** `opacity-60` (unchanged from current `BrandName`). Applied to the `[.DEV]` portion. Hero overrides this in its own markup (see below).

### Hero — embeds `<BallMark/>` directly (does NOT use `<BrandName/>`)

Location: `web/src/pages/landing/hero.tsx`

**Why not use `<BrandName/>`:** the hero has three intentional visual divergences from `BrandName`:

1. **Casing**: `sabermatic` (lowercase) vs. `Sabermatic` (title case)
2. **Bracket opacity**: `opacity-40` vs. `opacity-60`
3. **Bracket tracking**: `tracking-[-0.03em]` + `font-bold` vs. none

These differences are deliberate typographic character for the hero, not drift to eliminate. Forcing the hero through `BrandName` would require adding 3 props (`case`, `dim`, `bracketTracking`) to the primitive, polluting every other caller.

**Instead:** the hero keeps its current markup and drops `<BallMark/>` inline:

```tsx
<h1
  id="hero-heading"
  className="group mb-9 text-[clamp(56px,10vw,140px)] font-extrabold leading-[0.9] tracking-[-0.055em]"
  aria-label="Sabermatic dot DEV"
>
  sabermatic<span
    className="font-bold tracking-[-0.03em] opacity-40"
    aria-hidden="true"
  >[<BallMark className="group-hover:animate-[spin_2s_linear_infinite] motion-reduce:animate-none" />DEV]</span>
</h1>
```

Motion (nice-to-have): `group` lives on the `<h1>` itself so the hover hit area matches the visible wordmark (not a wrapper `<div>` or the whole `<section>`). The ball's `className` lands on the inner `<svg>` (see BallMark DOM structure) so `animate-spin` composes with the wrapper span's `translateY` instead of clobbering it. If the rotation feels off in practice, drop the classes from the `<h1>` + `<BallMark>` — `BallMark` is agnostic, so this doesn't affect other surfaces.

### Email wrapper — add PNG mark

Location: `internal/email/templates/wrapper.html`

Header row becomes a table-in-table for Outlook baseline alignment; logo image conditional on `LogoURL`:

```html
<td style="padding:32px 32px 0 32px;">
  {{if .LogoURL}}
  <table role="presentation" cellpadding="0" cellspacing="0"><tr>
    <td style="padding-right:10px;vertical-align:middle;">
      <img src="{{.LogoURL}}" width="28" height="28" alt=""
           style="display:block;border:0;outline:none;text-decoration:none;">
    </td>
    <td style="vertical-align:middle;">
      <p style="margin:0;font-size:14px;font-weight:600;letter-spacing:0.05em;color:#a1a1aa;">
        {{.AppName}}
      </p>
    </td>
  </tr></table>
  {{else}}
  <p style="margin:0;font-size:14px;font-weight:600;letter-spacing:0.05em;color:#a1a1aa;">
    {{.AppName}}
  </p>
  {{end}}
</td>
```

Asset: reuse existing `web/public/mark-256.png`. Rendered at 28×28, retina-ready from a 256px source. `alt=""` — decorative; wordmark text carries the name.

### `RenderEmail` signature change

Location: `internal/email/template.go`

Add `LogoURL` field to `TemplateData`. Change `RenderEmail` signature:

```go
type TemplateData struct {
    AppName string
    LogoURL string       // absolute URL to 256×256 mark PNG, or "" to skip
    Body    template.HTML
    Footer  string
}

// RenderEmail renders the shared email wrapper.
// logoURL should be an absolute URL to a publicly-reachable mark PNG.
// Pass "" to render without a logo (e.g., in tests or if branding asset is unavailable).
func RenderEmail(bodyHTML template.HTML, footer string, logoURL string) (string, error) {
    ...
    err = tmpl.Execute(&buf, TemplateData{
        AppName: branding.AppName,
        LogoURL: logoURL,
        Body:    bodyHTML,
        Footer:  footer,
    })
    ...
}
```

**Callers to update** (verified via grep):

1. `internal/backend/auth.go:168` (sendVerifyEmail) — has `b.cfg.Auth.BaseURL`. Pass `b.cfg.Auth.BaseURL + "/mark-256.png"`.
2. `internal/backend/auth.go:323` (sendResetEmail) — same.
3. `internal/jobs/evaluate.go:286` (`renderEvaluationEmail`, called from `EvaluateWorker` at line 210 which passes `w.BaseURL`). Thread a `logoURL string` parameter through `renderEvaluationEmail`, populated at the caller as `w.BaseURL + "/mark-256.png"`, then passed as the third arg to `email.RenderEmail`.

**URL resolution:**
- The backend reads `cfg.Auth.BaseURL`, populated from the `BASE_URL` env var (`internal/config/config.go`, field `Auth.BaseURL`, default `http://localhost:3000`).
- **Local dev architecture**: `Procfile.dev` runs `vite build --watch` + `air`. There is no separate Vite dev server at runtime; `air` serves the entire embedded SPA (including `/mark-256.png`) at `:8080`.
- The default `BASE_URL=http://localhost:3000` does **not** match the actual backend port (`:8080`). Setting `BASE_URL=http://localhost:8080` in `.env` before running `make dev` makes email `<img src>` URLs resolve locally. Without that override, local-dev emails embed broken URLs — acceptable for routine local work, inconvenient for email smoke tests.
- In prod, `BASE_URL=https://sabermatic.dev` and `/mark-256.png` is served by the embedded SPA via `spa.go`. Works end-to-end.

### OG SVG geometry

Location: `web/public/og-landing.svg`, `web/public/og-sample.svg`

Each SVG is 1200×630. **Current layout**: detached ball (scaled `<g>` at the top, around y=150) *plus* a text wordmark `Sabermatic[.DEV]` below (at y=410, font-size 44) *plus* tagline (at y=460, font-size 22). **New layout**: ball removed from above the wordmark; a single B-treatment wordmark (with the ball inline replacing the `.`) centered at roughly canvas midline, tagline below.

**Technique** — `rsvg-convert` supports a subset of SVG and does NOT interpret `em` units inside `<g transform>`, so inline-SVG tricks that work in browsers fail. Use absolute coordinates:

```
1200px canvas, 630 tall
Wordmark baseline: y=340 (approximately canvas center, font-size ~108)
Tagline baseline: y=418

Left text:     <text x=400 y=340 font-size=108>Sabermatic[</text>
Ball mark:     <g transform="translate(<measured-x> <measured-y>) rotate(-28) scale(<measured-s>)">
                 <circle .../><path .../> …
               </g>
Right text:    <text x=<measured-x-plus-ball-width> y=340 font-size=108>DEV]</text>
Tagline:       <text x=600 y=418 text-anchor=middle font-size=28>data-driven system design prep</text>
```

**Coordinate resolution:** `rsvg-convert` uses whatever font the host system has matching `ui-sans-serif,system-ui,sans-serif`. Text width is deterministic at a given font size on a given host but differs across hosts (macOS → Helvetica, Linux CI → DejaVu Sans). To avoid per-machine drift, measure `"Sabermatic["` width empirically on the committer's machine, hardcode the resulting `x` offsets, and note in the file comment that regeneration must happen on a host with the same fallback font. Alternative: use a webfont via `<defs>` (skipped — `rsvg-convert` doesn't fetch remote fonts). **Horizontal centering**: after measuring, set the starting `x` so that the full rendered width (`Sabermatic[` + ball + `DEV]`) is centered with its midpoint at `x=600`.

**Ball geometry:** reuse the **simplified seam geometry** currently in `og-landing.svg` (two curved seam paths + 8 short seam ticks, all rotated `-28°` CCW). Do *not* use the detailed 14-tick `mark.svg` geometry inline — at the ~60px ball size inside the wordmark, the extra ticks muddy into a blob and add SVG file weight for no visual gain. This is a deliberate divergence from the `<BallMark/>` React primitive (which does use the detailed geometry), justified by the rendering medium (`rsvg-convert`) and the smaller target size. Target visual size: ~0.6× the font cap-height so the ball reads as a period-dot.

**Tagline content:**
- `og-landing.svg`: "data-driven system design prep"
- `og-sample.svg`: "sample evaluation"

Only the tagline `<text>` differs between the two files; everything else (coordinates, ball geometry, font-size) is identical. Copy-paste the landing SVG once the layout is dialed in, then swap the tagline.

**Why regenerate `og-sample.png` too:** keep both OG assets visually coherent with the live site; otherwise the sample-share OG will look like a relic. Cheap to do.

### `spa.go` — add Twitter Card tags

Location: `internal/handler/spa.go` — inside the existing `for path, og := range ogRoutes` loop that builds the `tags` string.

Extend the `Sprintf` format string to also emit:

```html
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="{baseURL}{og.image}">
<meta name="twitter:title" content="{og.title}">
<meta name="twitter:description" content="{og.description}">
```

No new code path — just more tags inside the existing `Sprintf` format string; the per-route `strings.Replace(..., "</head>", tags+"</head>", 1)` stays unchanged.

Update `internal/handler/spa_test.go` to assert the new tags on the same routes it already covers.

## Accessibility

- `<BrandName/>` span: `role="img"` + `aria-label="Sabermatic dot DEV"`; inner styled span is `aria-hidden="true"` to prevent double-reading.
- Hero `<h1>`: gets `aria-label="Sabermatic dot DEV"` overriding the visible text's accessible name. Intentional — without it, screen readers announce "sabermatic [.DEV]" with inconsistent period/bracket treatment.
- `<BallMark/>` SVG: `aria-hidden="true"`.
- Email `<img alt="">`: decorative; wordmark text carries the name.
- Brackets (`[` / `]`) are NOT announced to screen readers at any surface. Deliberate — they're a stylization, not semantic content.

## Motion

- `<BallMark/>` primitive: no motion by default. No `spin` prop.
- **Hero only**: applies `group-hover:animate-[spin_2s_linear_infinite]` via a `className` passed to `<BallMark/>`. `motion-reduce:animate-none` respects `prefers-reduced-motion`. The `group` class goes on the `<h1>`'s parent.
- If rotation is visually noisy in practice, drop the className. No primitive change needed.

## Testing

| Layer | Test |
|---|---|
| Frontend unit (`ball-mark.test.tsx`, new) | Renders SVG with `aria-hidden="true"`, with circle + stitching paths |
| Frontend unit (`brand-name.test.tsx`, new) | Asserts the net-new ARIA attributes added by this spec: `role="img"` and `aria-label="Sabermatic dot DEV"` on the outer span; `aria-hidden="true"` on the inner styled span; visible text contains `Sabermatic`, `[`, `DEV]`; `<BallMark/>` rendered inside the inner span |
| Frontend unit (`hero.test.tsx`, new) | Hero's `<h1>` contains the `<BallMark/>` SVG as a direct descendant (via a `data-testid` on `BallMark` or by matching the ball SVG's circle); `<h1>` has `aria-label="Sabermatic dot DEV"`; `<h1>` does NOT have `role="img"` (which would only be present if the hero had been refactored to use `<BrandName/>` — this assertion guards against accidental refactor) |
| Backend unit (`internal/email/template_test.go`, update) | **Update existing `TestRenderEmail` to the 3-arg signature (pass a concrete `logoURL`).** Add separate asserts for the logo: `Contains(html, "src=\""+logoURL+"\"")`, `Contains(html, "width=\"28\" height=\"28\" alt=\"\"")` (matched as an in-order substring to avoid false positives on unrelated `alt=""` attributes), and a dedicated case that passes `logoURL == ""` and asserts the wrapper does NOT contain `<img `, exercising the `{{if .LogoURL}}` else branch. This file owns tests for the `email.RenderEmail` public API. |
| Backend unit (`internal/jobs/render_email_test.go`, update) | Tests the jobs-internal `renderEvaluationEmail`. The existing `TestRenderEvaluationEmail_EscapesHTML` already passes a `baseURL` ("https://example.com"). Add one new assertion to it: `assert.Contains(t, html, "https://example.com/mark-256.png")`. Does NOT re-test `email.RenderEmail` directly (that's `template_test.go`'s job). |
| Backend unit (`internal/backend/auth_test.go`) | If existing tests cover verify-email or reset-password rendering paths, update the expected HTML fixtures to include the `<img>` tag. Otherwise no change needed. |
| Backend unit (`internal/handler/spa_test.go`, update) | Asserts `twitter:card`, `twitter:image`, `twitter:title`, `twitter:description` on routes that already have OG coverage |
| Manual visual | `/`, `/login`, `/signup`, `/forgot-password`, authed app loading splash, force an error-boundary render. Screenshot each for the PR. |
| Manual visual | Regenerate OG PNGs; visually diff with previous versions; render social-card previews (Twitter/Slack unfurl) |
| Email smoke | Render one email via `render_email_test.go` fixtures and inspect `<img>` tag + resolved URL. Optional: send a real email to a Gmail + Apple Mail + Outlook.com inbox; screenshot. |

**No snapshot tests.** This repo doesn't use Vitest snapshots; assertion-style RTL tests are the pattern. Introducing snapshots here would be a new testing convention and would generate noise on unrelated class-string changes.

Full CI: `make test` must pass.

## Rollout

Single PR, commits sequenced for easy review:

1. Add `<BallMark/>` primitive + `ball-mark.test.tsx`
2. Update `<BrandName/>` to use `<BallMark/>` + `role="img"` + aria-label; update `brand-name.test.tsx`
3. Hero: inline `<BallMark/>` into existing hand-rolled markup; add `hero.test.tsx`
4. Email: extend `TemplateData`, change `RenderEmail` signature, update wrapper.html, update all 3 callers, update `template_test.go` + `render_email_test.go`
5. Regenerate `og-landing.svg|png` and `og-sample.svg|png`
6. `spa.go`: add Twitter Card tags; update `spa_test.go`
7. Add `make regen-og` target to run `rsvg-convert` for both OG PNGs
8. Verify: `make test` + manual screen sweep + email render smoke

## Risks / open questions

- **Baseline alignment** across browsers (Safari/Chrome/Firefox) may differ slightly for the inline SVG. Test all three before committing the final `translateY` value.
- **`rsvg-convert` font rendering drift** — regenerated PNGs use the committer's system fallback font; other contributors regenerating get a different result. Mitigation: document the fallback used (e.g., "Regenerated on macOS with Helvetica fallback") in a comment inside each SVG, and add a `make regen-og` target so the command itself is stable.
- **Email client compatibility:**
  - *Outlook on Windows* uses the Word rendering engine; table-in-table layout handles baseline alignment but vertical-align quirks can still shift the image. Test via litmus or emailonacid if available; otherwise accept and iterate on reports.
  - *Gmail iOS / Outlook dark mode* — auto-inversion can desaturate the amber PNG into a muddy blob. Mitigation: ball has its own strong fill + stroke; acceptable degradation. Not adding explicit dark-mode variant.
  - *HTTPS requirement* — Gmail blocks HTTP `<img>` loads. Staging and prod `BASE_URL` must be HTTPS; local-dev emails will have broken image placeholders for external recipients.
  - *Image-off clients* — some corporate Outlook installs block all remote images by default. The conditional `{{if .LogoURL}}` branch ensures the wordmark text alone still renders cleanly.
  - *Empty `LogoURL`* — tests + staging with no config set render the plain-text header branch, no broken `<img>` displayed.
- **OG PNG regeneration** is manual. `make regen-og` documents the command; future SVG edits require running it. Not ideal but acceptable for low-frequency asset changes.
- **Brackets not announced by screen readers** — deliberate. If user research later shows brackets *should* be spoken, change `aria-label` to `"Sabermatic open bracket dot DEV close bracket"` or similar.
- **`role="img"` on `<span>`** — valid per ARIA spec. Tested readers: NVDA, VoiceOver, JAWS all announce the `aria-label` as the accessible name. If regression testing reveals a reader that doesn't, fall back to moving the label to the nearest semantic ancestor (`<Link>` or `<h1>`).
