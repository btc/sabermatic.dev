# 2026-04-24 — Auth/nav/landing tweaks

Small, bundled set of navigation and copy tweaks across the auth layout, the
post-logout redirect, the public header, and the landing page.

## Motivation

Three small things noticed while reviewing auth and landing flows:

1. The brand mark on auth pages isn't clickable — users expect the logo to
   take them home from any page.
2. After logging out, the app sends users to `/login` rather than the public
   landing page. Logout is an "I'm done" action; the marketing landing is the
   right destination, not another auth prompt.
3. The landing page's "Stack" nav entry and the corresponding "Built with"
   section aren't pulling their weight — the nav entry is being dropped, and
   on reflection the section itself should go. The voice-first "Conversation"
   section is the most distinctive thing we do and belongs at the top of the
   page, with copy that names what's distinctive about it.

## Changes

### 1. Auth logo links home

`web/src/pages/auth/auth-layout.tsx` currently renders `<BrandName>` inside a
static `<div>`. Wrap it in `<Link to="/">` so clicking the mark returns users
to the root landing page from any auth screen (login, signup, forgot-password,
reset-password, verify-email).

No visual change — the link wrapper is transparent. Underline-on-hover styling
is not added; the brand mark is deliberately minimal.

### 2. Logout redirects to `/`

Two call sites navigate to `/login` after a successful logout. Both change
to `/`:

- `web/src/layouts/app-layout.tsx` (top nav "Log out" dropdown item) — line 31.
- `web/src/pages/settings.tsx` (account-deletion flow, which calls logout after
  the delete RPC succeeds) — lines 315 and 316. Both the `onSuccess` and
  `onError` branches of the chained logout mutation change to `/`.

No backend changes — logout already clears the session cookie server-side.

### 3. Remove "Stack" from landing top nav

`web/src/components/public-header.tsx` — delete the
`{ href: "#stack", label: "Stack" }` entry from `LANDING_NAV`. The remaining
entries ("Questions", "How it works") keep their current order.

### 4. Delete the Credits section

The "Built with" / credits section is being removed entirely, not just
dehyperlinked from the nav.

- Delete `web/src/pages/landing/credits.tsx`.
- Remove the `Credits` import and the `<Credits />` element from
  `web/src/pages/landing/index.tsx`.

Sections are individually numbered (each section hardcodes its own `NN —`
label in its JSX), so removing section 07 leaves no gap to renumber in the
remaining sections.

No tests reference the Credits component or the `#stack` anchor.

### 5. Reorder: Conversation section first after Hero

The voice-first "Conversation" section (currently `voice-pipeline.tsx`,
labelled `04 — Conversation`) moves to the top of the section list, right
after the Hero. Section numbers renumber sequentially so the page reads
01→06 top-to-bottom.

New order in `web/src/pages/landing/index.tsx`:

| Position | Component       | File                 | Section label      |
|----------|-----------------|----------------------|--------------------|
| 1        | `Hero`          | `hero.tsx`           | (no number)        |
| 2        | `VoicePipeline` | `voice-pipeline.tsx` | `01 — Conversation`|
| 3        | `Scoring`       | `scoring.tsx`        | `02 — Evaluation`  |
| 4        | `StrengthsGaps` | `strengths-gaps.tsx` | `03 — Evidence`    |
| 5        | `Transcript`    | `transcript.tsx`     | `04 — Transcript`  |
| 6        | `Coaching`      | `coaching.tsx`       | `05 — Coaching`    |
| 7        | `Library`       | `library.tsx`        | `06 — Library`     |
| 8        | `CTARepeat`     | `cta-repeat.tsx`     | (no number)        |

Each file's `NN —` label is a single inline string edit. `id` anchors do not
change: `voice-pipeline.tsx` keeps `id="pipeline"` and `library.tsx` keeps
`id="library"`, so the `#pipeline` and `#library` links in the public header
continue to work.

Nav order in `public-header.tsx` is not changed — "Questions" (`#library`)
stays before "How it works" (`#pipeline`). Reordering the nav is out of scope.

### 6. New headline and subhead for the Conversation section

In `web/src/pages/landing/voice-pipeline.tsx`, lines 34–39:

**Before:**

> You speak. The interviewer speaks back.
>
> A conversation, not a form. Follow-ups out loud. Pushback when you hand-wave. Silence when you're mid-thought. Built to feel like the real thing.

**After:**

> Conversational mock interviews with an expert interviewer.
>
> Adaptive follow-ups. Pushback when you hand-wave. Patient when you're mid-thought.

The new headline names the product (mock interviews) and the authority
signal (expert interviewer). The subhead does different work: it describes
three distinctive behaviors that will be demonstrated by the 3-column
"You / Interviewer / Afterward" diagram immediately below, in the same
parallel structure.

## Out of scope

- Reordering the public header nav links.
- Moving the credits/stack information to a footer or `/about` page (decided
  against — we're removing the signal, not relocating it).
- Changes to the 3-column demo below the Conversation headline.
- Changes to any other section's copy or numbering beyond the renumber that
  falls out of the reorder.
- Changes to logout behavior beyond the redirect target (session cookie
  handling, analytics events, etc.).

## Verification

- `cd web && npx tsc -b` — typecheck clean.
- `cd web && npm run lint` — no new lint errors (removed files, removed imports).
- `cd web && npx vitest run` — all existing tests pass; no test references
  the removed Credits component, the `#stack` anchor, or the old section
  numbering.
- Browser smoke test on `make dev`:
  - Load `/` — confirm section order is Hero → Conversation → Evaluation →
    Evidence → Transcript → Coaching → Library → CTA, numbered 01→06.
  - Confirm the Conversation section headline reads the new copy.
  - Top nav has only "Questions", "How it works", "Log in", "Sign up" — no
    "Stack" link.
  - Click `BrandName` mark on `/login`, `/signup`, `/forgot-password`,
    `/reset-password`, `/verify-email` — each returns to `/`.
  - From a logged-in session, click "Log out" from the top-right dropdown —
    lands on `/`, not `/login`.
  - From `/settings`, trigger account deletion (in a disposable test account)
    — lands on `/` after the chained logout, not `/login`.
- `make test` — full CI green before merge.

## Risks

Low across the board. Each change is mechanical and local:

- The logout redirect change affects any user who logs out; the new
  destination is a public page that will always load, so there's no risk of
  routing to a screen that requires auth.
- Removing the Credits section is a content decision; there's no external
  dependency (no inbound links to `#stack` in emails, docs, or OG tags that
  we're aware of).
- The section reorder is pure JSX reordering plus label string edits; no
  component internals change.
