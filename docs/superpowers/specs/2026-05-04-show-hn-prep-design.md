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

There's also a vestigial liability we want to clear: the DB connection pool was sized for a now-removed advisory-lock architecture, leaving us with 80 connections per instance × 2 instances = 160 connections targeted at a Cloud SQL instance that allows ~50. The cluster only survives because it's never actually scaled to 2 instances.

## Out of scope

- ClickHouse or any new analytics infra (Cloud Trace + BigQuery sink covers it)
- Third-party analytics SDKs (PostHog, Plausible, GA4 — explicitly rejected; we own the data)
- A "report a bug" feedback channel on the landing page
- Real-time analytics dashboards (BQ is post-hoc only)
- Renaming the `drill_session` cookie to `sabermatic_session` (boy-scout note for follow-up)
- Geographic analytics (`ip_country` enrichment)
- Building UI for GDPR data export (manual fulfillment via psql/GCS within 30 days is acceptable; UI is a follow-up spec)
- Lawyer review of terms/privacy text (drafting plain-English versions ourselves)

---

## Workstream 1: DB connection pool right-sizing

### Problem

`internal/config/config.go:52` defaults `DATABASE_MAX_POOL_SIZE` to 80, with a comment justifying it as "each active session pins a connection for its advisory lock." The advisory-lock architecture is gone — production code has zero callers of `PGTryAdvisoryLock` / `PGAdvisoryUnlock`. The pool is sized for an architecture that no longer exists, while Cloud SQL's `db-g1-small` tier caps at ~50 `max_connections`.

### Design

Reduce default to `15` per instance. Math: 15 × max 3 instances = 45, fits under 50 with 5 reserved for psql/migrations/superuser. Sized for River workers (24 max workers, mostly idle on external API calls — realistic peak DB concurrency ~5–8) plus headroom for HTTP request bursts.

Delete the dead advisory-lock SQL source; let `sqlc generate` remove the generated artifacts.

### Components

| File | Change |
|---|---|
| `internal/config/config.go:52` | `default=80` → `default=15`; rewrite comment to reflect new sizing rationale (no mention of advisory locks) |
| `sql/queries/advisory_locks.sql` | Delete |
| `internal/db/advisory_locks.sql.go` | Auto-deleted by `sqlc generate` |
| `internal/db/querier.go` | `PGTryAdvisoryLock` and `PGAdvisoryUnlock` methods auto-removed by `sqlc generate` |

### Testing

- `go build ./...` — compile-time verification that nothing references the removed methods
- `make test` — full CI suite
- No new tests; this removes dead code

### Risk

Near-zero. Pure deletion of unreferenced code + a smaller default value. Pgx multiplexes hundreds of concurrent requests over small pools fine; queries are short. Rollback is the env override `DATABASE_MAX_POOL_SIZE=80`, no code change.

---

## Workstream 2: Cloud Run instance cap bump

### Problem

`terraform/cloud_run.tf:10` sets `max_instance_count = 2`. With 100 concurrent requests per instance, that's a ceiling of 200 concurrent in-flight requests — under HN front-page hug-of-death range.

### Design

Bump to `3`. Bounded by Workstream 1's pool sizing: 15 × 3 = 45 < 50 max_connections.

At 3 instances × 100 concurrent × ~200ms requests = ~1500 RPS sustained ceiling. Comfortable for realistic HN spike (5–50 RPS sustained, occasional bursts).

### Components

| File | Change |
|---|---|
| `terraform/cloud_run.tf:10` | `max_instance_count = 3` (was 2) |

### Testing

`terraform plan` shows the diff; `terraform apply` after the spec is approved and Workstream 1 is shipped (Workstream 1 must land first or the cluster will exceed Cloud SQL caps under load).

### Risk

Low. `min_instance_count` stays at default (0); we accept 1–2 second cold start per instance over paying for an always-warm instance during the Show HN window.

### Open follow-ups (not in this spec)

- If the spike exceeds capacity at 3 instances, the next bump requires either dropping pool to 10 (4 instances × 10 = 40) or upgrading Cloud SQL tier to `db-custom-1-3840` (~100 max_connections, +~$25/mo). Defer the decision.

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
BigQuery dataset `sabermatic_analytics`, table `analytics_events` (date-partitioned, clustered)
```

Hot path is fire-and-forget: emission is one slog write. Cloud Logging buffers/retries to BQ. App is unaware of BQ availability.

### Event taxonomy

Single events table. One row per event. Funnel queries: `WHERE event_name IN (...)`.

| Event | Emitted from | Identifies |
|---|---|---|
| `landing_view` | Frontend beacon (`POST /api/beacon`) on landing mount | visitor |
| `sample_view` | `Backend.GetSampleSession` success | visitor |
| `signup_started` | Frontend beacon when user clicks "Sign up" / "Continue with Google" | visitor |
| `signup_completed` | `Backend.Signup` success | visitor + new user_id |
| `oauth_completed` | OAuth callback success (`internal/handler/oauth.go`) | visitor + user_id |
| `email_verified` | `Backend.VerifyEmail` success | user_id |
| `session_created` | `Backend.CreateSession` success | user_id + session_id |
| `first_message_sent` | `Backend.ExecuteTurn` first turn for a session | user_id + session_id |
| `session_completed` | Session end-state transition | user_id + session_id |

### BigQuery table schema

Pre-created (not auto-evolved) in Terraform. Date-partitioned on `event_time`, clustered on `event_name, visitor_id`.

| Column | Type | Mode | Notes |
|---|---|---|---|
| `event_id` | STRING | REQUIRED | UUID per event, dedup key |
| `event_name` | STRING | REQUIRED | Cluster key |
| `event_time` | TIMESTAMP | REQUIRED | Partition key |
| `visitor_id` | STRING | REQUIRED | Cluster key, first-party cookie |
| `user_id` | STRING | NULLABLE | Set after auth |
| `session_id` | STRING | NULLABLE | Promoted to top-level for power-user queries |
| `trace_id` | STRING | NULLABLE | Join key to Cloud Trace |
| `referer` | STRING | NULLABLE | **Populated only on `landing_view`** (first-touch attribution) |
| `utm_source` | STRING | NULLABLE | Same — landing_view only |
| `utm_medium` | STRING | NULLABLE | Same |
| `utm_campaign` | STRING | NULLABLE | Same |
| `path` | STRING | NULLABLE | URL path on emission |
| `user_agent` | STRING | NULLABLE | |
| `properties` | JSON | NULLABLE | Event-specific extras (e.g., `auth_method`, raw `new_user_id`) |

Funnel attribution semantic: `referer` and `utm_*` are stamped only on `landing_view`. Other events join back to landing_view by `visitor_id` for first-touch source. (No mutable cookie state, no per-event referer pollution.)

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
- Captures `Referer` header.
- Parses `utm_source`, `utm_medium`, `utm_campaign` from URL query string.
- Stashes one `events.ContextValues` struct in `ctx`.

Mount on the public router (so it runs before any handler — including the unauthenticated landing/sample paths).

The existing auth middleware at `internal/handler/middleware.go:51` already attaches `user_id` to spans; extend it to also call `events.WithUserID(ctx, userID)`.

#### New: `internal/handler/beacon.go`

`POST /api/beacon` handler.

Request body: `{event_name: string, properties: map[string]any}`.

Validates `event_name` against an allowlist (`landing_view`, `signup_started` only — backend events go through their own handlers, not this beacon).

Calls `b.events.Emit(ctx, name, mapToAttrs(props)...)`. Returns 204 No Content.

#### New: `web/src/lib/analytics.ts`

Frontend SDK:

```ts
export function track(event: 'landing_view' | 'signup_started', props?: Record<string, unknown>): void {
  // Build payload with referer (document.referrer), utm_* (URLSearchParams), event_name, properties
  // Use navigator.sendBeacon if available (survives page-unload), fall back to fetch with keepalive
  // Fire-and-forget; never throw
}
```

Call sites:
- `web/src/pages/landing/index.tsx` — `useEffect(() => track('landing_view'), [])`
- `web/src/pages/auth/signup.tsx` — `track('signup_started', {auth_method: 'password'})` on form submit; same for OAuth buttons with appropriate `auth_method`

#### Modified: backend handlers

Inject `*events.Emitter` into `Backend` (constructor signature change). Emit events at success boundaries:

| Handler | Event | Properties |
|---|---|---|
| `Backend.GetSampleSession` (sample.go) | `sample_view` | — |
| `Backend.Signup` (auth.go:110) | `signup_completed` | `auth_method=password`, `new_user_id` |
| OAuth callback (`internal/handler/oauth.go`) | `oauth_completed` | `auth_method=google`/`github`, `new_user_id`, `is_new_user` |
| `Backend.VerifyEmail` (auth.go:275) | `email_verified` | — |
| `Backend.CreateSession` (session.go:36) | `session_created` | `session_id`, `question_id` |
| `Backend.ExecuteTurn` (turn.go:65) | `first_message_sent` | `session_id` (only on first user turn — guard with a check on existing message count) |
| Session end-state transition | `session_completed` | `session_id`, `message_count`, `duration_seconds` |

**Implementation note for `session_completed`:** the codebase doesn't have a single obvious "session ended" emission point today (sessions transition via the conductor / River jobs). Implementation step: locate the state transition that marks a session as terminal (likely in `internal/backend/session.go` or a River job) and emit there. If no clean hook exists, add one. This is the only event whose emission point requires investigation; all others map to existing handler success paths.

#### New: `terraform/analytics.tf`

| Resource | Purpose |
|---|---|
| `google_bigquery_dataset.analytics` | Dataset `sabermatic_analytics`, region matches Cloud Run, no default table expiration |
| `google_bigquery_table.analytics_events` | Pre-created table with explicit schema (above), partitioned on `event_time` (DAY), clustered on `event_name, visitor_id` |
| `google_logging_project_sink.analytics` | Filter: `resource.type="cloud_run_revision" AND resource.labels.service_name="sabermatic" AND jsonPayload.analytics_event=true`. Destination: BQ dataset. `unique_writer_identity = true`. |
| `google_bigquery_dataset_iam_member.sink_writer` | Grant the sink's `writer_identity` `roles/bigquery.dataEditor` on the dataset |

Schema field mapping caveat: Logs Router with BQ destination writes `jsonPayload.<field>` as top-level columns. Verify during implementation that slog attribute names map cleanly. Adjust slog field names if needed to match the BQ schema column names exactly.

### Read-time queries (validation)

Spec is validated against these queries — schema covers them all:

1. **Hourly traffic by event** — partition + cluster keys
2. **Source attribution** (HN vs. Twitter vs. organic) — first-touch join via `visitor_id`
3. **Funnel conversion** — single SELECT with COUNTIF per step
4. **Time-to-first-session** — APPROX_QUANTILES on TIMESTAMP_DIFF
5. **Auth method preference & activation** — JSON_VALUE(properties, '$.auth_method')
6. **Sample-page impact** — visitors who saw `sample_view` vs. those who didn't, signup rate
7. **Power users** — `session_id` count grouped by `user_id`

### Testing

- Unit tests on `events.Emitter` — recording slog handler verifies output shape
- Unit tests on `AnalyticsContextMiddleware` — visitor_id cookie set on first hit, reused on second; UTM params captured from query
- Integration test on `/api/beacon` — known event names accepted, unknown rejected with 400
- Manual smoke after deploy: hit landing, check Cloud Logging for the `analytics_event=true` line; check BQ table for the row a few minutes later

### Risk

Low for app code (fire-and-forget, fails closed). Medium for the Logs Router sink — schema mismatches between slog output and BQ table cause silent drops. Mitigate by:
1. Pre-creating the BQ table with explicit schema (forces field-name agreement)
2. Manual smoke test post-deploy before relying on the data
3. Cloud Logging retains the source events even if the sink fails — recoverable

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
| `web/public/sitemap.xml` | List public URLs: `/`, `/sample`, `/about`, `/terms`, `/privacy`, `/login`, `/signup` |

### Decisions for user to confirm during spec review

1. **Refund policy** (drafted into the Terms below): subscriptions cancellable anytime with no refund for time paid; minute packs non-refundable. Confirm or override.
2. **Subprocessor list** (drafted into the Privacy below): Anthropic, OpenAI, GCP, Stripe, Mailgun. Verify this matches actual prod usage — Mailgun in particular is configured but defaults to a test key; confirm prod really sends via Mailgun.
3. **Refund/cancellation language** in the Terms accurately reflects what your Stripe billing actually allows (e.g., is mid-cycle cancellation set up to stop future charges and retain access through period end?). This is a billing-system check; no spec change needed unless behavior diverges from the language.

### Risk

Low. New pages, no behavior changes to existing flows.

---

## Implementation order

Workstreams are mostly independent, but ordering matters for safety:

1. **Workstream 1 (DB cleanup)** — must land before Workstream 2 (otherwise scaling exceeds DB caps)
2. **Workstream 4 (Trust pages)** — independent, can land anytime; landing-page-only changes deploy fast. Gates only on user confirming the open decisions above.
3. **Workstream 3 (Analytics)** — independent of 1/2/4; biggest scope, do first if you want measurement during early traffic
4. **Workstream 2 (Cloud Run cap)** — last, gates on Workstream 1 being deployed and stable

## Success criteria

Before posting Show HN:

- [ ] `DATABASE_MAX_POOL_SIZE` defaults to 15; advisory_locks files removed; `make test` passes
- [ ] `terraform plan` shows max_instance_count=3, no other unintended drift
- [ ] `/api/beacon` returns 204 for `landing_view` and `signup_started`; rejects others
- [ ] Manual hit to landing → row visible in `sabermatic_analytics.analytics_events` BQ table within 5 minutes
- [ ] `/about`, `/terms`, `/privacy` render with real content; linked in footer; meta description in source view
- [ ] `robots.txt` and `sitemap.xml` resolve
- [ ] `terraform apply` clean against production

After posting Show HN, validate within 1 hour:

- [ ] Funnel query (Q3) returns non-zero numbers
- [ ] Source attribution query (Q2) shows traffic split
- [ ] No 5xx error spike in Cloud Run logs
- [ ] No DB connection exhaustion in Cloud SQL metrics

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
> - **Practice content** — the conversations, transcripts, audio recordings, AI evaluations, and coaching analyses generated when you use the service.
> - **Usage data** — events about how you use the product (page views, signups, session starts, session completions). These help us understand how the service is used.
> - **Technical data** — IP address, browser/device information, log data from your interactions with the service.
> - **Payment data** — if you purchase a subscription or minute pack, Stripe processes your payment. We receive transaction status and metadata; we do not receive or store your card number.
>
> ## How we use it
>
> We use your conversations, transcripts, and recordings to operate the service for you, investigate issues you report, and improve our prompts, scoring, and product based on what works.
>
> **We do not train AI models on your data, and we do not sell or share your data with third parties** other than the subprocessors listed below who help us operate the service.
>
> ## Subprocessors
>
> We use the following third-party services to operate Sabermatic. Each receives only the data needed to perform its function:
>
> - **Anthropic** — runs the AI interviewer and coaching/analysis models. Your conversation content is sent to Anthropic for processing.
> - **OpenAI** — converts your speech to text (Whisper) and generates the interviewer's voice (TTS). Audio and transcripts are sent to OpenAI for processing.
> - **Google Cloud Platform** — hosts the service (Cloud Run, Cloud SQL, Cloud Storage, BigQuery) and runs Vertex AI / Gemini for image generation. Your account data and practice content are stored on Google Cloud infrastructure.
> - **Stripe** — processes payments. Your payment information is sent directly to Stripe.
> - **Mailgun** — sends transactional emails (verification, password reset). Your email address is sent to Mailgun for delivery.
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
> We keep your data for as long as your account is active. If you delete your account (Settings → Delete Account), we delete your account and associated data.
>
> ## Your rights
>
> You have the right to access, correct, export, or delete your personal data.
>
> Account deletion is available in Settings → Delete Account and removes your account and associated data.
>
> To exercise other rights — including data export — email brian@spanda.llc. We will respond within 30 days.
>
> ## Children
>
> Sabermatic is not intended for children under 13 (or under 16 in the European Economic Area). We do not knowingly collect data from children below these ages. If you believe a child has provided us data, please contact brian@spanda.llc and we will delete it.
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
> You agree to use the service only for system design interview practice and related learning. You agree not to:
>
> - Use the service to cheat in a real interview, including by transmitting interviewer questions or content to or from a real interview in progress.
> - Use the chat for any purpose other than system design interview practice.
> - Attempt to disrupt the service, scrape data, or reverse-engineer the AI prompts or models.
> - Submit content that is illegal, infringing, or harmful.
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
> To the maximum extent permitted by law, Spanda, LLC is not liable for any indirect, incidental, consequential, or punitive damages arising from your use of the service. Our total liability for any claim is limited to the amount you have paid us in the 12 months before the claim.
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
