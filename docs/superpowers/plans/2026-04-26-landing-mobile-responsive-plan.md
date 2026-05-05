# Landing Page Mobile Responsiveness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the landing page render correctly on mobile (320–640px). Fix inverted Tailwind padding sitewide, lower the hero clamp floor, hide section anchors on small viewports, compact the brand wordmark in the header, and tighten the scoring card's fixed grid.

**Architecture:** Frontend-only, all changes in `web/src/`. Each task is one commit. Tests use vitest + React Testing Library + jsdom (already standard); responsive behavior is verified via Tailwind class-token presence (jsdom doesn't honor media queries) plus a final manual browser smoke test at multiple device viewports.

**Tech Stack:** React 19, React Router v7, Tailwind v4, vitest, @testing-library/react, MemoryRouter.

**Spec:** `docs/superpowers/specs/2026-04-26-landing-mobile-responsive-design.md`

**Important — commit messages:** Use the commit messages in this plan **verbatim**. Do NOT append AI-attribution trailers. The user's global rule (`~/.claude/CLAUDE.md`) forbids any AI-attribution in git history.

**Verification commands:**
- Frontend typecheck: `cd web && npx tsc -b`
- Frontend lint: `cd web && npm run lint`
- Frontend tests: `cd web && npx vitest run`
- Full CI: `make test`
- Local dev: `make dev` (starts overmind with vite on :5173 via the proxy at :8080)

---

## File Structure

Files modified:

- `web/src/components/public-header.tsx` — invert section padding; hide section anchors below `sm`; widen brand `<Link>` tap area; pass `responsiveCompact` to `<BrandName>`.
- `web/src/components/brand-name.tsx` — add `responsiveCompact` prop (opt-in).
- `web/src/pages/landing/hero.tsx` — invert section padding; lower clamp floor 56→36.
- `web/src/pages/landing/voice-pipeline.tsx` — invert section padding.
- `web/src/pages/landing/scoring.tsx` — invert section padding; tighten `DimRow` grid columns on mobile.
- `web/src/pages/landing/strengths-gaps.tsx` — invert section padding.
- `web/src/pages/landing/transcript.tsx` — invert section padding.
- `web/src/pages/landing/coaching.tsx` — invert section padding.
- `web/src/pages/landing/library.tsx` — invert section padding.
- `web/src/pages/landing/cta-repeat.tsx` — invert section padding.
- `web/src/__tests__/brand-name.test.tsx` — add `describe("with responsiveCompact", …)` block.
- `web/src/__tests__/public-header.test.tsx` — add token-regex assertions for the responsive nav-anchor classes.

Files created: none.

Files deleted: none.

---

## Task 1: Invert horizontal padding sitewide

**Why:** The pattern `px-10 sm:px-6` appears in 9 files. Tailwind `sm:` is min-width 640px, so mobile gets 40px gutters and desktop gets 24px — backwards. This is the dominant cause of the cluttered nav and the wrapping hero. Mechanical edit; no new tests (these are visual changes on parent containers, covered by the final browser smoke test).

**Files:**
- Modify: `web/src/components/public-header.tsx:18`
- Modify: `web/src/pages/landing/voice-pipeline.tsx:28`
- Modify: `web/src/pages/landing/scoring.tsx:75`
- Modify: `web/src/pages/landing/strengths-gaps.tsx:43`
- Modify: `web/src/pages/landing/transcript.tsx:49`
- Modify: `web/src/pages/landing/coaching.tsx:50`
- Modify: `web/src/pages/landing/library.tsx:39`
- Modify: `web/src/pages/landing/hero.tsx:44` (non-adjacent tokens)
- Modify: `web/src/pages/landing/cta-repeat.tsx:7` (non-adjacent tokens)

- [ ] **Step 1: Verify the seven adjacent-token files exist**

Run: `grep -rn "px-10 sm:px-6" web/src/ | awk -F: '{print $1}' | sort -u`

Expected output (exactly 7 unique file paths, one per line, in alphabetical order):

```
web/src/components/public-header.tsx
web/src/pages/landing/coaching.tsx
web/src/pages/landing/library.tsx
web/src/pages/landing/scoring.tsx
web/src/pages/landing/strengths-gaps.tsx
web/src/pages/landing/transcript.tsx
web/src/pages/landing/voice-pipeline.tsx
```

Also confirm the count: `grep -rln "px-10 sm:px-6" web/src/ | wc -l | awk '{print $1}'` → expected `7`.

If either output disagrees, stop and re-read the spec's padding-inversion section before continuing.

- [ ] **Step 2: Apply find-and-replace to all seven adjacent-token files**

Use the Edit tool on each file. Old string: `px-10 sm:px-6` → New string: `px-5 sm:px-6 lg:px-10`.

Apply to each of these files in turn:
- `web/src/components/public-header.tsx`
- `web/src/pages/landing/voice-pipeline.tsx`
- `web/src/pages/landing/scoring.tsx`
- `web/src/pages/landing/strengths-gaps.tsx`
- `web/src/pages/landing/transcript.tsx`
- `web/src/pages/landing/coaching.tsx`
- `web/src/pages/landing/library.tsx`

- [ ] **Step 3: Apply targeted edit to `hero.tsx`**

Use the Edit tool on `web/src/pages/landing/hero.tsx`:

Old string:
```
className="px-10 pt-36 pb-28 text-center sm:px-6"
```
New string:
```
className="px-5 pt-36 pb-28 text-center sm:px-6 lg:px-10"
```

- [ ] **Step 4: Apply targeted edit to `cta-repeat.tsx`**

Use the Edit tool on `web/src/pages/landing/cta-repeat.tsx`:

Old string:
```
className="border-t border-border px-10 py-40 text-center sm:px-6"
```
New string:
```
className="border-t border-border px-5 py-40 text-center sm:px-6 lg:px-10"
```

- [ ] **Step 5: Verify no `px-10 sm:px-6` remains anywhere**

Run: `grep -rn "px-10 sm:px-6" web/src/`

Expected output: empty (zero matches).

Run: `grep -rn "sm:px-6 lg:px-10" web/src/ | wc -l | awk '{print $1}'`

Expected output: `9` (the `awk '{print $1}'` strips the leading whitespace that BSD `wc` adds on macOS).

- [ ] **Step 6: Typecheck and lint**

Run: `cd web && npx tsc -b && npm run lint`

Expected: both clean. The current `web/eslint.config.js` does not
include a class-ordering plugin, so no warning is expected. If a
future plugin is added and warns about class ordering, apply the
suggested ordering — the responsive ramp `px-5 sm:px-6 lg:px-10` is
the canonical order (unprefixed → `sm:` → `lg:`) and any auto-fix
should preserve that.

- [ ] **Step 7: Run existing tests**

Run: `cd web && npx vitest run`

Expected: all existing tests pass. No test asserts the literal padding classes.

- [ ] **Step 8: Commit**

```bash
git add web/src/components/public-header.tsx web/src/pages/landing/hero.tsx web/src/pages/landing/voice-pipeline.tsx web/src/pages/landing/scoring.tsx web/src/pages/landing/strengths-gaps.tsx web/src/pages/landing/transcript.tsx web/src/pages/landing/coaching.tsx web/src/pages/landing/library.tsx web/src/pages/landing/cta-repeat.tsx
git commit -m "fix(web): invert landing/header horizontal padding (px-5 sm:px-6 lg:px-10)"
```

---

## Task 2: BrandName `responsiveCompact` prop

**Why:** In the public header at mobile widths, `Sabermatic[🎾DEV]` plus auth links is too wide. We need an opt-in prop that compacts the wordmark to `S[🎾DEV]` below `sm` while preserving the full form everywhere else. TDD: new tests first, then the prop implementation. The default behavior (`responsiveCompact={false}`) must be byte-identical to today, so the existing 5-test suite continues to pass.

**Files:**
- Modify: `web/src/__tests__/brand-name.test.tsx`
- Modify: `web/src/components/brand-name.tsx`

- [ ] **Step 1: Write the failing test**

Use the Edit tool on `web/src/__tests__/brand-name.test.tsx` with the
following old_string / new_string pair. The anchor is the last `it`
block plus the outer closing `});`, which is unique in the file:

Old string:
```tsx
  it("forwards caller className to the outer span", () => {
    const { container } = render(<BrandName className="text-red-500" />);
    const outer = container.firstChild as HTMLElement;
    expect(outer.className).toContain("text-red-500");
    expect(outer.className).toContain("whitespace-nowrap");
  });
});
```

New string:
```tsx
  it("forwards caller className to the outer span", () => {
    const { container } = render(<BrandName className="text-red-500" />);
    const outer = container.firstChild as HTMLElement;
    expect(outer.className).toContain("text-red-500");
    expect(outer.className).toContain("whitespace-nowrap");
  });

  describe("with responsiveCompact", () => {
    it("renders the mobile 'S' fragment and the desktop 'Sabermatic' fragment with the right Tailwind tokens", () => {
      const { container } = render(<BrandName responsiveCompact />);
      const desktop = container.querySelector("span.hidden.sm\\:inline");
      const mobile = container.querySelector("span.sm\\:hidden");
      expect(desktop?.textContent).toBe("Sabermatic");
      expect(mobile?.textContent).toBe("S");
    });

    it("places the bracketed [DEV] suffix immediately after the mobile 'S' fragment", () => {
      const { container } = render(<BrandName responsiveCompact />);
      const mobile = container.querySelector("span.sm\\:hidden");
      const next = mobile?.nextElementSibling as HTMLElement | null;
      expect(next).not.toBeNull();
      expect(next?.getAttribute("aria-hidden")).toBe("true");
      expect(next?.textContent).toContain("[");
      expect(next?.textContent).toContain("DEV");
      expect(next?.textContent).toContain("]");
    });

    it("keeps aria-label canonical regardless of the visible variant", () => {
      const compact = render(<BrandName responsiveCompact />);
      const compactImg = compact.getByRole("img", { name: "Sabermatic dot DEV" });
      expect(compactImg).toBeInTheDocument();
      compact.unmount();

      const full = render(<BrandName />);
      const fullImg = full.getByRole("img", { name: "Sabermatic dot DEV" });
      expect(fullImg).toBeInTheDocument();
    });
  });
});
```

The first new test is a true TDD red — it fails until the prop is
implemented because the new spans don't exist yet. The second is also
red because the `nextElementSibling` of a non-existent `span.sm\:hidden`
is `null`. The third is a regression guard that already passes today
(the canonical aria-label is invariant across both variants); it's
included because the prop change crosses a wire that affects which
DOM tree contains the outer `role="img"` span.

- [ ] **Step 2: Run the new tests to verify they fail**

Run: `cd web && npx vitest run web/src/__tests__/brand-name.test.tsx`

Expected: **2 new failures, 1 new pass, 5 existing pass** (8 tests total). The first two new tests fail at runtime because the rendered output does not contain `span.hidden.sm\:inline` or `span.sm\:hidden` (each `querySelector` returns `null`, the assertions then fail). The third new test (aria-label invariance) already passes today because the outer `<span role="img" aria-label="Sabermatic dot DEV">` exists regardless of `responsiveCompact`.

Note: vitest does NOT typecheck test files (`tsconfig.app.json` excludes `src/**/__tests__`). Passing an unknown prop like `responsiveCompact` to `<BrandName />` produces no TS error at runtime; it's silently ignored. Failures here are runtime DOM-shape failures, not type errors.

- [ ] **Step 3: Implement the prop**

Replace the entire contents of `web/src/components/brand-name.tsx` with:

```tsx
import { BallMark } from "@/components/ball-mark";
import { cn } from "@/lib/utils";

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

- [ ] **Step 4: Run the BrandName tests to verify they pass**

Run: `cd web && npx vitest run web/src/__tests__/brand-name.test.tsx`

Expected: all 8 tests pass (5 existing + 3 new).

- [ ] **Step 5: Run the full frontend test suite**

Run: `cd web && npx vitest run`

Expected: all tests pass. No other component is affected because `responsiveCompact` defaults to `false` and the default branch renders the same DOM shape (bare `"Sabermatic"` text node followed by the bracketed `<span>`) as today.

- [ ] **Step 6: Typecheck and lint**

Run: `cd web && npx tsc -b && npm run lint`

Expected: both clean.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/brand-name.tsx web/src/__tests__/brand-name.test.tsx
git commit -m "feat(web): add BrandName responsiveCompact prop for tight nav contexts"
```

---

## Task 3: Public header — hide section anchors on mobile, expand brand tap area, pass `responsiveCompact`

**Why:** With padding fixed (Task 1) and the BrandName prop available (Task 2), the public header can be made truly mobile-first: hide the two section anchors below `sm`, widen the brand link's tap area to meet the 44×44 guideline, and pass `responsiveCompact` so the brand renders as `S[🎾DEV]` on mobile. TDD: extend the existing test with class-token presence assertions first.

**Files:**
- Modify: `web/src/__tests__/public-header.test.tsx`
- Modify: `web/src/components/public-header.tsx`

- [ ] **Step 1: Write the failing test additions**

Replace the entire contents of `web/src/__tests__/public-header.test.tsx` with:

```tsx
import { render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { PublicHeader } from "@/components/public-header";

function renderAt(pathname: string) {
  return render(
    <MemoryRouter initialEntries={[pathname]}>
      <PublicHeader />
    </MemoryRouter>,
  );
}

describe("PublicHeader", () => {
  it("renders anchor nav on the landing page without a Stack link", () => {
    renderAt("/");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const linkNames = Array.from(nav.querySelectorAll("a")).map((a) => a.textContent?.trim());
    expect(linkNames).toEqual(["Questions", "How it works", "Log in", "Sign up"]);
    expect(screen.queryByRole("link", { name: "Stack" })).toBeNull();
  });

  it("hides anchor links off the landing paths but keeps auth links", () => {
    renderAt("/login");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const linkNames = Array.from(nav.querySelectorAll("a")).map((a) => a.textContent?.trim());
    expect(linkNames).toEqual(["Log in", "Sign up"]);
  });

  it("hides the section anchors below sm and shows them at sm+ via Tailwind tokens", () => {
    renderAt("/");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const anchors = Array.from(nav.querySelectorAll("a")).filter((a) => {
      const text = a.textContent?.trim();
      return text === "Questions" || text === "How it works";
    });
    expect(anchors).toHaveLength(2);
    for (const anchor of anchors) {
      const tokens = anchor.className.split(/\s+/);
      expect(tokens).toContain("hidden");
      expect(tokens).toContain("sm:inline-block");
    }
  });

  it("does NOT apply the responsive-hide tokens to the auth links", () => {
    renderAt("/");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const authLinks = Array.from(nav.querySelectorAll("a")).filter((a) => {
      const text = a.textContent?.trim();
      return text === "Log in" || text === "Sign up";
    });
    expect(authLinks).toHaveLength(2);
    for (const link of authLinks) {
      const tokens = link.className.split(/\s+/);
      expect(tokens).not.toContain("hidden");
      expect(tokens).not.toContain("sm:inline-block");
    }
  });

  it("renders the brand link with an expanded tap area (negative margins cancelling padding)", () => {
    renderAt("/");
    const brandLink = screen.getByRole("link", { name: "Sabermatic dot DEV" });
    const tokens = brandLink.className.split(/\s+/);
    // Hyphen-prefixed classes need exact-token matching, not \b regex,
    // because \b requires a word char on the boundary and "-" is non-word.
    expect(tokens).toContain("-mx-1");
    expect(tokens).toContain("px-1");
    expect(tokens).toContain("-my-3");
    expect(tokens).toContain("py-3");
  });

  it("passes responsiveCompact to the brand mark so mobile renders an 'S' fragment", () => {
    renderAt("/");
    const brandLink = screen.getByRole("link", { name: "Sabermatic dot DEV" });
    const mobileFragment = brandLink.querySelector("span.sm\\:hidden");
    expect(mobileFragment?.textContent).toBe("S");
  });
});
```

- [ ] **Step 2: Run the public-header tests to verify the new ones fail**

Run: `cd web && npx vitest run web/src/__tests__/public-header.test.tsx`

Expected: **3 new failures, 1 new pass, 2 existing pass** (6 tests total). The failures and the pass:

| Test | Status before Task 3 Step 3 | Why |
|---|---|---|
| "hides the section anchors below sm…" | FAIL | section anchors lack `hidden` / `sm:inline-block` tokens today |
| "does NOT apply the responsive-hide tokens to the auth links" | PASS | auth links don't have those tokens today either; this is a regression guard |
| "renders the brand link with an expanded tap area…" | FAIL | brand `<Link>` lacks `-mx-1 -my-3 px-1 py-3` today |
| "passes responsiveCompact to the brand mark…" | FAIL | `<BrandName />` is rendered without the prop today, so no `span.sm:hidden` fragment exists in the DOM |

All three failure messages are token-presence assertions failing because the underlying tokens / DOM nodes are absent.

- [ ] **Step 3: Update `public-header.tsx`**

Replace the entire contents of `web/src/components/public-header.tsx` with:

```tsx
import { Link, useLocation } from "react-router-dom";

import { BrandName } from "@/components/brand-name";

const LANDING_NAV = [
  { href: "#library", label: "Questions" },
  { href: "#pipeline", label: "How it works" },
];

const LANDING_PATHS = new Set(["/", "/about"]);

export function PublicHeader() {
  const location = useLocation();
  const showAnchors = LANDING_PATHS.has(location.pathname);

  return (
    <header className="sticky top-0 z-40 border-b border-border bg-background/90 backdrop-blur-md">
      <div className="mx-auto flex h-[60px] max-w-[1120px] items-center justify-between px-5 sm:px-6 lg:px-10">
        <Link
          to="/"
          className="inline-block -mx-1 -my-3 px-1 py-3 text-sm font-medium tracking-[-0.01em]"
        >
          <BrandName responsiveCompact />
        </Link>
        <nav aria-label="Public navigation" className="flex items-center gap-7 text-[13px] text-muted-foreground">
          {showAnchors &&
            LANDING_NAV.map((a) => (
              <a
                key={a.href}
                href={a.href}
                className="hidden sm:inline-block hover:text-foreground transition-colors"
              >
                {a.label}
              </a>
            ))}
          <Link to="/login" className="hover:text-foreground transition-colors">
            Log in
          </Link>
          <Link
            to="/signup"
            className="rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground"
          >
            Sign up
          </Link>
        </nav>
      </div>
    </header>
  );
}
```

The wrapper div already carries `px-5 sm:px-6 lg:px-10` from Task 1
— the new file content above preserves it. If your diff shows the
padding being re-modified, Task 1 either didn't run or didn't commit;
stop and recover that step before continuing.

- [ ] **Step 4: Run the public-header tests to verify they pass**

Run: `cd web && npx vitest run web/src/__tests__/public-header.test.tsx`

Expected: all 6 tests pass (2 existing + 4 new).

- [ ] **Step 5: Run the full frontend test suite**

Run: `cd web && npx vitest run`

Expected: all tests pass.

- [ ] **Step 6: Typecheck and lint**

Run: `cd web && npx tsc -b && npm run lint`

Expected: both clean.

- [ ] **Step 7: Commit**

```bash
git add web/src/components/public-header.tsx web/src/__tests__/public-header.test.tsx
git commit -m "fix(web): mobile-first public header (hide anchors <sm, expand brand tap area, compact wordmark)"
```

---

## Task 4: Lower hero `<h1>` clamp floor

**Why:** The current `clamp(56px, 10vw, 140px)` produces a 56px wordmark on every viewport below ~560px, which doesn't fit `sabermatic[🎾DEV]` even after the padding fix. Lower the floor to 36px so the curve drives the size at small viewports. No test asserts the clamp value (verified during spec review), so this is a single-line edit followed by browser verification at 320px.

**Files:**
- Modify: `web/src/pages/landing/hero.tsx:53`

- [ ] **Step 1: Apply the clamp change**

Use the Edit tool on `web/src/pages/landing/hero.tsx`:

Old string:
```
className="group mb-9 text-[clamp(56px,10vw,140px)] font-extrabold leading-[0.9] tracking-[-0.055em]"
```
New string:
```
className="group mb-9 text-[clamp(36px,10vw,140px)] font-extrabold leading-[0.9] tracking-[-0.055em]"
```

- [ ] **Step 2: Run the hero tests to verify nothing regressed**

Run: `cd web && npx vitest run web/src/pages/landing/__tests__/hero.test.tsx`

Expected: all 6 existing tests pass. None assert the clamp numeric value.

- [ ] **Step 3: Typecheck and lint**

Run: `cd web && npx tsc -b && npm run lint`

Expected: both clean.

- [ ] **Step 4: Browser-verify at 320px viewport**

Start dev server: `make dev`

Open http://localhost:8080 in Chrome. Open DevTools → Toggle Device Toolbar (Cmd+Shift+M). Set viewport to **320 × 568** (custom or "iPhone SE" first-gen).

Inspect the hero `<h1>`:
- The text `sabermatic[🎾DEV]` must render on a single line.
- There must be ≥4px of horizontal clearance on each side between the wordmark and the section's left/right gutter.
- If the wordmark wraps mid-line OR sits flush against the gutter (<4px clearance), the floor is still too high. **Drop the floor in 2px increments**: edit `hero.tsx:53` again, change `36px` → `34px`, save, refresh. Re-check. If still failing, try `32px`. If `32px` still doesn't fit, **stop** — at that scale the wordmark is too small to function as a hero. Re-open the spec and revisit the design rather than continuing to drop the number.

Document the chosen final floor (36, 34, or 32) in the commit message.

- [ ] **Step 5: Commit**

If the 36px floor was sufficient:

```bash
git add web/src/pages/landing/hero.tsx
git commit -m "fix(web): lower hero clamp floor 56→36 so wordmark fits 320px viewport"
```

If you had to drop further, replace `36` with the actual floor used (e.g., `56→34`).

---

## Task 5: Tighten Scoring `DimRow` grid columns on mobile

**Why:** `scoring.tsx:46` uses `grid-cols-[160px_1fr_80px]`, which needs a minimum 280px. After Task 1 the section's content area at 320px viewport is 232px (320 − 40 outer − 48 inner card padding) — 48px short. The card has `overflow-hidden`, so the deficit clips silently rather than producing a horizontal scrollbar. Tighten the mobile grid columns to fit; restore the full layout at `sm`+. Boy-scout fix per CLAUDE.md.

**Files:**
- Modify: `web/src/pages/landing/scoring.tsx:46`

- [ ] **Step 1: Apply the responsive grid override**

Use the Edit tool on `web/src/pages/landing/scoring.tsx`:

Old string:
```
    <div className={`grid grid-cols-[160px_1fr_80px] items-center gap-5 py-[18px] last:border-b-0 ${rowBorder}`}>
```
New string:
```
    <div className={`grid grid-cols-[110px_1fr_56px] sm:grid-cols-[160px_1fr_80px] items-center gap-5 py-[18px] last:border-b-0 ${rowBorder}`}>
```

- [ ] **Step 2: Typecheck and lint**

Run: `cd web && npx tsc -b && npm run lint`

Expected: both clean.

- [ ] **Step 3: Run the full frontend test suite**

Run: `cd web && npx vitest run`

Expected: all tests pass. No test asserts the literal grid columns.

- [ ] **Step 4: Browser-verify the scoring card at 320px viewport**

Re-open http://localhost:8080 at 320×568 in Chrome DevTools (or refresh if `make dev` is still running).

Scroll to the Scoring section. For each `DimRow` (Requirements / Architecture / Deep Dive / Scalability / Communication / Overall):
- All three columns visible: label on the left, animated bar in the middle, score value on the right.
- Score values like `4 / 5` and the overall `4.2` must be **fully visible** against the card's right edge — no clipping.
- Resize to 640px (sm breakpoint). The grid should restore to the wider `160 / 1fr / 80` layout at exactly that breakpoint.

**Fallback ladder if clipping persists at 320px:**

| Attempt | Mobile grid columns | Mobile grid total |
|---|---|---|
| Default (this plan) | `110px 1fr 56px` | 206px |
| Step down 1 | `100px 1fr 50px` | 190px |
| Step down 2 | `90px 1fr 44px` | 174px |
| **Stop** | If `90px 1fr 44px` still clips | redesign required — the value column doesn't fit in a single grid row at 320px; surface to the spec author rather than continuing to shrink |

After each step, re-verify the 640px breakpoint still uses `160 / 1fr / 80`. Document any deviation from the default in the commit message.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/landing/scoring.tsx
git commit -m "fix(web): responsive DimRow grid so scoring values don't clip at 320px"
```

---

## Task 6: Final verification

**Why:** Confirm the full mobile-responsive pass works end-to-end before considering the work complete. Includes the full CI suite plus a manual browser sweep across the device viewports defined in the spec.

**Files:** none modified (verification only).

- [ ] **Step 1: Run full CI**

Run: `make test`

Expected: green (buf lint, codegen check, frontend typecheck+lint+tests, backend tests).

If anything fails, stop and diagnose. Do not proceed.

- [ ] **Step 2: Browser smoke at iPhone SE 1st gen (320×568)**

With `make dev` running and the page open in Chrome DevTools at 320×568:
- Header shows brand `S[🎾DEV]` + `Log in` + `Sign up` button only — no `Questions` or `How it works`.
- Brand and auth links have visible spacing between them.
- `Sign up` pill button is fully visible (not clipped against the right edge).
- Hero `sabermatic[🎾DEV]` is on a single line with ≥4px clearance per side.
- Scoring card shows all three columns for every `DimRow` with no value clipping.
- All section gutters look tight (20px) but breathable.
- Scroll the entire page top-to-bottom — no horizontal scrollbar at any point.

- [ ] **Step 3: Browser smoke at iPhone SE 2/3 (375×667)**

Resize DevTools to 375×667. Re-scroll the page top-to-bottom and re-run the checklist from Step 2 (header items present, Sign-up not clipped, hero on one line with ≥4px clearance, all DimRow columns visible without clipping, no horizontal scroll). No regressions.

- [ ] **Step 4: Browser smoke at iPhone 14 Pro (393×852)**

Resize to 393×852. Same checklist as Step 2 / Step 3.

- [ ] **Step 5: Browser smoke at iPad mini (768×1024)**

Resize to 768×1024. Verify:
- Section padding is 24px each side (`px-6` from the responsive ramp at the `sm` breakpoint, since 768 < 1024).
- Section anchors `Questions` and `How it works` are visible in the header.
- Brand renders the full `Sabermatic[🎾DEV]` form. Inspect the brand link in DevTools and confirm `span.sm:hidden` is computed `display: none` and `span.hidden.sm:inline` is computed `display: inline`.
- `DimRow` grid uses the wider `160 / 1fr / 80` layout (inspect any DimRow and confirm `grid-template-columns: 160px 1fr 80px`).

- [ ] **Step 6: Browser smoke at desktop (1440×900)**

Resize to 1440×900. Verify:
- Section gutters compute to 40px each (`lg:px-10` is active at ≥1024). Inspect any landing section's outer wrapper and confirm `padding-left: 40px; padding-right: 40px;`. This matches the pre-change `px-10` value exactly.
- Content is centered with `max-w-[1120px]` engaging — surplus viewport (1440 − 1120 − 80 = 240px) is split as `mx-auto` margins on either side.
- Section anchors and full brand wordmark are present, identical to today.

- [ ] **Step 7: No commit needed**

Verification produced no file changes. Report verification status to the orchestrating skill (subagent-driven-development or executing-plans) and the user.
