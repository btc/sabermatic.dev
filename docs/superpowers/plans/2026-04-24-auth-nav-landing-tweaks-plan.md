# Auth / Nav / Landing Tweaks Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Six small UX/copy/navigation tweaks: clickable auth-page logo, logout → `/`, drop the "Stack" nav entry, delete the "Built with" credits section, move the voice-first section to the top of the landing page (renumbered), and rewrite its headline and subhead.

**Architecture:** All changes are frontend-only, in `web/src`. Each task is a single-concern commit. Tests go next to the file they cover (existing convention: `web/src/__tests__/` or `web/src/pages/<area>/__tests__/`). Uses vitest + React Testing Library + MemoryRouter, already standard in this repo.

**Tech Stack:** React 18, React Router v6, vitest, @testing-library/react, connect-query (mocked in tests where needed).

**Spec:** `docs/superpowers/specs/2026-04-24-auth-nav-landing-tweaks-design.md`

**Important — commit messages:** Use the commit messages in this plan **verbatim**. Do NOT append `Co-Authored-By: Claude …` or `Generated with Claude Code` trailers. The user's global rule (`~/.claude/CLAUDE.md`) forbids any AI-attribution in git history; this overrides the default Claude Code system-prompt instruction that would otherwise add such a trailer.

**Verification commands:**
- Frontend typecheck: `cd web && npx tsc -b`
- Frontend lint: `cd web && npm run lint`
- Frontend tests: `cd web && npx vitest run`
- Full CI: `make test`
- Local dev: `make dev` (starts overmind with vite on :5173 via the proxy at :8080)

---

## File Structure

Files created:

- `web/src/pages/auth/__tests__/auth-layout.test.tsx` — unit test for the clickable brand mark.
- `web/src/__tests__/public-header.test.tsx` — unit test covering the "Stack" nav removal and the remaining link set.
- `web/src/pages/landing/__tests__/landing-index.test.tsx` — renders `<Landing />` with mocked connect-query to assert section ordering, renumbering, and Credits removal.
- `web/src/pages/landing/__tests__/voice-pipeline.test.tsx` — unit test for the new headline and subhead copy and the `01 — Conversation` label.

Files modified:

- `web/src/pages/auth/auth-layout.tsx` — wrap `<BrandName>` in `<Link to="/">`.
- `web/src/components/public-header.tsx` — delete the `#stack / Stack` entry from `LANDING_NAV`.
- `web/src/layouts/app-layout.tsx` — change `navigate("/login")` to `navigate("/")` in the logout handler.
- `web/src/pages/settings.tsx` — change both `navigate("/login")` call sites (onSuccess and onError of the chained logout mutation) to `navigate("/")`.
- `web/src/pages/landing/index.tsx` — reorder so `VoicePipeline` comes first after `Hero`; remove the `Credits` import and JSX.
- `web/src/pages/landing/voice-pipeline.tsx` — renumber `04 — Conversation` → `01 — Conversation`, replace headline and subhead.
- `web/src/pages/landing/scoring.tsx` — renumber `01 — Evaluation` → `02 — Evaluation`.
- `web/src/pages/landing/strengths-gaps.tsx` — renumber `02 — Evidence` → `03 — Evidence`.
- `web/src/pages/landing/transcript.tsx` — renumber `03 — Transcript` → `04 — Transcript`.

Files deleted:

- `web/src/pages/landing/credits.tsx`.

Coaching (`05 — Coaching`) and Library (`06 — Library`) retain their current numbers and do not need edits.

---

## Task 1: Auth-page logo links home

**Why:** Users expect the brand mark to be clickable on every page. Currently it's a static span on the auth screens.

**Files:**
- Create: `web/src/pages/auth/__tests__/auth-layout.test.tsx`
- Modify: `web/src/pages/auth/auth-layout.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/auth/__tests__/auth-layout.test.tsx` with the following content:

```tsx
import { render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { AuthLayout } from "@/pages/auth/auth-layout";

function renderAuthLayout() {
  return render(
    <MemoryRouter>
      <AuthLayout>
        <div>child content</div>
      </AuthLayout>
    </MemoryRouter>,
  );
}

describe("AuthLayout", () => {
  it("wraps the brand mark in a link to the root landing page", () => {
    renderAuthLayout();
    const link = screen.getByRole("link", { name: /Sabermatic dot DEV/ });
    expect(link.getAttribute("href")).toBe("/");
  });

  it("keeps the tagline and children rendered outside the link", () => {
    renderAuthLayout();
    expect(screen.getByText("system design, measured.")).toBeInTheDocument();
    expect(screen.getByText("child content")).toBeInTheDocument();
    // Tagline must not be inside the link wrapping the brand.
    const link = screen.getByRole("link", { name: /Sabermatic dot DEV/ });
    expect(link.textContent).not.toContain("system design");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/pages/auth/__tests__/auth-layout.test.tsx`

Expected: FAIL — first test fails with no link matching the name. (`AuthLayout` currently renders `<BrandName>` inside a `<div>`, not a `<Link>`.)

- [ ] **Step 3: Replace `auth-layout.tsx` contents**

Overwrite `web/src/pages/auth/auth-layout.tsx` with:

```tsx
import type { ReactNode } from "react";
import { Link } from "react-router-dom";

import { BrandName } from "@/components/brand-name";

export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="w-full max-w-sm">
        <div className="mb-10 text-center">
          <Link to="/" className="inline-block">
            <BrandName className="text-base font-semibold tracking-wider text-muted-foreground" />
          </Link>
          <p className="mt-2 text-sm text-muted-foreground/70">system design, measured.</p>
        </div>
        {children}
      </div>
    </div>
  );
}
```

Notes on this exact markup:
- `Link` wraps only `<BrandName>`, so the tagline `<p>` stays outside the clickable surface (the second test guards this).
- The `<Link>` deliberately has **no `aria-label`** of its own — `<BrandName>` exposes a `role="img"` with `aria-label="Sabermatic dot DEV"`, which becomes the link's computed accessible name. Adding `aria-label="..."` on the `<Link>` would override that child label and break the test query.
- `inline-block` preserves the previous `<BrandName>` layout behavior (span in flow) while letting the anchor be a clickable block.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/pages/auth/__tests__/auth-layout.test.tsx`

Expected: PASS (both tests).

- [ ] **Step 5: Typecheck**

Run: `cd web && npx tsc -b`

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/auth/auth-layout.tsx web/src/pages/auth/__tests__/auth-layout.test.tsx
git commit -m "feat(web): auth pages wrap brand mark in Link to /"
```

---

## Task 2: Remove "Stack" from landing top nav

**Why:** The linked section (Credits / "Built with") is being removed; the nav entry goes with it.

**Files:**
- Create: `web/src/__tests__/public-header.test.tsx`
- Modify: `web/src/components/public-header.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/__tests__/public-header.test.tsx` with the following content:

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
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/__tests__/public-header.test.tsx`

Expected: FAIL — first test fails because `PublicHeader` currently renders a "Stack" link on the landing path.

- [ ] **Step 3: Remove the Stack entry**

Edit `web/src/components/public-header.tsx`. Change the `LANDING_NAV` array from:

```tsx
const LANDING_NAV = [
  { href: "#library", label: "Questions" },
  { href: "#pipeline", label: "How it works" },
  { href: "#stack", label: "Stack" },
];
```

to:

```tsx
const LANDING_NAV = [
  { href: "#library", label: "Questions" },
  { href: "#pipeline", label: "How it works" },
];
```

No other changes in this file.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/__tests__/public-header.test.tsx`

Expected: PASS (both tests).

- [ ] **Step 5: Typecheck**

Run: `cd web && npx tsc -b`

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/public-header.tsx web/src/__tests__/public-header.test.tsx
git commit -m "feat(web): drop Stack entry from landing top nav"
```

---

## Task 3: Logout redirects to `/`

**Why:** Logout is an "I'm done" action. The public landing is the right destination, not another auth form.

Two call sites change. Both are tiny string edits (`"/login"` → `"/"`), covered by manual browser verification in Task 7 — no new unit test is added here because mocking the full auth/query-client surface of `AppLayout` and `Settings` is disproportionate to a two-character change.

**Files:**
- Modify: `web/src/layouts/app-layout.tsx`
- Modify: `web/src/pages/settings.tsx`

- [ ] **Step 1: Update `app-layout.tsx`**

Edit `web/src/layouts/app-layout.tsx`, line 31. Change:

```tsx
  const handleLogout = () => {
    logout.mutate({}, { onSuccess: () => navigate("/login") });
  };
```

to:

```tsx
  const handleLogout = () => {
    logout.mutate({}, { onSuccess: () => navigate("/") });
  };
```

- [ ] **Step 2: Update `settings.tsx`**

Edit `web/src/pages/settings.tsx`, inside `handleDelete` (around lines 310–327). Change:

```tsx
    deleteAccount.mutate({}, {
      onSuccess: () => {
        logout.mutate({}, {
          onSuccess: () => navigate("/login"),
          onError: () => navigate("/login"),
        });
      },
```

to:

```tsx
    deleteAccount.mutate({}, {
      onSuccess: () => {
        logout.mutate({}, {
          onSuccess: () => navigate("/"),
          onError: () => navigate("/"),
        });
      },
```

- [ ] **Step 3: Typecheck**

Run: `cd web && npx tsc -b`

Expected: no errors.

- [ ] **Step 4: Run existing tests**

Run: `cd web && npx vitest run`

Expected: PASS — no test references the previous `/login` redirect target.

- [ ] **Step 5: Commit**

```bash
git add web/src/layouts/app-layout.tsx web/src/pages/settings.tsx
git commit -m "feat(web): logout redirects to / (public landing) instead of /login"
```

---

## Task 4: Delete the Credits section

**Why:** The section isn't earning its spot. Its nav entry was already being dropped in Task 2.

**Files:**
- Delete: `web/src/pages/landing/credits.tsx`
- Modify: `web/src/pages/landing/index.tsx` (remove import + JSX usage)

This task does not add a test of its own — Task 5 adds a landing-index test that already asserts the Credits content (the "No magic" headline, the `07 —` label, the `#stack` id) is absent. If Task 5 runs after Task 4, that test's coverage includes this removal.

- [ ] **Step 1: Delete the Credits file**

```bash
git rm web/src/pages/landing/credits.tsx
```

- [ ] **Step 2: Remove the Credits import and JSX from `landing/index.tsx`**

Edit `web/src/pages/landing/index.tsx`. Change:

```tsx
import { Coaching } from "./coaching";
import { Credits } from "./credits";
import { CTARepeat } from "./cta-repeat";
import { Hero } from "./hero";
import { Library } from "./library";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Transcript } from "./transcript";
import { VoicePipeline } from "./voice-pipeline";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Transcript />
      <VoicePipeline />
      <Coaching />
      <Library />
      <Credits />
      <CTARepeat />
    </div>
  );
}
```

to:

```tsx
import { Coaching } from "./coaching";
import { CTARepeat } from "./cta-repeat";
import { Hero } from "./hero";
import { Library } from "./library";
import { Scoring } from "./scoring";
import { StrengthsGaps } from "./strengths-gaps";
import { Transcript } from "./transcript";
import { VoicePipeline } from "./voice-pipeline";

export default function Landing() {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Transcript />
      <VoicePipeline />
      <Coaching />
      <Library />
      <CTARepeat />
    </div>
  );
}
```

(JSX reordering happens in Task 5; here we only remove `<Credits />`.)

- [ ] **Step 3: Typecheck + build sanity**

Run: `cd web && npx tsc -b`

Expected: no errors — `credits.tsx` has no other importers.

- [ ] **Step 4: Run existing tests**

Run: `cd web && npx vitest run`

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/landing/index.tsx
git commit -m "feat(web): remove Credits (Built with) section from landing"
```

(The deletion of `credits.tsx` was already staged by `git rm` in Step 1.)

---

## Task 5: Reorder sections and renumber labels

**Why:** The Conversation section is the most distinctive moment in the product. Put it first after the Hero. Renumber so the page reads 01→06 top-to-bottom.

**Files:**
- Create: `web/src/pages/landing/__tests__/landing-index.test.tsx`
- Modify: `web/src/pages/landing/index.tsx`
- Modify: `web/src/pages/landing/voice-pipeline.tsx` (label only; headline/subhead change is Task 6)
- Modify: `web/src/pages/landing/scoring.tsx` (label only)
- Modify: `web/src/pages/landing/strengths-gaps.tsx` (label only)
- Modify: `web/src/pages/landing/transcript.tsx` (label only)

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/landing/__tests__/landing-index.test.tsx` with the following content:

```tsx
import { render } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

// Stub every landing section so this test exercises ONLY landing/index.tsx
// JSX ordering. Each section becomes a single <div> with a stable data-testid.
// Rationale: Scoring/StrengthsGaps/Transcript/Coaching all read different
// data shapes via `useSampleEvaluation`/`useSampleSession`/`useSampleCoach`
// and early-return null on missing data. Trying to feed them a single canned
// shape from a global useQuery mock results in those sections rendering
// nothing, which would make any DOM-order assertion meaningless. Stubbing
// the section components themselves keeps this test focused on the
// responsibility of `landing/index.tsx`: composing sections in a specific
// order. Per-section label and copy assertions live in their own files
// (e.g. `voice-pipeline.test.tsx`).
vi.mock("@/pages/landing/hero", () => ({
  Hero: () => <div data-testid="section-hero" />,
}));
vi.mock("@/pages/landing/voice-pipeline", () => ({
  VoicePipeline: () => <div data-testid="section-conversation" />,
}));
vi.mock("@/pages/landing/scoring", () => ({
  Scoring: () => <div data-testid="section-evaluation" />,
}));
vi.mock("@/pages/landing/strengths-gaps", () => ({
  StrengthsGaps: () => <div data-testid="section-evidence" />,
}));
vi.mock("@/pages/landing/transcript", () => ({
  Transcript: () => <div data-testid="section-transcript" />,
}));
vi.mock("@/pages/landing/coaching", () => ({
  Coaching: () => <div data-testid="section-coaching" />,
}));
vi.mock("@/pages/landing/library", () => ({
  Library: () => <div data-testid="section-library" />,
}));
vi.mock("@/pages/landing/cta-repeat", () => ({
  CTARepeat: () => <div data-testid="section-cta" />,
}));

import Landing from "@/pages/landing";

function renderLanding() {
  return render(
    <MemoryRouter>
      <Landing />
    </MemoryRouter>,
  );
}

describe("Landing (index)", () => {
  it("renders sections in the new order with Conversation first after Hero", () => {
    const { container } = renderLanding();
    const order = Array.from(container.querySelectorAll("[data-testid^='section-']"))
      .map((el) => el.getAttribute("data-testid"));

    expect(order).toEqual([
      "section-hero",
      "section-conversation",
      "section-evaluation",
      "section-evidence",
      "section-transcript",
      "section-coaching",
      "section-library",
      "section-cta",
    ]);
  });

  it("does not render the removed Credits section", () => {
    const { container } = renderLanding();
    // If `landing/index.tsx` were to re-import and render `<Credits />`,
    // either typecheck would fail (the file is deleted in Task 4) or — if
    // somehow re-introduced — the unmocked component would render and add
    // a <section> with id="stack" / "No magic" copy. Neither should appear.
    expect(container.querySelector("[data-testid='section-credits']")).toBeNull();
    expect(container.querySelector("#stack")).toBeNull();
  });
});
```

Notes on this approach:
- Every section is mocked at the module boundary, so this test never touches `useQuery`, `useSampleEvaluation`, `useScrollReveal`, or any other section internals. It tests exactly one thing: the JSX composition in `landing/index.tsx`.
- Renumbering inside individual section files (Scoring `→ 02`, StrengthsGaps `→ 03`, Transcript `→ 04`) is verified by the browser smoke check in Task 7 — those are one-character mechanical edits and the typecheck/lint will catch any structural breakage. The new `01 — Conversation` label is verified specifically by `voice-pipeline.test.tsx` in Task 6.

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/pages/landing/__tests__/landing-index.test.tsx`

Expected: FAIL — first test fails with the old order:
```
Expected: ["section-hero", "section-conversation", "section-evaluation", "section-evidence", "section-transcript", "section-coaching", "section-library", "section-cta"]
Received: ["section-hero", "section-evaluation", "section-evidence", "section-transcript", "section-conversation", "section-coaching", "section-library", "section-cta"]
```

- [ ] **Step 3: Reorder `landing/index.tsx`**

Edit `web/src/pages/landing/index.tsx`. Change the JSX body from the post-Task-4 state:

```tsx
      <Hero />
      <Scoring />
      <StrengthsGaps />
      <Transcript />
      <VoicePipeline />
      <Coaching />
      <Library />
      <CTARepeat />
```

to:

```tsx
      <Hero />
      <VoicePipeline />
      <Scoring />
      <StrengthsGaps />
      <Transcript />
      <Coaching />
      <Library />
      <CTARepeat />
```

Imports can stay alphabetical; order of imports does not affect render order.

- [ ] **Step 4: Renumber `voice-pipeline.tsx` section label**

Edit `web/src/pages/landing/voice-pipeline.tsx`, line 32. Change:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 04 — Conversation
```

to:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 01 — Conversation
```

(Do not touch the headline or subhead in this task — Task 6 owns those lines.)

- [ ] **Step 5: Renumber `scoring.tsx` section label**

Edit `web/src/pages/landing/scoring.tsx`, line 79. Change:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 01 — Evaluation
```

to:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 02 — Evaluation
```

- [ ] **Step 6: Renumber `strengths-gaps.tsx` section label**

Edit `web/src/pages/landing/strengths-gaps.tsx`, line 47. Change:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 02 — Evidence
```

to:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 03 — Evidence
```

- [ ] **Step 7: Renumber `transcript.tsx` section label**

Edit `web/src/pages/landing/transcript.tsx`, line 53. Change:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 03 — Transcript
```

to:

```tsx
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 04 — Transcript
```

- [ ] **Step 8: Run test to verify it passes**

Run: `cd web && npx vitest run src/pages/landing/__tests__/landing-index.test.tsx`

Expected: PASS (both tests).

- [ ] **Step 9: Typecheck**

Run: `cd web && npx tsc -b`

Expected: no errors.

- [ ] **Step 10: Commit**

```bash
git add web/src/pages/landing/index.tsx \
        web/src/pages/landing/voice-pipeline.tsx \
        web/src/pages/landing/scoring.tsx \
        web/src/pages/landing/strengths-gaps.tsx \
        web/src/pages/landing/transcript.tsx \
        web/src/pages/landing/__tests__/landing-index.test.tsx
git commit -m "feat(web): move Conversation section to first slot; renumber 01–06"
```

---

## Task 6: New Conversation section copy

**Why:** The Conversation section is now first after the Hero; the old headline and subhead were written when it sat mid-page. The new copy names the product (mock interviews), the authority signal (expert interviewer), and three distinctive behaviors (adaptive, pushback, patient) in parallel structure that mirrors the 3-column demo below.

**Files:**
- Create: `web/src/pages/landing/__tests__/voice-pipeline.test.tsx`
- Modify: `web/src/pages/landing/voice-pipeline.tsx`

- [ ] **Step 1: Write the failing test**

Create `web/src/pages/landing/__tests__/voice-pipeline.test.tsx` with the following content:

```tsx
import { render, screen } from "@testing-library/react";
import React from "react";
import { describe, expect, it } from "vitest";

import { VoicePipeline } from "@/pages/landing/voice-pipeline";

describe("VoicePipeline", () => {
  it("renders the new headline", () => {
    render(<VoicePipeline />);
    const heading = screen.getByRole("heading", { level: 2 });
    // Use jest-dom's toHaveTextContent because React preserves the JSX
    // newline+indent whitespace inside the <h2>; toBe() against the bare
    // string would fail on the surrounding whitespace.
    expect(heading).toHaveTextContent(
      "Conversational mock interviews with an expert interviewer.",
    );
  });

  it("renders the new subhead", () => {
    render(<VoicePipeline />);
    expect(
      screen.getByText(
        "Adaptive follow-ups. Pushback when you hand-wave. Patient when you're mid-thought.",
      ),
    ).toBeInTheDocument();
  });

  it("is labelled as section 01 — Conversation", () => {
    const { container } = render(<VoicePipeline />);
    const label = container.querySelector("section header > div");
    expect(label?.textContent?.replace(/\s+/g, " ").trim()).toBe("01 — Conversation");
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd web && npx vitest run src/pages/landing/__tests__/voice-pipeline.test.tsx`

Expected: FAIL — the first two tests fail because the file still has the old headline ("You speak. The interviewer speaks back.") and old subhead. The third test passes (Task 5 already renumbered the label to `01 — Conversation`).

- [ ] **Step 3: Replace the headline and subhead in `voice-pipeline.tsx`**

Edit `web/src/pages/landing/voice-pipeline.tsx`, lines 34–39. Change:

```tsx
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            You speak. The interviewer speaks back.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            A conversation, not a form. Follow-ups out loud. Pushback when you hand-wave. Silence when you're mid-thought. Built to feel like the real thing.
          </p>
```

to:

```tsx
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Conversational mock interviews with an expert interviewer.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Adaptive follow-ups. Pushback when you hand-wave. Patient when you're mid-thought.
          </p>
```

Keep the surrounding `<header>`, className, and `max-w-[20ch]` constraint unchanged — the new headline is longer than the old one, but `max-w-[20ch]` is measured in `ch` units against the font-size and will wrap naturally.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd web && npx vitest run src/pages/landing/__tests__/voice-pipeline.test.tsx`

Expected: PASS (all three tests).

- [ ] **Step 5: Typecheck**

Run: `cd web && npx tsc -b`

Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add web/src/pages/landing/voice-pipeline.tsx web/src/pages/landing/__tests__/voice-pipeline.test.tsx
git commit -m "feat(web): new Conversation headline and subhead copy"
```

---

## Task 7: Full verification — CI and browser smoke

**Why:** Every task has passed its targeted tests, but we should also confirm the full test suite is green and that the UI-affecting changes look right in a real browser.

**Files:** none modified.

- [ ] **Step 1: Run full frontend test suite**

Run: `cd web && npx vitest run`

Expected: all tests pass. If any pre-existing test fails, investigate — there should be no interaction between these changes and other tests.

- [ ] **Step 2: Run frontend lint**

Run: `cd web && npm run lint`

Expected: no errors. (Removed imports should not leave dangling lint warnings.)

- [ ] **Step 3: Run frontend typecheck**

Run: `cd web && npx tsc -b`

Expected: no errors.

- [ ] **Step 4: Run full CI**

Run: `make test`

Expected: buf lint ok, codegen check ok, frontend typecheck+lint+tests ok, backend tests ok.

- [ ] **Step 5: Browser smoke test**

Start local dev in a separate terminal: `make dev`. Then in a browser at the proxied dev URL:

1. **Landing order + numbering** — open `/`. Scroll through and confirm:
   - Section order (top to bottom): Hero → Conversation → Evaluation → Evidence → Transcript → Coaching → Library → CTA.
   - Section labels read `01 — Conversation`, `02 — Evaluation`, `03 — Evidence`, `04 — Transcript`, `05 — Coaching`, `06 — Library`.
   - No `Built with` / `No magic. Good tools.` section anywhere on the page.
2. **Conversation copy** — confirm the H2 reads "Conversational mock interviews with an expert interviewer." and the paragraph below reads "Adaptive follow-ups. Pushback when you hand-wave. Patient when you're mid-thought."
3. **Top nav** — confirm the landing top nav shows only `Questions`, `How it works`, `Log in`, `Sign up` (no `Stack`). Click `Questions` — page scrolls to the Library section (`#library`). Click `How it works` — page scrolls to the Conversation section (`#pipeline`).
4. **Auth logo link** — visit each of `/login`, `/signup`, `/forgot-password`. On each, click the brand mark at the top of the card — confirm the browser navigates to `/`.
5. **Logout redirect (app-layout)** — log in with a test account, then open the avatar dropdown and click `Log out`. Confirm the browser lands on `/` (the public landing), not `/login`.
6. **Logout redirect (account deletion)** — this one is destructive, so use a disposable test account only. From `/settings`, trigger account deletion. After the chained logout, confirm the browser lands on `/`, not `/login`. If a disposable test account is not available, skip this step and record that the code path was verified by inspection only.

- [ ] **Step 6: Final report**

Post a brief summary to the PR / chat: which checks ran, which browser steps passed, and flag anything skipped.

---

## Self-review notes

- Each spec requirement (auth logo, logout redirect, Stack nav entry, Credits deletion, reorder + renumber, new copy) maps to exactly one task above. No gaps.
- No placeholders: every step that changes code includes the exact before/after source, and every command has an explicit expected result.
- Type and name consistency: `BrandName`, `PublicHeader`, `AuthLayout`, `VoicePipeline`, `Landing`, `LANDING_NAV`, `handleLogout`, `handleDelete` are used as they appear in source. No inventing APIs.
- Task 4 does not add its own test because Task 5's `landing-index.test.tsx` covers the removal; the plan says so explicitly so a reader doesn't try to add a redundant test. Task 5 must run after Task 4 for that coverage to be meaningful — the task order above reflects that.
- Task 3 skips adding a unit test by design (two-character change; mocking the auth/query-client surface is disproportionate). The browser smoke in Task 7 is the real check.
