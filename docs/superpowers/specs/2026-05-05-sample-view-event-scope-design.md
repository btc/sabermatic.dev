# sample_view event scope — design spec

**Status:** spec, awaiting review
**Date:** 2026-05-05
**Author trigger:** post-deploy observation that `sample_view` events fire for visitors who only browse `/`, never `/sample`.
**Goal:** Make `sample_view` mean what its name implies — a visitor reached the `/sample` page — by moving the emission from a backend RPC to a frontend beacon, mirroring `landing_view`.

## Context

The original "Show HN prep" W3 spec (`2026-05-04-show-hn-prep-design.md`) defined `sample_view` as:

> Emitted from: `(*sample.Server).GetSampleSession` success — Identifies: visitor

That spec was implemented faithfully by Chunk C of W3 (commit `337c16a`). Both the spec and 7 rounds of review treated "GetSampleSession success" as a proxy for "visitor saw the sample page."

**This proxy is wrong** because `GetSampleSession` is a data-fetch RPC, not a page-visit handler:

- `web/src/pages/landing/transcript.tsx:32` calls `useSampleSession()` to render the landing-page transcript preview.
- `web/src/pages/landing/scoring.tsx:64` calls it to render the scoring preview.
- `web/src/pages/session/transcript.tsx:249` and `web/src/pages/session/overview.tsx:140` call it (gated on `dataSource === "sample"`) when rendering `/sample`.

The query is cached infinitely (`staleTime: Infinity, gcTime: Infinity` in `web/src/api/sample-queries.ts`). So whichever page first triggers the cache miss fires the RPC, the others get cache hits and don't re-fire. For most visitors the first-touch is the landing page (because they arrive at `/` before clicking through to `/sample`), so `sample_view` is effectively a duplicate of `landing_view`.

### Empirical confirmation

Browsed only `/` in production. BQ shows 4 `sample_view` rows alongside 4 `landing_view` rows from the same visitor cookies. No `/sample` visits occurred.

### What signal we actually want

The product question `sample_view` is supposed to answer is: **"Of visitors who land, what fraction click through to the demo session page (`/sample`) to engage with it?"** That's an engagement signal. The current implementation answers a different question — "did the visitor's browser ever cause sample data to be fetched?" — and the answer is approximately "yes" for everyone who didn't immediately bounce.

This breaks the spec's intended Q6 query (sample-page impact on signup conversion): the `viewed_sample` cohort is essentially "everyone who landed," so the comparison degenerates.

## Why review missed it

Worth capturing for future-review hygiene:

1. **Spec said "fire from GetSampleSession success"; implementation matched the spec.** Spec compliance review verified the literal text and stopped there.
2. **Reviewers focused on the backend emission site** without tracing the SPA call graph upstream. None of the 7 review rounds asked "where else is GetSampleSession called?"
3. **The discriminated-union frontend SDK type intentionally only allowed `landing_view` and `signup_started`** (the beacon allowlist), reinforcing the assumption that `sample_view` was correctly server-side.
4. **Browser smoke test wasn't done before reviews concluded** — only post-deploy.

The general lesson: **when a backend event proxies a frontend behavior, trace the actual call sites that trigger the backend before declaring spec compliance.** Backend code's correctness vs. spec doesn't establish that the spec captures the intended semantic.

## Out of scope

- Renaming the event or introducing a parallel "sample data loaded" event (unnecessary; sample-data-loaded has no product value distinct from landing_view).
- Backfilling or re-attributing historical `sample_view` rows in BQ (cost/value not justified for a Show HN window's worth of mostly-noise data).
- Reviewing every other backend-emitted event for similar proxy issues (likely none — the others all fire from user-action endpoints like Signup/Login/CreateSession/ExecuteTurn, not data-fetch endpoints).

## Design

Move `sample_view` to the same emission pattern as `landing_view`:

| Pattern | Fires when | Identifies | Caching |
|---|---|---|---|
| **Current (broken)** | First call to `GetSampleSession` per visitor session — usually from landing | Visitor (sometimes user_id by accident) | Once per react-query cache lifetime (effectively forever per visitor session) |
| **Proposed (correct)** | `/sample` route mounts in the SPA | Visitor (user_id may or may not be set depending on auth) | Once per page load (same module-scoped guard pattern as `landing_view`) |

### Components

| File | Change |
|---|---|
| `internal/rpc/sample/server.go` | Remove `s.em.Emit(ctx, "sample_view")` from `(*Server).GetSampleSession`. Decide whether to keep the `em` field on the Server struct (see Open Question 1). |
| `internal/handler/beacon.go` | Add `"sample_view"` to `allowedBeaconEvents`. |
| `internal/handler/beacon_test.go` | Extend `TestBeacon_RejectsDisallowedEvent` (or add a sibling) to confirm `sample_view` is now accepted. |
| `web/src/lib/analytics.ts` | Extend the `TrackArgs` discriminated union with `{ event: "sample_view" }` (no props). |
| `web/src/lib/analytics.test.ts` | Add a test that `track({ event: "sample_view" })` posts the right payload. |
| `web/src/pages/sample.tsx` | Add a `useEffect` on mount that fires `track({ event: "sample_view" })`. Use the same module-scoped + `useRef` guard as `web/src/pages/landing/index.tsx` so re-mounts (auth-flip, browser-back, StrictMode dev double-fire) don't re-fire. |

### Open questions to resolve during implementation

1. **Keep or drop the `em` field on `*sample.Server`?** Currently the Chunk A wiring added `Server { ss *SampleService; em *events.Emitter }` and threaded `b.Events()` through `samplerpc.NewServer(...)` in `internal/rpc/register.go:41`. With `sample_view` moving frontend-side, that wiring becomes unused.
   - **Drop it (recommended):** YAGNI. Re-add when there's a real server-side emission for sample. Three-file change (server.go, register.go, server_test.go).
   - **Keep it:** future-proofs for hypothetical server-side sample events (e.g., a "sample_data_validation_failed" diagnostic event). Adds dead injection.
2. **Update the `analytics_events` view DDL?** No view change needed — `sample_view` rows already flow through the same sink/raw-table/view pipeline. Frontend-emitted events land in the same `jsonPayload.event_name` shape as backend-emitted ones.
3. **Historical row treatment?** The BQ data accumulated since 2026-05-05 has rows with the old (wrong) semantic. Recommend leaving them in place and documenting the cutoff: any `sample_view` row before the deploy of this fix means "GetSampleSession RPC fired"; after means "/sample page mounted." Funnel queries pre-cutoff should ignore `sample_view`.

### Testing

Per-file:
- `internal/rpc/sample/server.go`: existing test in `server_test.go` now passes a discard emitter that is no longer exercised; either remove the unused emitter (if Open Q 1 = drop) or leave it as harmless no-op. Either way `make test` should remain green.
- `internal/handler/beacon.go`: add `sample_view` to allowlist; ensure `TestBeacon_AcceptsAllowedEvent` and `TestBeacon_RejectsDisallowedEvent` still cover the right boundaries.
- `web/src/pages/sample.tsx`: add a vitest covering "mounts → fires beacon once," similar to landing's coverage. The module-scoped guard is already a tested pattern.

End-to-end:
- After deploy, browse `/` in a clean visitor cookie. Wait, query BQ — expect ONE `landing_view` and ZERO `sample_view`.
- Browse `/sample` directly. Expect ONE `sample_view`.
- Browse `/` then click through to `/sample`. Expect ONE `landing_view` AND ONE `sample_view` (the cache invalidation/sharing of `useSampleSession` is irrelevant now — emit is page-mount-driven).

### Risk

Low. Reverting a backend emission to a frontend beacon mirrors a well-tested pattern (`landing_view`). The frontend beacon path was the source of a CRITICAL bug during initial implementation (wire-shape mismatch in `analytics.ts`), but that path is now stabilized with end-to-end tests and verified production data flowing through it.

## Implementation order

This is a single workstream, ~half-day, no infra changes. One commit suffices, with the changes batched. Optional finer-grained split:
1. Backend changes (remove emit, add beacon allowlist entry).
2. Frontend changes (add to TrackArgs, fire from `sample.tsx`).
3. Tests + verification.

Single commit is fine because the frontend beacon for `sample_view` would 400 against the old backend (not yet in allowlist) and the backend emission would silently still fire if frontend isn't yet emitting — neither inconsistency is harmful in transit.

## Success criteria

Before marking done:
- [ ] `make test` passes (frontend typecheck + lint + vitest, backend tests with -race, golangci-lint).
- [ ] Manual smoke: browse `/` only → query BQ → ZERO new `sample_view` rows from that session.
- [ ] Manual smoke: browse `/sample` directly → query BQ → ONE new `sample_view` row.
- [ ] Spec/plan updates: amend `2026-05-04-show-hn-prep-design.md` Q6 (sample-page impact query) note to reflect the corrected semantic, OR add a follow-up spec note that Q6 is meaningful only for events emitted after this fix's deploy timestamp.

## Process improvements (for future review hygiene)

Lessons captured from this miss, to apply to subsequent W3-style emission specs:

1. **For each backend-emitted event, trace ALL frontend call sites of the underlying handler.** Don't accept "fires from X handler success" as the spec without confirming X is invoked 1:1 with the user behavior the event name implies.
2. **Browser smoke a fresh deploy with a clean visitor cookie before declaring W3-class work done.** This catches semantic mismatches that pure code review and unit tests can't.
3. **Prefer frontend beacons for "user reached page X" events** unless there's a strong reason to emit server-side. The frontend has the page-context truth; the backend has only inferred signals (Referer, query params).
