# Cancel session triggers evaluation

**Date:** 2026-04-04
**Status:** Fixed

## Symptom

Clicking "Cancel Session" during an interview shows the evaluation waiting screen (spinning indicator, "Analyzing your responses..." messages). The session is never marked cancelled, minutes are not returned correctly, and the user is stuck on a screen that will never resolve.

## Architecture

The cancel flow crosses four layers:

```
UI (interview.tsx)
  → WebSocket message { type: "cancel_session" }
    → Conductor.cancelSession() (Go)
      → Backend.CancelSession() (Go)
        → SQL: CancelSession query (Postgres)
```

The normal end flow is parallel but diverges at the backend:

```
Backend.CompleteSession()
  1. SET status = 'completed'
  2. Refund unused minutes
  3. Enqueue evaluate_session job   ← cancel must NOT do this
  4. Commit

Backend.CancelSession()
  1. SET status = 'cancelled'
  2. Refund unused minutes
  3. (no eval enqueue)
  4. Commit
```

After the WebSocket round-trip, the server sends `session_ended` with a `reason` field:

- `"candidate"` or `"interviewer"` or `"timeout"` — normal end, eval enqueued
- `"cancelled"` — user cancelled, no eval

The UI then decides what to show based on this.

## Root cause

Two bugs, one in each direction of the stack.

### Bug 1: SQL wrote the wrong status

`sql/queries/sessions.sql`, the `CancelSession` query:

```sql
-- BEFORE (bug)
UPDATE interview_sessions
SET status = 'completed', ...

-- AFTER (fix)
UPDATE interview_sessions
SET status = 'cancelled', ...
```

The Go code correctly avoided enqueuing an eval job, but the SQL marked the session `completed` instead of `cancelled`. This meant:

- The session appeared in "completed" lists instead of being hidden
- Any system polling for completed-but-unevaluated sessions could pick it up
- The status didn't reflect reality for debugging or analytics

Additionally, the database CHECK constraint on `interview_sessions.status` (migration 004) didn't include `'cancelled'` as a valid value — it only allowed `('active', 'completed', 'evaluating', 'reviewed', 'evaluation_failed', 'failed')`. Even with the SQL literal fixed, the UPDATE would be rejected by Postgres. A new migration (006) adds `'cancelled'` to the constraint.

### Bug 2: UI didn't distinguish cancel from normal end

`web/src/ws/hooks.ts`, the `session_ended` handler:

```ts
// BEFORE (bug) — ignored the reason entirely
case "session_ended":
  setState("ended");

// AFTER (fix) — routes to different state
case "session_ended":
  setState(msg.reason === "cancelled" ? "cancelled" : "ended");
```

The `reason` field was already typed in the protocol (`protocol.ts` line 30) and sent by the server, but the hook discarded it. Both cancel and normal end mapped to `state === "ended"`.

In `interview.tsx`, `state === "ended"` renders `WaitingView`, which polls the session status every 3 seconds waiting for `"reviewed"` or `"evaluation_failed"`. Since no eval job was enqueued, neither status ever arrives. The user sees an infinite loading screen.

The fix adds a `"cancelled"` state to `InterviewState` and a `useEffect` that navigates to `/` (home) when the state becomes `"cancelled"`.

## What was already correct

- **Conductor** (`conductor.go:538`): Called `backend.CancelSession`, not `CompleteSession`. The right method.
- **Backend** (`session.go:319`): Did not enqueue an eval job. Called `RefundSessionMinutes`. The Go logic was correct.
- **Protocol** (`protocol.ts:30`): Already had `"cancelled"` as a typed reason variant.
- **Server message** (`messages.go:58`): Sent `reason: "cancelled"` in the `session_ended` payload.

The bug was a mismatch at two boundaries: the SQL literal and the UI state mapping. Everything in between was wired correctly.

## Why no test caught it

Zero tests existed for the cancel path. Searching all `*_test.go` files for `cancel_session`, `CancelSession`, or `cancelSession` returns no results. The WebSocket integration tests (`session_ws_test.go`) exercise `end_session` in five separate tests but never send `cancel_session`. The backend billing tests (`billing_test.go`) test `CompleteSession` refund behavior but have no `CancelSession` counterpart.

The coverage gap is asymmetric: the "happy path" (end → eval → review) was well-tested. The "unhappy path" (cancel → refund → go home) was not tested at a single layer.

Specifically:

| Layer | `end_session` tests | `cancel_session` tests |
|-------|-------------------|----------------------|
| SQL query | Implicitly via `CompleteSession` test | **None** |
| `Backend.CompleteSession` / `CancelSession` | `TestCompleteSession_RefundsUnusedMinutes` | **None** |
| WebSocket conductor | 5 tests send `end_session` | **None** |
| UI hook / page | (no unit tests for either) | (no unit tests for either) |

The SQL bug (`'completed'` instead of `'cancelled'`) is the kind of string-literal error that only surfaces when you assert the resulting status. Without a test that reads the session row back and checks `status == "cancelled"`, the wrong literal silently passed.

### Tests added

Two integration tests in `internal/backend/billing_test.go`:

- **`TestCancelSession_SetsStatusCancelled`** — Creates a session, cancels it, reads the row back, and asserts `status = "cancelled"`, `archived_at` is set, and `ended_at` is set. This is the test that catches Bug 1: with the old SQL it fails with `expected "cancelled", got "completed"`.

- **`TestCancelSession_RefundsFullReservation`** — Creates a 30-min session from a 60-min grant, cancels immediately, and asserts the balance is restored (59 min — the 1-min floor is deducted). This mirrors the existing `TestCompleteSession_RefundsUnusedMinutes` test for the cancel path.

These tests require Docker (testcontainers) and were confirmed to compile and vet cleanly. The status test was verified to fail against the buggy SQL before the fix was re-applied.

## Files changed

| File | Change |
|------|--------|
| `sql/queries/sessions.sql` | `status = 'completed'` → `'cancelled'` |
| `internal/db/sessions.sql.go` | Same (generated code) |
| `internal/backend/session.go` | Comment: `completed` → `cancelled` |
| `web/src/ws/hooks.ts` | New `"cancelled"` state; route `session_ended` by reason |
| `web/src/pages/interview.tsx` | Navigate to `/` on cancelled; include in `isEnded` guard |
| `internal/backend/billing_test.go` | Two new tests: `TestCancelSession_SetsStatusCancelled`, `TestCancelSession_RefundsFullReservation` |
| `sql/migrations/006_cancelled_status.up.sql` | Add `'cancelled'` to `interview_sessions_status_check` constraint |
| `sql/migrations/006_cancelled_status.down.sql` | Reverse: drop `'cancelled'` from constraint |
