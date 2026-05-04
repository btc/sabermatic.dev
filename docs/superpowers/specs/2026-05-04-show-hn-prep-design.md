# Show HN prep — design

**Status:** spec, awaiting review
**Date:** 2026-05-04
**Goal:** Prepare sabermatic.dev for a Show HN post by addressing the few real risks: undersized infra under load, no way to measure the spike, missing trust-signal pages, and dead infrastructure code that's been masking the first problem.

## Context

Show HN is for the product, not the repo. The repo's GitHub-link polish (root README, etc.) is out of scope. What matters is that visitors arriving from HN can:

1. Reach the site without it falling over
2. Find the trust-signal pages (about, terms, privacy) they expect
3. Sign up and try the product
4. Be measured — we want to reconstruct the funnel after the spike

There's also a vestigial liability we want to clear: the DB connection pool was sized at 80 per instance for a now-removed advisory-lock architecture (each session held a dedicated connection). With 2 instances at 80 connections that's a 160-connection target on a Cloud SQL `db-g1-small` instance whose default `max_connections` is 100 (Postgres default; not overridden in Terraform). The cluster has not failed only because it's never actually scaled to 2 instances simultaneously. Without right-sizing, bumping the Cloud Run cap (Workstream 2) would expose the over-provisioning.

## Out of scope

- ClickHouse or any new analytics infra (Cloud Trace + BigQuery sink covers it)
- Third-party analytics SDKs (PostHog, Plausible, GA4 — explicitly rejected; we own the data)
- A "report a bug" feedback channel on the landing page
- Real-time analytics dashboards (BQ is post-hoc only)
- Renaming the `drill_session` cookie to `sabermatic_session` (would invalidate every active session; defer until a planned auth-system change makes the migration cheap)
- Implementing real cascading data deletion in `Backend.DeleteAccount` (currently soft-delete only; privacy text drafted to match current behavior; cascading delete is a separate spec)
- Geographic analytics (`ip_country` enrichment)
- Building UI for GDPR data export (manual fulfillment via psql/GCS within 30 days is acceptable; UI is a follow-up spec)
- Lawyer review of terms/privacy text (drafting plain-English versions ourselves)

---

## Workstream 1: DB connection pool right-sizing

### Problem

`internal/config/config.go` defaults `DATABASE_MAX_POOL_SIZE` to 80, with a comment justifying it as "each active session pins a connection for its advisory lock." The advisory-lock architecture is gone — production code has zero callers of `PGTryAdvisoryLock` / `PGAdvisoryUnlock`. The pool is sized for an architecture that no longer exists.

`db-g1-small` (current Cloud SQL tier) does not have a `max_connections` override in Terraform, so the value is the Postgres default of 100. The original comment in the code uses this number; the system has been running with pool=80 successfully against that ceiling for weeks, which empirically confirms it.

### Design

Reduce default to `25` per instance. Math: 25 × max 3 instances = 75 conns, leaves 25 for psql/migrations/superuser/monitoring under a 100-conn cap.

**Why 25, not 15** (revised after code audit): the original 15-conn estimate assumed River AI workers would release their connection during external API calls. **The audit (now complete) shows they do not.** `internal/jobs/evaluate.go`, `coach.go`, and `educator.go` each open a transaction at the top of the work function, then call `w.LLM.CallToolAndLog(ctx, tx, ...)` — passing the in-flight tx into the LLM call so the call's metadata is logged in the same atomic unit as the evaluation/coach/educator results. The connection is held for the full LLM round-trip (multi-second to multi-minute).

Worst-case per-instance concurrent connections from this:
- 10 AI workers × 1 conn each = 10 (held during LLM)
- 5 default + 5 notify + 2 maint + 2 gemini workers, each potentially holding ~1 conn briefly = ~5 average concurrent
- HTTP handlers at 100 concurrent reqs × ~10ms queries ≈ 1–2 concurrent
- River internal queue polling, leadership = ~1–2

Realistic peak: ~15–18 conns per instance. Pool of 25 gives ~30% headroom for transient bursts. 25 × 3 = 75 < 100, comfortably under the cap.

Going lower (e.g., to 15) requires refactoring the AI workers to release the tx before the LLM call and re-acquire after — see follow-up below. That's the right boy-scout fix but is out of scope for Show HN.

Delete the dead advisory-lock SQL source; run `sqlc generate` to remove the generated artifacts.

### Known follow-up (separate spec)

**AI worker LLM-call decoupling.** Refactor `evaluate.go`, `coach.go`, `educator.go` to:
1. Call LLM standalone (no tx held), capture toolInput + call metadata (tokens, latency).
2. Open tx.
3. Insert llm_call row using captured metadata + insert evaluation/coach/educator results + mark messages.
4. Commit.

This is the pattern `internal/jobs/generate_image.go` already uses correctly. After this refactor, the pool can be reduced further (10–15 per instance is plausible) and we get cleaner separation of "external API" from "DB work." Out of scope here because (a) it requires careful idempotency design (what if step 3 fails after step 1 succeeded — re-run the LLM?), and (b) Show HN doesn't depend on it. Add to the post-Show-HN backlog as its own spec.

### Components

| File | Change |
|---|---|
| `internal/config/config.go` | `DATABASE_MAX_POOL_SIZE` default `80` → `25`; rewrite comment to drop advisory-lock justification (use the math above; reflect the AI-worker tx pattern explicitly) |
| `sql/queries/advisory_locks.sql` | Delete |
| `internal/db/advisory_locks.sql.go` | Removed by `sqlc generate` after the SQL source is deleted |
| `internal/db/querier.go` | `PGTryAdvisoryLock` and `PGAdvisoryUnlock` methods removed by `sqlc generate` |

Implementation step: after deleting `sql/queries/advisory_locks.sql`, run `sqlc generate`, then `go build ./...` to confirm nothing referenced the removed methods (already verified to be the case).

### Testing

- `go build ./...` — compile-time verification
- `sqlc diff` — exits 0 (verifies generated files are in sync with sources after deletion)
- `make test` — full CI suite

No new tests; removing dead code.

### Pre-deploy verification

Run `SHOW max_connections;` against prod Cloud SQL once and record the actual value in this spec section before merging W1. Reviewers disagree on whether `db-g1-small`'s default is the Postgres default 100 or a Cloud SQL tier-specific 50:
- Existing `internal/config/config.go` comment (written when pool=80 was set) says 100
- The system has run with pool=80 successfully for weeks — empirically the cap must be ≥ pool peak, suggesting 100
- Cloud SQL documentation cites 50 as a common shared-core tier default

The pool=25 target works under either ceiling for a single instance (25 < 50), and works for 3 instances under 100 (75 < 100). If the verified value turns out to be 50, then `max_instance_count = 3` exceeds the cap (75 > 50) and W2 must downsize either the cap or the pool. **Ship the verification before W2 applies.**

### Monitoring criterion (post-deploy)

Watch Cloud SQL "current connections" metric during HN spike. If it regularly approaches the per-instance ceiling, bump pool via env override (no code change) or downsize Cloud Run cap. If it stays comfortably below ~15 per instance, the sizing is conservative and W1's known follow-up (AI-worker refactor) becomes higher-leverage.

### Risk

Low for the deletion (pure removal of unreferenced code). Low for the sizing change at pool=25 (accommodates current AI-worker tx pattern with margin). Risk concentrates on the unverified `max_connections` ceiling — resolved by the verification step above. Rollback is `DATABASE_MAX_POOL_SIZE=80` env override, no code change.

---

## Workstream 2: Cloud Run instance cap bump

### Problem

`terraform/cloud_run.tf:10` sets `max_instance_count = 2`. With 100 concurrent requests per instance, that's a ceiling of 200 concurrent in-flight requests — under HN front-page hug-of-death range.

### Design

Bump to `3`. Bounded by Workstream 1's pool sizing: 25 × 3 = 75 < 100 max_connections (gated on verification step in W1; if real cap is 50, this workstream must downsize before applying).

Capacity math: 100 concurrent reqs/instance × 3 instances = 300 in-flight requests. At ~200ms per request, that's 300 / 0.2s = 1500 requests/sec sustained ceiling for short read requests. Comfortable for realistic HN spike (5–50 RPS sustained, occasional bursts).

Caveat on the RPS ceiling: AI turns (interview generation) take multi-second LLM latency, so concurrency slots fill up for longer and effective throughput is much lower. Plan capacity on **concurrent active sessions**, not RPS. The HN audience is browsing landing/sample pages and doing signup, not running interviews simultaneously, so the bottleneck during the spike is short-request capacity (which 1500 RPS easily handles).

### Components

| File | Change |
|---|---|
| `terraform/cloud_run.tf:10` | `max_instance_count = 3` (was 2) |

### Testing

`terraform plan` shows the diff; `terraform apply` after the spec is approved and Workstream 1 is shipped (Workstream 1 must land first or the cluster will exceed Cloud SQL caps under load).

### Risk

Low. `min_instance_count` stays at default (0); we accept 1–2 second cold start per instance over paying for an always-warm instance during the Show HN window.

### Open follow-ups (not in this spec)

- If the spike exceeds capacity at 3 instances, the next bump options are: (a) refactor AI workers to release the LLM-call tx (W1 follow-up spec) and lower pool to 10–15, freeing room to add instances; (b) upgrade Cloud SQL tier (e.g., `db-custom-1-3840`, ~$25/mo more, gives more memory and a higher computed `max_connections` floor). Defer the decision.

---

## Workstream 3: Analytics events to BigQuery

### Goal

Reconstruct the Show HN funnel after the spike: traffic source → landing → signup → email verification → first session → completed session. No third-party analytics; emit structured events from the backend, ship via Cloud Logging to BigQuery, query with SQL.

### Architecture

```
Backend code (events.Emitter)
   ↓ slog.LogAttrs (one JSON line per event, "analytics_event":true marker)
Cloud Run stdout
   ↓ (automatic forwarding by Cloud Run)
Cloud Logging
   ↓ (Logs Router sink, filter on jsonPayload.analytics_event=true)
BigQuery dataset `sabermatic_analytics`, table `analytics_events_raw` (LogEntry schema, sink-managed)
   ↓ (BQ view `analytics_events` flattens jsonPayload into top-level columns)
Read-time queries hit the view
```

Hot path is fire-and-forget: emission is one slog write. Cloud Logging buffers/retries to BQ. App is unaware of BQ availability.

**Important architectural fact:** Cloud Logging Logs Router → BigQuery sinks write the full LogEntry format to the destination table. The sink **manages the table schema itself** — you cannot pre-create a flat table with custom column names and have the sink populate it. Custom event fields land at `json_payload.<field>`, not as top-level columns. The flattening view (defined below) is what gives us the clean queryable schema; do not skip it.

### Event taxonomy

Single events table. One row per event. Funnel queries: `WHERE event_name IN (...)`.

| Event | Emitted from | Identifies |
|---|---|---|
| `landing_view` | Frontend beacon (`POST /api/beacon`) on landing mount | visitor |
| `sample_view` | `(*sample.Server).GetSampleSession` success | visitor |
| `signup_started` | Frontend beacon when user clicks "Sign up" / "Continue with Google" | visitor |
| `signup_completed` | `Backend.Signup` success | visitor + new user_id |
| `oauth_completed` | `Backend.OAuthLogin` success | visitor + user_id |
| `email_verified` | `Backend.VerifyEmail` success | user_id |
| `session_created` | `Backend.CreateSession` success | user_id + session_id |
| `first_message_sent` | `Backend.ExecuteTurn` first turn for a session | user_id + session_id |
| `session_ended` | Three terminal-state paths (see handler table) — `reason` property distinguishes | user_id + session_id |

### BigQuery destination — raw table + flattening view

#### Raw table: `analytics_events_raw`

The Logs Router sink writes here in **LogEntry format**. The sink manages the schema; we do not pre-create columns. Terraform creates the dataset and configures the sink with `use_partitioned_tables = true` (date-partitioned by ingestion time). The sink will create the table on first write.

The raw table will have LogEntry-standard columns: `timestamp`, `severity`, `logName`, `resource` (RECORD), `labels` (RECORD), `trace`, `spanId`, `httpRequest`, `jsonPayload` (RECORD with our custom fields), etc. Our slog event payload lands inside `jsonPayload`.

Note: Cloud Logging's built-in `trace` field already provides Cloud Trace correlation in the format `projects/PROJECT/traces/TRACE_ID`. Our `events.Emitter` does not need to stamp `trace_id` separately; we extract from the `trace` field in the view.

#### Read view: `analytics_events`

A BQ view (Terraform: `google_bigquery_table` with `view` block) flattens `jsonPayload` into the clean column shape we want for queries. This is what all read-time queries reference.

**Sink jsonPayload typing — RECORD assumed.** Cloud Logging Logs Router → BigQuery sinks default to writing `jsonPayload` as a RECORD with auto-discovered subfields (BOOL/STRING/NUMBER native typed columns under `jsonPayload.*`). JSON typing is opt-in via newer schema options; we do not opt in. The view DDL below uses **direct field access** (`jsonPayload.event_name`), not `JSON_VALUE()` — using `JSON_VALUE` on RECORD-typed columns is a type error and will fail at view creation.

```sql
CREATE OR REPLACE VIEW `sabermatic_analytics.analytics_events` AS
SELECT
  timestamp                                                  AS event_time,
  jsonPayload.event_id                                       AS event_id,
  jsonPayload.event_name                                     AS event_name,
  jsonPayload.visitor_id                                     AS visitor_id,
  NULLIF(jsonPayload.user_id,    '')                         AS user_id,
  NULLIF(jsonPayload.session_id, '')                         AS session_id,
  REGEXP_EXTRACT(trace, r'traces/(.+)$')                     AS trace_id,
  NULLIF(jsonPayload.referer,      '')                       AS referer,
  NULLIF(jsonPayload.utm_source,   '')                       AS utm_source,
  NULLIF(jsonPayload.utm_medium,   '')                       AS utm_medium,
  NULLIF(jsonPayload.utm_campaign, '')                       AS utm_campaign,
  NULLIF(jsonPayload.path,         '')                       AS path,
  NULLIF(jsonPayload.user_agent,   '')                       AS user_agent,
  jsonPayload.properties                                     AS properties
FROM `sabermatic_analytics.analytics_events_raw`
WHERE jsonPayload.analytics_event IS TRUE
```

The `WHERE jsonPayload.analytics_event IS TRUE` filter is redundant with the sink's filter (the sink only routes matching log entries to this table) but is kept as defense-in-depth in case the sink filter is ever loosened.

**Subfield auto-discovery caveat:** BQ infers RECORD subfield types from observed values. If a slog field is sometimes-missing or sometimes-typed-differently, BQ may classify it as JSON or STRING. This affects only first-write schema setup — once the column is typed, all subsequent writes coerce to it. Mitigation: always emit every event field (use empty string for missing, never omit), and always emit the same Go type per field. The pre-flight smoke test (next section) catches typing surprises before the spike.

**Bootstrap order (resolves view-vs-table ordering):** BigQuery permits view creation against a non-existent underlying table — view creation succeeds; queries against the view error until the table appears. So `terraform apply` order is:
1. `google_bigquery_dataset.analytics`
2. `google_logging_project_sink.analytics`
3. `google_bigquery_table.analytics_events_view` (`depends_on = [google_bigquery_dataset.analytics]` is sufficient)

The sink lazily creates `analytics_events_raw` on first write. After deployment, smoke-test by emitting one event from the browser and querying via the view within ~5 minutes.

Logical columns the view exposes (what queries see):

| Column | Type | Notes |
|---|---|---|
| `event_id` | STRING | UUID per event, dedup key |
| `event_name` | STRING | Filter / group key |
| `event_time` | TIMESTAMP | From LogEntry timestamp |
| `visitor_id` | STRING | First-party cookie identifier |
| `user_id` | STRING | NULL until auth |
| `session_id` | STRING | NULL outside a session |
| `trace_id` | STRING | Extracted from LogEntry `trace` field; joins to Cloud Trace |
| `referer` | STRING | **Populated only on `landing_view`** (first-touch attribution) |
| `utm_source` | STRING | Same |
| `utm_medium` | STRING | Same |
| `utm_campaign` | STRING | Same |
| `path` | STRING | URL path on emission |
| `user_agent` | STRING | |
| `properties` | RECORD (auto-typed) | Event-specific extras. Access via `properties.<field>`. (BQ may infer JSON instead if first-write data is heterogeneous; fall back to `JSON_VALUE(properties, '$.<field>')` in queries if so.) |

Funnel attribution semantic: `referer` and `utm_*` are stamped only on `landing_view`. Other events join back to landing_view by `visitor_id` for first-touch source. No mutable cookie state.

Why view, not pre-created table or scheduled query: a view is free, always-fresh, no operational moving parts. Querying through it is essentially querying the raw table with a SELECT projection — BQ optimizes through the view.

Performance: queries via the view scan the raw table. The sink writes with `use_partitioned_tables=true`, which partitions by **ingestion time** (`_PARTITIONTIME`), not by our `event_time` column. Queries that filter on `event_time` will NOT trigger partition pruning unless they also constrain `_PARTITIONTIME`. At HN-spike scale (~10K events/day), full-table scans cost cents — accept this. If volume grows, expose `_PARTITIONTIME` as `event_partition_date` in the view so queries can use it for pruning. Clustering on `event_name`/`visitor_id` is not available since the sink controls the table — accept this for the same reason.

### Components

#### New: `internal/events/`

| File | Purpose |
|---|---|
| `events.go` | `Emitter` type with `Emit(ctx, name, props...)`. Reads `visitor_id`, `user_id`, `referer`, `utm_*`, `trace_id` from context. Writes one slog line at INFO with `analytics_event=true`. Fire-and-forget; no error return. |
| `context.go` | Typed context-key helpers for analytics fields. Single `contextValues` struct keyed in ctx. |
| `events_test.go` | Test that `Emit` writes the expected fields and reads from context correctly. Uses a recording slog handler. |

Emission API:

```go
type Emitter struct {
    logger *slog.Logger
}

func NewEmitter(logger *slog.Logger) *Emitter

func (e *Emitter) Emit(ctx context.Context, name string, props ...slog.Attr)
```

#### New: `internal/handler/middleware.go` analytics middleware

`AnalyticsContextMiddleware` runs on all HTTP routes:

- Reads `visitor_id` cookie. If absent, generates a new UUID and sets the cookie (HttpOnly, SameSite=Lax, 1-year, Secure when BaseURL is HTTPS).
- Captures `Referer` header into context (used by non-beacon requests; beacon explicitly overrides — see beacon handler below).
- Parses `utm_source`, `utm_medium`, `utm_campaign` from URL query string.
- Stashes one `events.ContextValues` struct in `ctx`.

**Mount point — must wrap the top-level mux including ConnectRPC paths.** Existing handler chain in `internal/handler/server.go:53-55` is `SecurityHeaders(secureCookies, otelhttp.NewMiddleware(...)(mux))`. Modify by wrapping `mux` with `AnalyticsContextMiddleware` BEFORE the otel middleware sees it, so OTel still observes the wrapped chain and analytics context is populated for every request including ConnectRPC handlers (sample/session/etc.).

Concretely, change the assignment so that `mux` is replaced by `AnalyticsContextMiddleware(mux)` at line 44 (or where the SPA route is registered) before being passed into `otelhttp.NewMiddleware(...)(...)`. Then `SecurityHeaders(secureCookies, otelHandler)` stays untouched.

Verify after wiring with a smoke test: from inside a Connect handler, `events.ContextValuesFromContext(ctx)` returns non-nil with a populated `visitor_id`.

**HttpOnly rationale:** visitor_id is HttpOnly so it's not reachable from JavaScript / XSS. Frontend does not need to read it — all events flow through the backend (either directly via Connect handlers, or via the `/api/beacon` POST), and the backend reads the cookie server-side.

**Cookie bootstrap on the initial HTML response:** the SPA's `index.html` is served by Cloud Run too (Vite-built static SPA). Ensure `AnalyticsContextMiddleware` runs on the initial HTML GET so the `Set-Cookie` header arrives before any beacon fires. Without this, `navigator.sendBeacon` may race ahead of cookie storage on the very first visit.

**User_id propagation — TWO auth paths to extend:**

The codebase has two separate authentication mechanisms:

1. `internal/rpc/interceptor.go:67` — `AuthInterceptor.authenticate()` for ConnectRPC handlers (sample, session, billing, etc. — where almost all events emit)
2. `internal/handler/middleware.go:51` — `RequireAuth` for the `/admin/jobs/` REST routes only

Extend BOTH to attach user_id to the analytics context:

- In `interceptor.go`: after `auth.WithUser(ctx, user)`, also `ctx = events.WithUserID(ctx, user.ID.String())`
- In `middleware.go`: after the existing span attribute set, also `ctx = events.WithUserID(ctx, user.ID.String())` and pass via `r = r.WithContext(ctx)`

Without the interceptor extension, all authenticated event emissions (signup_completed, oauth_completed, session_*, etc.) would lack user_id since they fire from inside Connect handlers.

#### New: `internal/handler/beacon.go`

`POST /api/beacon` handler.

Request body:
```json
{
  "event_name": "landing_view" | "signup_started",
  "referrer":   "<from document.referrer, may be empty>",
  "utm_source": "<from URLSearchParams, may be empty>",
  "utm_medium": "<may be empty>",
  "utm_campaign": "<may be empty>",
  "properties": { /* event-specific */ }
}
```

Validates `event_name` against an allowlist (`landing_view`, `signup_started` only — backend events go through their own handlers, not this beacon).

**Referrer/UTM precedence:** for beacon events, the body-provided values take precedence over the `Referer` header captured by the middleware. The middleware-captured `Referer` for a beacon POST is `https://sabermatic.dev/...` (the SPA page that fired the beacon), which is wrong for source attribution. The browser's `document.referrer` is the only place the *external* referrer is available, and only at page-load time — that's why the frontend SDK includes it in the POST body.

Implementation: beacon handler explicitly constructs the emission attributes from body fields (referrer/UTM/properties) plus context fields (visitor_id, user_id, trace_id) — does not rely on the middleware's referer/UTM stashing for beacon events.

Calls `b.events.Emit(ctx, name, attrs...)`. Returns 204 No Content. Reject unknown `event_name` with 400 Bad Request.

#### New: `web/src/lib/analytics.ts`

Frontend SDK uses a discriminated union so each event's required properties are type-checked:

```ts
type TrackArgs =
  | { event: 'landing_view' }
  | { event: 'signup_started', props: { auth_method: 'password' | 'google' | 'github' } };

export function track(args: TrackArgs): void {
  // Build payload:
  //   { event_name, referrer: document.referrer, utm_*: from URLSearchParams, properties: args.props }
  // Use navigator.sendBeacon if available (survives page-unload), fall back to fetch with keepalive
  // Fire-and-forget; never throw
}
```

Call sites:
- `web/src/pages/landing/index.tsx` — `useEffect(() => track({event: 'landing_view'}), [])`
- `web/src/pages/auth/signup.tsx` — `track({event: 'signup_started', props: {auth_method: 'password'}})` on form submit; same for OAuth buttons with `'google'`/`'github'`

#### Modified: backend handlers and RPC servers

Inject `*events.Emitter` into both `Backend` (for backend method emissions) and `internal/rpc/sample/Server` (for sample handler emissions). Constructor signature changes in both. Emit events at success boundaries (line numbers omitted — function names are stable, line numbers shift):

Note: `(*sample.Server).GetSampleSession` currently has signature `(_ context.Context, _ *connect.Request[...])` — it ignores ctx. Implementation must change the signature to use ctx for visitor_id lookup. (Also: emit at the *inner* methods for sessions — `CompleteSession` and `CancelSession` — not the `Wait*` wrappers, so the event emits regardless of which entry path the caller used.)

| Handler | Event | Properties |
|---|---|---|
| `(*sample.Server).GetSampleSession` (`internal/rpc/sample/server.go`) | `sample_view` | — |
| `Backend.Signup` (`internal/backend/auth.go`) | `signup_completed` | `auth_method=password`, `new_user_id` |
| `Backend.OAuthLogin` (`internal/backend/oauth.go`) — emit before returning success | `oauth_completed` | `auth_method=google`/`github`, `new_user_id`, `is_new_user` (true when internal `path == pathNewUser`), `is_reactivated` (true when `path == pathReactivated`) |
| `Backend.VerifyEmail` (`internal/backend/auth.go`) | `email_verified` | — |
| `Backend.CreateSession` (`internal/backend/session.go`) | `session_created` | `session_id`, `question_id` |
| `Backend.ExecuteTurn` (`internal/backend/turn.go`) — guard with check on existing message count | `first_message_sent` | `session_id` (only on first user turn) |
| `Backend.CompleteSession` (`internal/backend/session.go`) — inner method; called by `WaitAndCompleteSession` | `session_ended` | `session_id`, `reason=completed`, `turn_count` |
| `Backend.CancelSession` (`internal/backend/session.go`) — inner method; called by `WaitAndCancelSession` | `session_ended` | `session_id`, `reason=cancelled` |
| `internal/jobs/cleanup.go` — emit per ID returned by `CompleteAbandonedActiveSessions` | `session_ended` | `session_id`, `reason=abandoned` |
| `internal/jobs/cleanup.go` — emit per ID returned by `CancelAbandonedEmptySessions` | `session_ended` | `session_id`, `reason=abandoned_empty` |

**`is_new_user` / `is_reactivated` exposure:** `OAuthLoginResult` does not currently carry these. Emit `oauth_completed` from inside `Backend.OAuthLogin` before returning, where the `path` string constant (`pathNewUser`, `pathReactivated`, etc.) is in scope. Keeps the result struct clean and emits at the source where the truth lives. (The two flags are kept separate rather than collapsed into "new" — reactivation is product-meaningfully different from a brand-new signup; let the queries decide whether to count them together.)

**`session_ended` rationale:** there are four terminal paths for sessions (user-completed, user-cancelled, maintenance-cleanup of abandoned-with-messages, maintenance-cleanup of abandoned-empty). Using one event name with a `reason` property keeps funnel queries simple while preserving the distinction. Funnel queries that count "real completions" filter on `reason='completed'`; activation queries that count "any end state on a session that had messages" filter on `reason IN ('completed', 'cancelled', 'abandoned')` (excluding `abandoned_empty`); analyses of "users who started a session but never sent a message" use `reason='abandoned_empty'`.

A fifth potential terminal path exists in code: `Backend.FailSession` (`internal/backend/session.go:361`) for platform-error session failures. It currently has no production callers; **out of scope** for this spec. If it gains callers, add a row to this table for `reason=failed`.

**Cleanup-job emission detail:** the cleanup worker (`internal/jobs/cleanup.go`) calls TWO terminal SQL methods and gets back `[]uuid.UUID` from each:
- `q.CompleteAbandonedActiveSessions(ctx)` → IDs of sessions that had messages but were inactive past the threshold
- `q.CancelAbandonedEmptySessions(ctx)` → IDs of sessions created but never had a candidate message

Emit `session_ended` per ID from each list with the appropriate `reason`. The cleanup worker is a maintenance job with no per-session visitor/user context. To attach `user_id` to events, either (a) extend the SQL queries to RETURN session_id + user_id, or (b) issue a follow-up `SELECT user_id FROM sessions WHERE id = ANY(...)` after the cleanup. Option (a) is cleaner — modify `sql/queries/sessions.sql` and regenerate sqlc.

#### New: `terraform/analytics.tf`

| Resource | Purpose |
|---|---|
| `google_bigquery_dataset.analytics` | Dataset `sabermatic_analytics`, region matches Cloud Run, no default table expiration |
| `google_logging_project_sink.analytics` | Filter: `resource.type="cloud_run_revision" AND resource.labels.service_name="sabermatic" AND jsonPayload.analytics_event="true"`. Destination: the BQ dataset. `unique_writer_identity = true`. `bigquery_options.use_partitioned_tables = true`. |
| `google_bigquery_dataset_iam_member.sink_writer` | Grant the sink's `writer_identity` `roles/bigquery.dataEditor` on the dataset |
| `google_bigquery_table.analytics_events_view` | View `analytics_events` defined by the SELECT in the previous section. `depends_on = [google_bigquery_dataset.analytics]` is sufficient — BQ permits view creation against a non-existent table; queries return errors until the sink writes the first row. |

**Sink filter caveat:** Cloud Logging filter syntax compares strings; slog writes booleans as `true`/`false` JSON. Empirically the filter expression `jsonPayload.analytics_event="true"` works because Cloud Logging coerces bool to string in filters. Verify during smoke test by checking the sink's exported entries.

**Trace correlation:** Cloud Logging's built-in `trace` field carries `projects/<project>/traces/<trace_id>` automatically when the slog handler is wired with the OTel context. The view extracts the trace_id from this field. We do not separately stamp `trace_id` in the slog payload.

### Read-time queries (validation)

All queries reference the `analytics_events` view (not the raw table). Schema covers them all:

1. **Hourly traffic by event** — `event_time`, `event_name`, `visitor_id`
2. **Source attribution** (HN vs. Twitter vs. organic) — first-touch join on `visitor_id` to the `landing_view` row's `referer`/`utm_source`
3. **Funnel conversion** — single SELECT with COUNTIF per `event_name`
4. **Time-to-first-session** — APPROX_QUANTILES on TIMESTAMP_DIFF between `landing_view.event_time` and `first_message_sent.event_time` per `visitor_id`
5. **Auth method preference & activation** — `properties.auth_method` (direct RECORD field access; `JSON_VALUE(properties, '$.auth_method')` as fallback only if BQ types `properties` as JSON instead of RECORD)
6. **Sample-page impact** — visitors who saw `sample_view` vs. those who didn't, signup conversion
7. **Power users** — `COUNT(DISTINCT session_id)` grouped by `user_id`, filtered to `event_name='session_ended' AND properties.reason='completed'`

### Testing

- Unit tests on `events.Emitter` — recording slog handler verifies output shape
- Unit tests on `AnalyticsContextMiddleware` — visitor_id cookie set on first hit, reused on second; UTM params captured from query
- Integration test on `/api/beacon` — known event names accepted, unknown rejected with 400
- Manual smoke after deploy: hit landing, check Cloud Logging for the `analytics_event=true` line; check BQ table for the row a few minutes later

### Risk

**Application emission** — low. Fire-and-forget, fails closed; no impact on user requests if downstream fails.

**Cloud Logging stdout throughput per instance** — medium. Cloud Run forwards ~10 MB/s per instance to Cloud Logging via stdout. The app already produces structured logs (request logs, OTel spans, AI errors, River worker logs); analytics events add one extra ~500-byte line per event. At HN-spike scale (~10K events/day across the cluster) this is trivial. Risk: if existing log volume during the spike approaches the per-instance ingest cap, log lines (analytics included) may be rate-limited and dropped. Accept the risk; backfill from Cloud Logging's `_Default` bucket if drops are detected (see below).

**Logs Router sink failures** — medium. Sinks are best-effort with no retry/replay guarantees. If a BQ write fails (quota, IAM blip, schema issue), the event is silently dropped from the sink stream. **The original LogEntry remains in Cloud Logging's `_Default` bucket** for the bucket's retention period (30 days by default). Recovery is a manual one-shot script: query the `_Default` bucket and write missing rows to BQ. Document this in the post-spike runbook; don't build the recovery script preemptively.

**Schema drift from sink to view** — low (after this spec's redesign). The view depends on `jsonPayload.<field>` paths. New event types adding new properties keep working. Renaming/removing emitted fields breaks the view; track fields like a contract.

**Pre-flight sanity checks** to do before the Show HN window:
1. Cloud Logging write quota for the project ≥ 100 KB/s (well under default).
2. BQ Storage Write API quota ≥ 10 MB/s (well under default; Logs Router uses this internally).
3. After deploy, manually fire `track({event: 'landing_view'})` from the browser — confirm row visible via `SELECT * FROM sabermatic_analytics.analytics_events ORDER BY event_time DESC LIMIT 1` within 5 minutes.
4. Confirm `properties` column queryable via `JSON_VALUE` (or direct field access if RECORD).

---

## Workstream 4: Trust pages + SEO basics

### Pages to add

| Route | File | Replaces |
|---|---|---|
| `/about` | `web/src/pages/about.tsx` | Existing route renders `<Landing />` — replace with real about page |
| `/terms` | `web/src/pages/terms.tsx` | New route |
| `/privacy` | `web/src/pages/privacy.tsx` | New route |

Add to `web/src/App.tsx` route table. Add footer links on the public layout (check if a public footer exists; if not, add one in `web/src/components/public-footer.tsx` and mount in landing/sample/auth layouts).

### Static assets

| File | Content |
|---|---|
| `web/index.html` | Add `<meta name="description" content="Practice system design interviews with an AI interviewer. Honest feedback on how you did.">` |
| `web/public/robots.txt` | Allow all crawlers; reference sitemap |
| `web/public/sitemap.xml` | List public content URLs only: `/`, `/sample`, `/about`, `/terms`, `/privacy`. **Exclude** `/login` and `/signup` — auth pages add no SEO value and shouldn't rank. Add `<meta name="robots" content="noindex">` to those pages while you're at it. |

### Decisions for user to confirm during spec review (gate W4 implementation)

These three answers must be in hand before W4 implementation starts. Each blocks publishing the corresponding section.

- [ ] **Refund policy** (drafted into Terms below): subscriptions cancellable anytime with no refund for time paid; minute packs non-refundable. Confirm or override.
- [ ] **Mailgun usage** (drafted into Privacy below as a subprocessor): config has `MAILGUN_API_KEY` defaulting to `test-key` and `MAILGUN_DOMAIN` to `localhost`; prod Terraform passes a real secret. **Confirm prod actually sends mail via Mailgun**, not via a different provider or a no-op. If different, update the privacy subprocessor list.
- [ ] **Stripe cancellation behavior** matches Terms language (mid-cycle cancel stops future charges, retains access through period end). Verify against your Stripe configuration. If your Stripe is set to immediate-cancel-and-prorate, the language must be updated.

### Privacy-policy known gap

The privacy text below claims that account deletion (Settings → Delete Account) "removes your account and associated data." **This is not currently true.** `Backend.DeleteAccount` performs a soft delete: `users.deleted_at = NOW()` plus auth_session wipe. Sessions, messages, evaluations, audio in GCS, OAuth links, billing rows, and llm_calls are retained.

Two options for resolving this gap:
1. **Honest privacy text** (drafted below) — say what's actually true: account deletion revokes access; content is retained until manual deletion (or follow-up implementation of cascading delete). Lower engineering cost, slightly worse user perception.
2. **Implement real cascading delete before launch** — extend `Backend.DeleteAccount` to delete sessions, messages, evaluations, audio (via GCS API), OAuth accounts, ledger entries, llm_calls, etc. Higher engineering cost; not in scope of this spec but could be a sibling spec.

**Recommendation: ship option 1 for Show HN, plan a follow-up spec for option 2.** Don't ship privacy text that doesn't match code.

The drafted Privacy text in Appendix A reflects option 1.

### Risk

Low. New pages, no behavior changes to existing flows.

---

## Implementation order

Workstreams are mostly independent, but ordering matters for safety:

1. **Workstream 1 (DB cleanup)** — must land before Workstream 2 (the new cap depends on the right-sized pool to stay under 100 conns). Includes the AI-worker tx audit gate.
2. **Workstream 4 (Trust pages)** — independent of 1/2/3 codewise; **gated on user confirming the three open decisions in W4 (refund, Mailgun, Stripe cancel behavior)** before drafting goes live.
3. **Workstream 3 (Analytics)** — independent of 1/2/4; biggest scope, schedule first if you want measurement during early ramp-up traffic.
4. **Workstream 2 (Cloud Run cap)** — last; depends on Workstream 1 being deployed and the Cloud SQL connections metric showing stable pool usage under nominal load.

## Success criteria

Before posting Show HN:

- [ ] `DATABASE_MAX_POOL_SIZE` defaults to 25; `sql/queries/advisory_locks.sql` deleted; `sqlc generate` produces no diff (`sqlc diff` exits 0); generated `internal/db/advisory_locks.sql.go` and querier methods removed; `go build ./...` passes; `make test` passes
- [ ] `SHOW max_connections;` against prod Cloud SQL recorded in W1 (verifies 100 vs 50 — gates W2 sizing)
- [ ] `terraform plan` shows max_instance_count=3 and the new analytics resources, no other unintended drift
- [ ] `/api/beacon` returns 204 for `landing_view` and `signup_started`; rejects unknown event names with 400
- [ ] Manual `track({event: 'landing_view'})` from browser → row visible in `analytics_events` view within 5 minutes
- [ ] `properties` field queryable: `SELECT properties.auth_method FROM analytics_events WHERE event_name='oauth_completed' LIMIT 1` returns a value (after a manual oauth signup test). If RECORD typing is not what BQ chose, fall back to `JSON_VALUE(properties, '$.auth_method')` and update the view to cast properties accordingly.
- [ ] `/about`, `/terms`, `/privacy` render with real content; linked in footer; meta description present in `index.html` source view
- [ ] `robots.txt` and `sitemap.xml` resolve; `/login` and `/signup` carry `<meta name="robots" content="noindex">`
- [ ] `terraform apply` clean against production

After posting Show HN, validate within 1 hour:

- [ ] Funnel query (Q3) returns non-zero numbers across all stages
- [ ] Source attribution query (Q2) shows traffic split
- [ ] No 5xx error spike in Cloud Run logs
- [ ] Cloud SQL "current connections" metric stays comfortably under 75 across the cluster (the pool=25 × 3 instances ceiling); under ~25 per instance steady-state means we have headroom
- [ ] If event drops detected: backfill from Cloud Logging `_Default` bucket using a one-shot script

---

## Appendix A: Trust page draft content

User explicitly requested plain, simple, honest, straightforward language. Drafts below for review during spec sign-off.

### About (`/about`)

> # About Sabermatic
>
> Sabermatic helps you practice system design interviews by talking through real problems with an AI interviewer — and getting honest feedback on how you did.
>
> It was built in 2026 during a job search, as a personal practice tool. After it became useful enough to recommend, it became a product. Billing exists to keep the service running without going broke offering it.
>
> Hope you find it helpful.
>
> ---
>
> Sabermatic is a product of Spanda, LLC.
>
> Contact: brian@spanda.llc
> Code: github.com/btc

### Privacy Policy (`/privacy`)

> # Privacy Policy
>
> Last updated: 2026-05-04
>
> Sabermatic is operated by Spanda, LLC ("we"). This policy explains what data we collect, how we use it, and the choices you have.
>
> ## What we collect
>
> When you use Sabermatic we collect:
>
> - **Account information** — your email, display name, password (hashed), and OAuth provider IDs if you sign in with Google or GitHub.
> - **Practice content** — the conversations, transcripts, audio recordings, AI evaluations, and coaching analyses generated when you use the service. Audio recordings are stored in Google Cloud Storage and remain associated with your account.
> - **Usage data** — events about how you use the product (page views, signups, session starts, session completions). These help us understand how the service is used.
> - **Technical data** — IP address, browser/device information, log data from your interactions with the service.
> - **Payment data** — if you purchase a subscription or minute pack, Stripe processes your payment. We receive transaction status and metadata; we do not receive or store your card number.
>
> ### Sign-in with Google or GitHub
>
> If you sign in using Google or GitHub, those providers receive your authentication request and share your name, email address, and provider account ID with us. Google and GitHub act as independent controllers of that data and handle it under their own privacy policies.
>
> ## How we use it
>
> We use your conversations, transcripts, and recordings to operate the service for you, investigate issues you report, and improve our prompts, scoring, and product based on what works.
>
> **We do not train AI models on your data, and we do not sell or share your data with third parties** other than the subprocessors listed below who help us operate the service.
>
> ## Subprocessors
>
> We use the following third-party services to operate Sabermatic. Each receives only the data needed to perform its function. Each operates under its own privacy policy and may retain the data it processes per its own retention rules.
>
> - **Anthropic** — runs the AI interviewer and coaching/analysis models. Your conversation content is sent to Anthropic for processing. Anthropic's standard API retention is up to 30 days for abuse monitoring before deletion.
> - **OpenAI** — converts your speech to text (Whisper) and generates the interviewer's voice (TTS). Audio and transcripts are sent to OpenAI for processing. OpenAI's standard API retention is up to 30 days for abuse monitoring before deletion.
> - **Google Cloud Platform** — hosts the service (Cloud Run, Cloud SQL, Cloud Storage, BigQuery) and runs Vertex AI / Gemini for image generation. Your account data and practice content are stored on Google Cloud infrastructure in the United States.
> - **Stripe** — processes payments. Your payment information is sent directly to Stripe.
> - **Mailgun** — sends transactional emails (verification, password reset). Your email address is sent to Mailgun for delivery.
>
> ### International transfers
>
> We process and store data on Google Cloud infrastructure in the United States. If you are in the European Economic Area, your data is transferred to the United States; we rely on Standard Contractual Clauses with our subprocessors as the legal basis for this transfer.
>
> ## Cookies
>
> We use the following cookies:
>
> - **Session cookie** (essential) — keeps you signed in. HttpOnly, SameSite=Lax.
> - **OAuth redirect cookie** (essential, transient) — remembers where to send you after signing in with Google or GitHub. Cleared after use.
> - **Visitor ID cookie** (functional) — a random identifier that lets us count unique visitors and reconstruct usage funnels. Not shared with third parties.
>
> Your theme preference is stored in your browser's local storage, not as a cookie.
>
> ## Data retention
>
> We keep your data for as long as your account is active.
>
> If you delete your account (Settings → Delete Account), we revoke access immediately and sign you out of all devices. Your account is marked deleted and can no longer be used to sign in. Practice content (transcripts, recordings, evaluations) is retained on our servers and removed on request — email brian@spanda.llc to request immediate deletion of your content.
>
> Encrypted database backups are retained for approximately 7 days before expiry.
>
> ## Your rights
>
> You have the right to access, correct, export, or delete your personal data.
>
> Account deletion is available in Settings → Delete Account; for immediate deletion of all associated content (rather than account-only), email brian@spanda.llc.
>
> To exercise other rights — including data access or export — email brian@spanda.llc. We will respond within 30 days.
>
> ## Security and breach notification
>
> We use industry-standard measures to protect your data, including encryption in transit (HTTPS), encryption at rest on Google Cloud, hashed password storage, and short-lived session tokens.
>
> If we become aware of a security breach that affects your personal data, we will notify affected users without undue delay and within 72 hours of confirming the breach where required by applicable law.
>
> ## Children
>
> Sabermatic is not intended for children under the applicable age of consent in their country (13 in the United States, and as set by member-state law in the European Economic Area — generally 13 to 16). If you are under that age, do not use the service. We do not knowingly collect data from children below these ages. If you believe a child has provided us data, please contact brian@spanda.llc and we will delete it.
>
> ## Changes to this policy
>
> If we change this policy in a way that materially affects how we handle your data, we will notify you by email before the change takes effect.
>
> ## Contact
>
> Spanda, LLC (Delaware, USA)
> brian@spanda.llc

### Terms of Service (`/terms`)

> # Terms of Service
>
> Last updated: 2026-05-04
>
> These terms govern your use of Sabermatic, a service operated by Spanda, LLC ("we"). By using the service you agree to these terms.
>
> ## The service
>
> Sabermatic is a practice tool for system design interviews. It uses AI to conduct simulated interviews and provide feedback on your responses.
>
> ## Your account
>
> You need an account to use the service. You are responsible for keeping your account credentials secure. You must provide accurate information and notify us promptly of any unauthorized use.
>
> ## Acceptable use
>
> You agree to use the service in good faith for system design learning and practice. Casual or exploratory use of the chat outside the strict interview format is fine; the AI is tuned for system design and may not be helpful for unrelated topics.
>
> You agree not to:
>
> - Use the service to cheat in a real interview, including by transmitting live interviewer questions or content to or from an interview in progress.
> - Attempt to disrupt the service, scrape data, or reverse-engineer the AI prompts or models.
> - Submit content that is illegal, infringing, or harmful, or use the service to harass others.
>
> We reserve the right to suspend or terminate accounts that violate these terms.
>
> ## Pricing and subscriptions
>
> Sabermatic offers paid plans (subscription and one-time minute packs). Current pricing is shown in the app.
>
> - **Subscriptions** can be cancelled at any time. Cancellation stops future billing; access continues through the end of the billing period for which you have paid. We do not refund time already paid.
> - **Minute packs** are non-refundable once purchased. Minutes do not expire while your account is active.
>
> Payments are processed by Stripe.
>
> ## Your content
>
> You retain ownership of the content you provide (your responses, recordings). By using the service, you grant us a non-exclusive license to process that content as needed to operate the service for you and to improve the product, as described in our Privacy Policy.
>
> The AI evaluations and coaching analyses generated by the service are made available to you for your use. The underlying prompts, models, and product are owned by us.
>
> ## Disclaimers
>
> The service is provided "as is." We do not guarantee that the AI's evaluations, coaching, or any output of the service is accurate, complete, or suitable for any particular purpose. **Sabermatic does not provide career, hiring, or professional advice. Using Sabermatic does not guarantee any interview, job offer, or employment outcome.**
>
> ## Limitation of liability
>
> To the maximum extent permitted by law, Spanda, LLC is not liable for any indirect, incidental, consequential, or punitive damages arising from your use of the service. Our total liability for any claim is limited to the greater of (a) one hundred US dollars or (b) the amount you have paid us in the 12 months before the claim.
>
> ## Termination
>
> We reserve the right to suspend or terminate your account for violation of these terms. You may terminate your account at any time via Settings → Delete Account.
>
> ## Changes to these terms
>
> We may update these terms from time to time. If we make material changes, we will notify you by email. Continued use of the service after changes take effect means you accept the updated terms.
>
> ## Governing law
>
> These terms are governed by the laws of the State of Delaware, USA, without regard to conflict-of-law rules. Any dispute will be resolved in the state or federal courts located in Delaware.
>
> ## Contact
>
> Spanda, LLC
> brian@spanda.llc
