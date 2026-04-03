# Growth, Landing Page & Launch Design Spec

## Overview

This spec covers the public-facing landing page, conversion funnel, session replay feature, sample session, and launch strategy for Sabermetric. It builds on the existing naming/branding spec and the authenticated UI spec.

**Note:** The UI spec and codebase use "Drill" as the internal project codename. "Sabermetric" is the user-facing product name. No need to update existing specs or code to say "Sabermetric" — the rename applies to user-visible UI text (wordmark, browser tab, meta tags) at implementation time.

The landing page is a single-scroll showcase that walks visitors through a real evaluated session (v0 session 27), demonstrating every major feature with real data. The goal: share something genuinely useful, let people see what it does, and not go broke running it.

## Conversion Funnel

1. Visitor lands on `sabermetric.dev` (from Show HN, Product Hunt, search, referral)
2. Scrolls through animated feature sections — real data from session 27
3. Optionally clicks "See a real evaluation" → `/sample` → full session replay
4. Clicks CTA "Start practicing" → `/signup` (Google/GitHub OAuth or email)
5. Lands on home dashboard → free tier, picks a question, starts first session
6. After 3+ reviewed sessions → coach card appears on home (UI progressive disclosure). On free tier, the coach card renders as an upgrade prompt ("Get strategic coaching — available on Pro") since coach access requires paid balance (see billing spec). On paid tier, the card shows the full coach analysis.

**No anonymous sessions.** Signup required. Free tier is the trial. Simpler engineering (no session migration, no IP-based rate limiting, no orphaned data), cleaner usage tracking, better conversion (the person who won't do one-click OAuth wasn't converting anyway).

**Authenticated users** who visit `sabermetric.dev` are redirected to `/` (home dashboard). They never see the landing page again.

### Routing

The landing page lives at `/`. The React Router configuration conditionally renders based on auth state:
- **Unauthenticated:** `/` renders the landing page. The existing `RequireAuth` guard (which redirects to `/login`) does NOT apply to `/` — instead, `/` itself handles both states.
- **Authenticated:** `/` renders the home dashboard (existing design).

The `/sample` route is a new public route (no auth required). It renders the standard session detail UI (overview, transcript, deep dive tabs) with pre-seeded session 27 data. The session detail component must support a "public mode" that reads from bundled/static data rather than calling authenticated API endpoints.

These routes extend the UI spec's routing table:

| Route | Auth | Description |
|---|---|---|
| `/` | conditional | Landing page (unauth) or home dashboard (auth) |
| `/sample` | public | Sample session detail with replay |

## Landing Page

**URL:** `sabermetric.dev` (unauthenticated visitors only)
**Tech:** same React app, public route. No separate marketing site.
**Animation:** scroll-triggered reveals (fade + slide up as sections enter viewport). No scroll hijacking.

### SEO & Social Previews

The landing page (`/`) and sample session (`/sample`) are the two public routes that will be shared on HN, Product Hunt, Slack, Twitter, etc. The SPA serves a single `index.html` from `embed.FS`, which returns empty HTML to social crawlers.

**Solution:** The Go backend injects OpenGraph and meta tags into `index.html` before serving for these two routes. A simple template approach — the Go handler detects `/` or `/sample`, injects route-specific `<meta>` tags (og:title, og:description, og:image) into the HTML `<head>`, then serves it. All other routes serve the unmodified `index.html`.

- **`/` (landing page):** og:title "Sabermetric", og:description "data-driven system design prep", og:image a static preview image of the scoring section
- **`/sample`:** og:title "Sabermetric — sample evaluation", og:description "See a real system design interview evaluated across 5 dimensions", og:image a static preview image of the session overview

### Section 1: Hero

- "sabermetric" lowercase wordmark, large
- Rotating tagline (typewriter/fade, ~3s per line, holds on final):
  1. "system design, measured."
  2. "measure what matters."
  3. "practice with precision."
  4. "the science of system design prep."
  5. "data-driven system design prep." ← holds
- "Start practicing" primary amber CTA
- Warm stone background, minimal, confident

### Section 2: Scoring

- Feature copy: what the scoring system is and why it matters (multi-dimensional, rigorous, no pass/fail)
- Illustration: session 27's real scores — five dimension bars animate from 0 to their values, overall score lands last
- Dimension labels: Requirements, Architecture, Deep Dive, Scalability, Communication
- Session 27's question title shown here to establish context for the walkthrough

### Section 3: Strengths, Gaps, Advice

- Feature copy: the evaluator breaks down your performance into specific strengths, gaps, and actionable advice
- Illustration: session 27's real evaluator output — green strength cards fade in, then orange gap cards, then advice narrative
- Real excerpts, not placeholders

### Section 4: Annotations

- Feature copy: feedback is grounded in what you actually said, tied to specific moments in your conversation
- Illustration: a short transcript snippet (3-5 messages) from session 27 with real annotation callouts appearing one by one
- Green (strength), orange (gap), violet (missed opportunity), blue (note) — whichever annotation types are present in the selected session 27 excerpt. Show only the types that appear naturally; don't force all four.

### Section 5: Deep Dive

- Feature copy: after each session, learn what you should have known — model answers and gap analysis with real-world examples
- Illustration: real educator output from session 27 — model answer excerpt with architecture and code, then a gap analysis showing how this works at a real company

### Section 6: Coaching

- Feature copy: tracks your growth across sessions, identifies thinking patterns, recommends what to practice next
- Illustration: real coach output from the v0 user who did session 27 — trend sparkline, weakest dimension badge, recommended next question. Coach data is user-level (not session-level), so this uses that user's full coaching history.

### Section 7: Voice → Transcript → Analysis

- Feature copy: speak naturally, the system handles the rest — live voice interviews, transcribed and analyzed
- Illustration: audio waveform pulses, resolves into a transcript line, then annotation markers land on it — three stages, one continuous animation
- Conveys the full pipeline: voice in → transcription → multi-layered analysis

### Section 8: Sample Session

- "See a real evaluation" — brief context (question title, duration, score preview)
- "View full session" link navigates to `/sample`, which renders the real session detail UI with session 27 data and audio replay

### Section 9: Credits

Clean, quiet list at the bottom. No logos, no "powered by" badges. Just what's inside.

- Interviewer — Claude Sonnet
- Evaluator — Claude Opus
- Educator — Claude Opus
- Coach — Claude Sonnet
- Speech-to-text — Whisper
- Text-to-speech — OpenAI TTS
- Backend — Go on Google Cloud Run
- Database — PostgreSQL
- Payments — Stripe

### Section 10: CTA Repeat

- "Start practicing" primary amber CTA
- "No credit card required" muted text below (accurate — free tier is 60 min/month with no payment info needed; limits are communicated inside the app, not on the landing page)

## Session Replay Feature

A general product feature available on any reviewed session and on the sample session.

### Mechanic

- "Replay" button on session detail page (transcript tab)
- Plays back the interview in real time with speed controls (1x, 1.5x, 2x)
- Audio plays for both interviewer (TTS) and candidate (recorded) segments
- Transcript messages appear in sync with audio playback
- Annotations reveal as their associated message plays
- Seekable timeline scrubber — jump to any point
- Play/pause controls

### Data Requirements Per Session

- Transcript messages with `created_at` timestamps (already stored). Replay timing derived from offsets relative to `session.started_at`.
- Audio segments (candidate recordings + interviewer TTS) stored in GCS with `audio_url` on each message.
- Annotations mapped to message `seq` indices (already stored).

**Dependency:** The conductor spec currently defers audio storage (GCS upload). Session replay requires audio persistence to be implemented first. For the sample session (v0 session 27), audio files already exist in v0 storage and can be migrated as static assets independently of the v1 audio pipeline.

**Schema note:** No new columns needed for replay timing — `messages.created_at` relative to `sessions.started_at` provides message-level timing. Per-segment audio duration can be derived from the audio files themselves at playback time.

### Text-Only Fallback

Sessions without preserved audio still get transcript replay — messages appear in timed sequence without audio playback.

## Sample Session

**URL:** `/sample`
**Data source:** v0 session 27 (all data present: transcript, evaluation, educator deep dive, coach analysis, candidate audio files, interviewer audio files)

Renders the real session detail UI (overview, transcript, deep dive tabs) with session 27 data. Replay auto-prompts on the transcript tab. Not a marketing mockup — the actual product UI with real data.

### Data Packaging

The sample session data is bundled as static JSON fixtures served by the Go backend at a public (no-auth) endpoint:

Three endpoints mirroring the authenticated session detail pattern:

- **`GET /api/sample/session`** — returns session metadata and transcript messages. Same response shape as `GET /api/sessions/:id`.
- **`GET /api/sample/evaluation`** — returns scores, strengths, gaps, advice, and annotations. Same response shape as `GET /api/sessions/:id/evaluation`.
- **`GET /api/sample/educator`** — returns educator deep dive content. Same response shape as `GET /api/sessions/:id/educator`.

One additional endpoint for the landing page:

- **`GET /api/sample/coach`** — returns coach analysis data (weakest dimension, improving dimensions, topic gaps, suggested question, narrative) for the landing page coaching section (Section 6). Not used by the `/sample` session detail page — coach data is user-level, not session-level. Does NOT include trend sparkline data — the sparkline is computed client-side from evaluation scores across sessions. For the landing page illustration, the sparkline uses pre-computed score data bundled in the session fixture (an array of `{date, overall_score}` pairs from the v0 user's session history).

The JSON fixtures are generated once from v0 database exports and committed to the repo (e.g., `internal/sample/session.json`, `internal/sample/coach.json`). The Go handler serves them directly — no database queries.

**Audio files:** Migrated from v0 storage to GCS with public-read ACLs, or served as static assets from the Go binary. The `audio_url` fields in the fixture JSON point to these locations.

**Frontend integration:** The session detail component receives a `dataSource` prop — either `"api"` (authenticated, fetches from `/api/sessions/:id/*`) or `"sample"` (public, fetches from `/api/sample/*`). Same component, same rendering, same fetch pattern (3 parallel calls), different base URL. The landing page sections also read from the sample endpoints for their illustrations.

## Show HN Launch Strategy

### Post Format

```
Show HN: Sabermetric – system design interview practice with real scoring

I built this for myself. I've been prepping for system design interviews
and wanted practice that actually measured what matters and tightened the
loop between doing a session and knowing exactly what to work on next.

It's a voice conversation with an AI interviewer, then separate AI roles
score you across 5 dimensions, annotate your transcript with specific
strengths and gaps, and generate a deep dive on what you should have
known — grounded in how real systems work at real companies.

Think Moneyball for interview prep — find the 2% you're missing instead
of grinding the same generic questions.

Stack: Go on Cloud Run, PostgreSQL, various LLMs handling different
parts (interviewing, evaluation, education, coaching), Whisper for
STT, OpenAI TTS for voice responses.

You can explore a real evaluated session here: [sample link]

It's been genuinely helpful for me so I'm sharing it. Free tier
available. There's a paid plan because LLMs are expensive and I'd
like to not go broke running it.
```

### What Appeals to HN

- "I built this for myself and it's been helpful" — not a product launch, just sharing a tool. Most credible framing on HN.
- Technical architecture as content — which models for which roles, why Go, why Postgres-backed job queue (River), sparks good comment threads. River is worth mentioning in HN comments (Go audience knows it) even if not in the post itself.
- The Moneyball framing — engineers know the reference, it precisely describes the approach
- Anti-hype tone — say what it does, not what it "revolutionizes"
- The scoring rubric — posting the 5 dimensions and what a 3 vs 5 looks like generates debate. Debate is free distribution.
- Real output via sample session — HN commenters will stress-test eval quality and give feedback
- Honest about costs — "I'd like to not go broke" is refreshing vs startup speak
- End with genuine feedback request on the hardest problem (evaluation quality)

### What to Avoid on HN

- "AI-powered" in the title
- Claims about job placement or success rates
- Comparisons to competitors by name
- Asking for upvotes

## Product Hunt Launch Strategy

### Timing

After Show HN. Use HN feedback to refine before broader launch. Tuesday-Thursday, early morning PT.

### Materials

- **Tagline:** "Moneyball for system design interviews" (reserved for marketing per naming spec) or "data-driven system design prep" (the landing page hold line — consistent branding)
- **Gallery (5 images):**
  1. Hero with brand and tagline
  2. Scoring dimensions (GIF/video of bars animating)
  3. Annotated transcript with strength + gap callouts
  4. Deep dive excerpt — model answer with code
  5. Coach card with trend and recommendation
- **Maker comment:** personal story — why you built it for yourself, what you learned, what's next. More emotional than HN, still authentic. Not a product pitch — sharing something useful.

### Conversion

Free tier removes all friction. "Try a session right now, free, no credit card." PH audiences click impulsively — they should be interviewing within 60 seconds of landing.

## Scope Boundaries

**This spec covers:**
- Landing page (10 sections, scroll-triggered, session 27 data)
- Sample session (`/sample` with replay)
- Session replay (general feature)
- Conversion funnel (landing → signup → free tier → home)
- Show HN + Product Hunt launch strategy
- Credits section

**Out of scope (designed elsewhere):**
- Authenticated home page, interview experience, evaluation, billing — existing UI spec
- Pricing — existing billing spec
- Brand identity, domain, taglines — existing naming spec

**Known cross-spec issue:**
- The UI spec says concurrent session limit is "pro: 2" but the billing spec updated this to "pro: 3". The billing spec is authoritative.
