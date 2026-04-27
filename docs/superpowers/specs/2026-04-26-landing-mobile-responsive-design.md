# 2026-04-26 — Landing page mobile responsiveness

A targeted pass to fix the landing page on small viewports (≤640px). The
header nav, the hero wordmark, and every section's horizontal padding are
all broken on mobile. Single root cause for most of it; small additional
fixes for the hero font scale and the nav information density.

## Motivation

A mobile screenshot of the current landing page shows:

1. The brand wordmark `Sabermatic[🎾DEV]` in the header butting directly
   into the next nav item ("Questions") with no visible gap.
2. Each remaining nav link wrapping its text onto multiple vertical lines —
   "How it works" stacked as three lines, "Log in" stacked as two.
3. The "Sign up" pill button clipped against the right edge of the viewport.
4. The hero `<h1>` "sabermatic[🎾DEV]" wrapping in the middle of the
   wordmark on every mainstream phone viewport (the 56px clamp floor
   produces a wordmark wider than the available content area on any
   viewport below roughly 560px, even after the padding fix).

All four symptoms have a small set of underlying causes:

- **Inverted Tailwind responsive padding.** The pattern `px-10 sm:px-6`
  appears in 9 files (header + 8 landing sections). Tailwind's `sm:` is a
  min-width breakpoint at 640px, so this means *mobile* gets `px-10` (40px
  per side, 80px total) and *tablet+desktop* drops to `px-6` (24px per
  side). Mobile gets *more* horizontal padding than desktop. This single
  inversion eats ~40px of horizontal width on every mobile section, which
  is the dominant cause of the hero text wrapping and a contributing cause
  of the nav clipping.
- **Hero `clamp()` floor too high for small phones.** The hero `<h1>` uses
  `text-[clamp(56px,10vw,140px)]`. The 56px floor combined with
  `font-extrabold` and `tracking-[-0.055em]` produces a wordmark wider than
  the available content area on any viewport ≤430px, even after the
  padding fix.
- **Too many items in the mobile nav.** The header renders five things on
  the landing route: brand wordmark, "Questions" anchor, "How it works"
  anchor, "Log in", "Sign up" button. At gap-7 (28px), that does not fit
  on a 375px viewport regardless of padding.
- **Brand wordmark too long for nav at mobile width.** Even after dropping
  the section anchors, `Sabermatic[🎾DEV]` plus a "Log in" link plus a
  "Sign up" button is still tight at 320–360px. A compacted brand form on
  mobile gives the auth links room to breathe.

## Changes

### 1. Invert horizontal padding sitewide

Across all 9 occurrences, the `px-10` token becomes `px-5` and `lg:px-10`
is inserted into the responsive ramp. Net new ramp: `px-5 sm:px-6 lg:px-10`.

**Seven files have adjacent tokens** — a single literal find-and-replace
of `px-10 sm:px-6` → `px-5 sm:px-6 lg:px-10` is sufficient:

- `web/src/components/public-header.tsx:18`
- `web/src/pages/landing/voice-pipeline.tsx:28`
- `web/src/pages/landing/scoring.tsx:75`
- `web/src/pages/landing/strengths-gaps.tsx:43`
- `web/src/pages/landing/transcript.tsx:49`
- `web/src/pages/landing/coaching.tsx:50`
- `web/src/pages/landing/library.tsx:39`

**Two files have non-adjacent tokens** and require targeted edits:

- `web/src/pages/landing/hero.tsx:44` — current
  `"px-10 pt-36 pb-28 text-center sm:px-6"` →
  `"px-5 pt-36 pb-28 text-center sm:px-6 lg:px-10"` (replace `px-10` with
  `px-5`, append `lg:px-10` after the existing `sm:px-6`).
- `web/src/pages/landing/cta-repeat.tsx:7` — current
  `"border-t border-border px-10 py-40 text-center sm:px-6"` →
  `"border-t border-border px-5 py-40 text-center sm:px-6 lg:px-10"`
  (same shape).

Verified by `grep -rn "sm:px-6 px-10" web/src/` — zero hits, so the
inverse ordering does not exist anywhere; the enumeration above is
exhaustive.

The new responsive ramp:

| Breakpoint     | Class       | Padding each side | Notes                              |
|----------------|-------------|-------------------|------------------------------------|
| `<640px`       | `px-5`      | 20px              | Mobile: tight, maximizes content   |
| `≥640px` (sm)  | `px-6`      | 24px              | Tablet: comfortable                |
| `≥1024px` (lg) | `px-10`     | 40px              | Desktop: matches the design intent |

The `max-w-[1120px]` content cap inside each section is unchanged, so on
viewports ≥1200px the visual layout is identical to today (the extra
gutter never engages — `mx-auto` absorbs the surplus).

### 2. Lower hero `<h1>` clamp floor

`web/src/pages/landing/hero.tsx:53` — change

```
text-[clamp(56px,10vw,140px)]
```

to

```
text-[clamp(36px,10vw,140px)]
```

The minimum is sized to fit `sabermatic[🎾DEV]` on a 320px viewport
(smallest mainstream phone — iPhone SE 1st gen) with the new 20px
gutter. Estimated rendered-width breakdown at 36px font-extrabold,
`tracking-[-0.055em]`:

| Glyph(s)                          | Estimated width |
|-----------------------------------|-----------------|
| `sabermatic` (10 lowercase chars) | ~190–210px      |
| `[` and `]`                       | ~16px           |
| `BallMark` SVG (`0.55em × 0.55em` per `web/src/components/ball-mark.tsx:27`, ≈20px at 36px font-size) | ~20px |
| `DEV` (3 uppercase, `font-bold`, `tracking-[-0.03em]`) | ~50–65px |
| **Total**                          | **~280–310px**  |

Available content area at 320px viewport with `px-5`: 280px. The total
range straddles the available width — the change is likely to fit but
is not guaranteed. The implementation step **must** verify this in a
real browser at 320px width before the change is considered complete.

**Definition of "fits":** the rendered wordmark must clear the
container by ≥4px on each side at 320px viewport (so rendered width
≤272px against the 280px content area). If the natural floor that
achieves this is below 32px, stop and revisit the design rather than
continuing to drop the number — at that font scale the wordmark
becomes too small to function as a hero.

If the 36px floor overflows, drop in 2px increments (34, 32) until the
≥4px clearance target is met.

We deliberately do **not** add `whitespace-nowrap` on the `<h1>`. If the
estimate is off and the text is still a hair too wide, wrapping is a
gentler failure mode than horizontal scroll across the whole section.

The 10vw middle value and 140px ceiling are unchanged — desktop visual is
identical to today.

### 3. Hide section anchors on mobile

`web/src/components/public-header.tsx` — the two section-anchor links
("Questions", "How it works") become `hidden` below `sm` and `inline-block`
at `sm` and above. The "Log in" and "Sign up" links remain visible at
every breakpoint.

The map call already exists at line 24:

```jsx
{showAnchors &&
  LANDING_NAV.map((a) => (
    <a key={a.href} href={a.href} className="hover:text-foreground transition-colors">
      {a.label}
    </a>
  ))}
```

Change the className on the anchor to:

```jsx
className="hidden sm:inline-block hover:text-foreground transition-colors"
```

`inline-block` (rather than `inline`) is used here because the anchors
are direct children of a flex container with `gap-7`. A flex item with
`display:none` is removed from the layout entirely — it does not consume
gap space — which is the desired behavior at mobile. At `sm`+, the
`inline-block` re-inserts each anchor as a flex item that participates
in the gap.

Rationale (option B from brainstorming, vs. a hamburger overlay): a
landing page's mobile nav job is to drive sign-ups. Section anchors are
scroll shortcuts, less useful when the entire page is one finger-flick
long. A hamburger to hide two links is engineering overhead — new
component, focus-trap, escape-to-close, aria-expanded — that doesn't earn
its complexity here.

### 4. Compact brand on mobile in the public header

`web/src/components/brand-name.tsx` — add an opt-in prop that renders the
brand as `S[🎾DEV]` below `sm` and `Sabermatic[🎾DEV]` at `sm` and above:

```tsx
interface BrandNameProps {
  className?: string;
  /** When true, renders only the initial "S" below sm and the full
   *  "Sabermatic" at sm and above. Use in tight nav contexts.
   *  Default `false` preserves the original full-wordmark behavior. */
  responsiveCompact?: boolean;
}

export function BrandName({ className, responsiveCompact = false }: BrandNameProps) {
  return (
    <span
      role="img"
      aria-label="Sabermatic dot DEV"
      className={cn("whitespace-nowrap", className)}
    >
      {responsiveCompact ? (
        <>
          <span className="hidden sm:inline">Sabermatic</span>
          <span className="sm:hidden">S</span>
        </>
      ) : (
        "Sabermatic"
      )}
      <span className="opacity-60" aria-hidden="true">
        [<BallMark />DEV]
      </span>
    </span>
  );
}
```

`web/src/components/public-header.tsx:19–20` — pass `responsiveCompact`
on the BrandName, and widen the parent `<Link>`'s tap area so the
mobile-compact form (~50–60px wide) still meets Apple HIG / Material
44×44px tap-target guidance:

```jsx
<Link to="/" className="inline-block -mx-1 -my-3 px-1 py-3 text-sm font-medium tracking-[-0.01em]">
  <BrandName responsiveCompact />
</Link>
```

Both negative-margin / padding pairs add hit area while cancelling out
in the surrounding flex layout (no visual shift). Horizontal: `-mx-1 px-1`
adds 8px (4px each side) → effective tap width ~58–68px in mobile-compact
mode. Vertical: `-my-3 py-3` adds 24px (12px each side) on top of the
`text-sm` line-height of 20px → effective tap height **44px** exactly,
meeting Apple HIG and Material guidance. The header itself is `h-[60px]`,
which only positions the link via `flex items-center`; it does not enlarge
the link's clickable bounding box, so explicit padding is needed.

The `aria-label="Sabermatic dot DEV"` is unchanged regardless of the
visible form, so screen readers always announce the full brand. The
`[🎾DEV]` suffix is preserved on mobile because (a) it's the brand's
distinctive visual signature and (b) it keeps the URL/TLD reinforcement
("sabermatic.dev") legible.

`<span>` (not `<span class="inline">`) is the right choice for the
mobile/desktop word fragments because the parent uses `whitespace-nowrap`
and we want the fragments to participate in normal text layout without
introducing any baseline shift or block-formatting behavior. `display:none`
removes the inactive fragment cleanly; the active fragment renders as a
plain text inline.

The hero's own large wordmark in `hero.tsx` is *not* affected by this
change — it inlines the full string directly and does not use
`<BrandName>`.

**All five existing `<BrandName>` call sites** are accounted for; only
the public-header receives the new prop:

| Call site                                   | Receives `responsiveCompact`? |
|---------------------------------------------|-------------------------------|
| `web/src/components/public-header.tsx:20`   | **yes** — new                 |
| `web/src/components/error-fallback.tsx:8`   | no — default false            |
| `web/src/layouts/app-layout.tsx:39`         | no — default false            |
| `web/src/layouts/app-layout.tsx:49`         | no — default false            |
| `web/src/pages/auth/auth-layout.tsx:12`     | no — default false            |

Every default-`false` call site renders exactly as today. The default
branch (`else "Sabermatic"`) is a bare text node sitting before the
existing bracketed `[DEV]` `<span>` — the same DOM shape as today — so
the existing assertion in `web/src/__tests__/brand-name.test.tsx:24`
that walks `outer.querySelector("span")` continues to find the
bracketed span as the first `<span>` child and continues to pass.

The auth-layout in particular is intentionally not given
`responsiveCompact`: auth pages render the brand alone in a centered
header with no competing nav items. The full `Sabermatic[🎾DEV]` at
`text-base` (16px) is ~125px wide — fits comfortably even at 320px
viewport, no compaction warranted.

### 5. Tighten Scoring `DimRow` grid columns on mobile (boy-scout)

`web/src/pages/landing/scoring.tsx:46` defines `DimRow` with a fixed grid:

```jsx
<div className={`grid grid-cols-[160px_1fr_80px] items-center gap-5 py-[18px] ...`}>
```

Minimum grid width is `160 + 80 + (2 × 20px gap) = 280px`. The
surrounding card has its own `px-6` (line 94, +48px). With the new
outer section padding `px-5` (40px) at a 320px viewport, the grid has
`320 − 40 − 48 = 232px` available — **48px short**. Because the card
has `overflow-hidden` (line 89), the deficit clips silently rather than
producing a horizontal scrollbar (i.e. the verification step "no
horizontal scroll" would *pass* despite a visible bug — the rightmost
score column would simply be cut off).

This bug exists today and is made marginally less severe (not fixed) by
the §1 padding inversion. Per the project's boy-scout rule (CLAUDE.md:
"When your awareness shines on a piece of tech debt. Address it."),
include the fix in this pass. Change line 46 to:

```jsx
<div className={`grid grid-cols-[110px_1fr_56px] sm:grid-cols-[160px_1fr_80px] items-center gap-5 py-[18px] ...`}>
```

Tightened mobile widths: `110 + 56 + (2 × 20px gap-5) = 206px` — fits
within the 232px budget at 320px viewport with 26px clearance. At
`sm`+ the grid returns to today's `160 / 1fr / 80` layout, so the
design intent is preserved on every viewport that already worked.

Other landing sections were inspected and found safe by construction
(no responsive override needed; sight-check during browser verification
to confirm): `transcript.tsx` uses message-bubble `max-w-[80%]` /
`[85%]` on lines 76 and 108, which shrink with the container.
`voice-pipeline`, `strengths-gaps`, `coaching`, and `library` use auto
/ fr-based grids and inline-flex chips that wrap naturally. Only
`scoring`'s `DimRow` has a fixed-pixel grid that requires the
responsive override above.

## Tests

- `web/src/__tests__/public-header.test.tsx` — existing tests assert link
  text content via `Array.from(nav.querySelectorAll("a")).map(a => a.textContent.trim())`.
  jsdom renders without media-query CSS, so all four anchors remain in
  the DOM and the existing assertions continue to pass unchanged.
  **Add** an assertion that the two section-anchor links each carry both
  the `hidden` and `sm:inline-block` Tailwind tokens (and that the "Log in"
  / "Sign up" links carry neither), to lock the responsive behavior in
  place. Use token-regex matching, not full-string equality, to stay
  robust against future class reordering by `prettier-plugin-tailwindcss`
  or wrapping with `cn(...)`:

  ```ts
  expect(anchor.className).toMatch(/\bhidden\b/);
  expect(anchor.className).toMatch(/\bsm:inline-block\b/);
  ```

- **Extend the existing `web/src/__tests__/brand-name.test.tsx`** (do
  not create a new file — the spec previously specified
  `web/src/components/__tests__/brand-name.test.tsx`, which would
  collide with the existing 5-test suite at the path above). Add a new
  `describe("with responsiveCompact", …)` block covering:
  - render contains both an inline span carrying the `hidden` and
    `sm:inline` tokens whose text content is `Sabermatic`, and a span
    carrying the `sm:hidden` token whose text content is `S`
    (assert via class-token presence, not media-query simulation)
  - `aria-label` remains `"Sabermatic dot DEV"` (inherited from the
    outer `role="img"` span; the existing top-level assertion already
    covers default mode)
  - the bracketed `[<BallMark />DEV]` span continues to render
    immediately after the wordmark fragments, with `aria-hidden="true"`

  Existing tests in that file remain unchanged and continue to pass:
  the default-mode `BrandName` renders the same DOM shape as today
  (bare `"Sabermatic"` text node followed by the bracketed `<span>`),
  so `outer.querySelector("span")` (line 24) continues to find the
  bracketed span first.

- `web/src/pages/landing/__tests__/hero.test.tsx` — does not assert the
  `clamp()` value, only structural facts (aria, BallMark presence, group
  class, lowercase casing). Lowering the floor from 56→36 does not
  break any existing assertion. No test changes required.

## Out of scope

- Hamburger menu, drawer, sheet, or any overlay nav. Option B above
  intentionally avoids introducing this.
- Any change to nav link order, copy, or destinations.
- Any change to brand mark beyond the optional mobile-compact form
  (no new logo glyph, no Ball-only treatment, no color changes).
- Any change to the hero copy, taglines, CTA buttons, or progress bar.
- Section reorder or copy edits in any landing section.
- Auth-layout `<BrandName>` rendering (default behavior preserved).
- Tablet-specific layout tuning between `sm` (640px) and `lg` (1024px).
  The intermediate breakpoint inherits the `sm` styles, which is
  acceptable for this page.

## Verification

- `cd web && npx tsc -b` — typecheck clean.
- `cd web && npm run lint` — no new lint errors.
- `cd web && npx vitest run` — all existing tests pass; new BrandName
  tests pass; updated public-header test passes.
- `make test` — full CI green (buf lint, codegen check, frontend
  typecheck+lint+tests, backend tests).
- Browser smoke test on `make dev`, with DevTools device emulation:
  - **iPhone SE 1st gen (320×568)** — hard floor for this pass:
    - Header shows brand `S[🎾DEV]` + "Log in" + "Sign up" button only
      — no "Questions" or "How it works". Brand and auth links have
      visible spacing. Sign-up button fully visible (not clipped).
    - Hero `sabermatic[🎾DEV]` renders on a single line with ≥4px
      clearance on each side. If overflowing, drop the clamp floor in
      2px increments per §2.
    - Scoring section's "Sample session · ... · 30 min" header bar
      and each `DimRow` (Requirements / Architecture / Deep Dive /
      Scalability / Communication / Overall) shows all three columns:
      label, bar, value. **No clipping** of score values like "4 / 5"
      or "4.2" against the right edge of the card.
    - All section gutters look tight (20px) but breathable.
  - **iPhone SE 2/3 (375×667)** — current actual lower bound for
    iOS traffic; should look noticeably more comfortable than the SE1g
    case above.
  - **iPhone 14 Pro (393×852)**: same as above with comfortable margins.
  - **iPad mini (768×1024)**: section anchors reappear, brand renders
    full `Sabermatic[🎾DEV]`. `DimRow` grid returns to
    `160 / 1fr / 80`. Layout matches desktop intent at smaller scale.
  - **Desktop (1440×900)**: visually identical to today; gutters use
    `px-10` exactly as before.
- Manual horizontal-scroll check on every section at 320px viewport
  width — none should overflow. **Note:** sections with fixed-pixel
  content inside an `overflow-hidden` card (currently only `scoring`'s
  `DimRow`, addressed in §5) can clip silently rather than producing
  scroll. `voice-pipeline.tsx:43` also wraps its outer card in
  `overflow-hidden`, but its mobile content is `grid-cols-1` (fluid
  width), so there is no fixed-pixel clipping risk there. Explicitly
  sight-check the right edge of every card's content at 320px, not
  just window-level scrollbars.

### Notes on viewport choices

iPhone SE 1st gen at 320px is below 1% of mobile traffic in late 2025,
but it's the safest hard floor for layout math — anything that fits
320px fits everything mainstream. The iPhone SE 2/3 (375px) and modern
Android (≥360px) are where the real traffic is; both inherit gracefully
from the 320px-fits design.

## Risks

Low. Each change is mechanical and CSS-only:

- **Padding inversion**: pure mechanical replace across 9 files (7
  literal, 2 token-targeted per §1). Per-breakpoint deltas:
  - <640px: 40 → 20px (more content area; the desired effect)
  - 640–1023px: 24px → 24px (unchanged)
  - ≥1024px: 24 → 40px (more breathing room on desktop)
  No semantic change at any viewport.
- **Hero clamp**: shrinks the floor only; the `10vw` curve and 140px
  ceiling are unchanged. Per-band behavior:
  - **≥560px**: identical to today (today's 56px floor was already
    inactive — `10vw ≥ 56px`, so the curve was driving the size).
  - **360–559px**: *smaller* than today. Today's 56px floor was active
    here; new behavior follows the `10vw` curve down to 36px at 360px,
    rising back to 55.9px at 559px. This is the desired effect — these
    are the viewports where the wordmark currently wraps mid-line.
  - **<360px**: *smaller* than today (down to the new 36px floor). This
    is the band that was clipping the worst.
- **Nav anchors** becoming `hidden sm:inline-block` is a one-class
  change per anchor. The links remain in the DOM for the existing
  tests and for SEO crawlers; only `display` is suppressed at small
  widths.
- **`BrandName` prop** is opt-in (`responsiveCompact` defaults to
  `false`), so the four other call sites (error-fallback, app-layout
  ×2, auth-layout) are byte-for-byte unaffected.
- **Brand link tap-area widening** (`-mx-1 px-1`) cancels itself in
  flex layout terms; no measurable shift to adjacent nav items.
- **Scoring grid mobile override**: introduces a responsive variant
  on a single grid; the existing `≥sm` layout (`160 / 1fr / 80`) is
  preserved exactly. The mobile variant only activates below 640px,
  where the existing layout was clipping.

If 36px still wraps on a real device, the fix is to drop the floor
further (per §2) — one number to change, no architectural implications.
If the tightened scoring grid feels cramped on a real device, it can be
tuned (e.g., `120 / 1fr / 64`) without other code changes.
