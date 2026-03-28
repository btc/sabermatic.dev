# System Design Interview Drill — Design Spec

## What This Is

A local CLI + web app for deliberate practice of system design interviews through real voice conversation with an LLM interviewer. The goal is to develop genuine system design expertise — not rehearse answers, but build the thinking patterns, production awareness, and technical judgment needed to perform at the distinguished/principal engineer level.

After each session, the system evaluates the transcript with per-message annotations, tracks scores over time, and a coach layer identifies patterns in your thinking and recommends what to practice next.

Personal training tool. One user. No auth, no deployment. Runs on localhost.

---

## Design Principles

**Metacognition is a first-class concern.** Wherever the system can help you see *how* you think — not just what you said — it should. The evaluator identifies reasoning patterns and reflexes. The coach tracks thinking habits across sessions. The goal is self-awareness about your design process.

**Never discard intermediate data.** Raw audio, raw Whisper transcriptions, raw LLM responses, all artifacts preserved. Data is cheap to store locally. Every transformation keeps its input alongside its output.

**Grounded in reality.** The interviewer behaves like a senior staff engineer at a top company. Scenarios are rooted in real systems at real companies. The evaluator scores against what companies actually care about. No interview theater.

**The scale has no ceiling.** Scoring describes quality of thinking, not title-level gates. A 5 should be genuinely rare and describe thinking that would impress anyone.

---

## Architecture

Three LLM roles as cleanly separated modules within a single FastAPI process:

```
┌─────────────────────────────────────────────────────────┐
│                    React UI (Vite + TS)                  │
│  localhost:3000                                          │
│                                                          │
│  Screens: Home/Coach, Interview, Results, History,       │
│           Session Review                                 │
└──────────┬──────────────────────────────────┬───────────┘
           │ WebSocket (audio + JSON)         │ REST
           │                                  │
┌──────────▼──────────────────────────────────▼───────────┐
│                 FastAPI Backend                          │
│  localhost:8000                                          │
│                                                          │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐       │
│  │ Interviewer  │ │  Evaluator  │ │    Coach    │       │
│  │             │ │             │ │             │       │
│  │ - State     │ │ - Scores    │ │ - Trends    │       │
│  │   machine   │ │   transcript│ │ - Gaps      │       │
│  │ - Converses │ │   against   │ │ - Generates │       │
│  │   in real   │ │   rubric    │ │   scenarios │       │
│  │   time      │ │ - Per-msg   │ │ - Recommends│       │
│  │ - Stateless │ │   annotate  │ │   next      │       │
│  │   by default│ │ - Both roles│ │   session   │       │
│  └─────────────┘ └─────────────┘ └─────────────┘       │
│                                                          │
│  ┌─────────────┐ ┌─────────────┐ ┌─────────────┐       │
│  │   Speech    │ │  Storage    │ │   Session   │       │
│  │             │ │             │ │   Manager   │       │
│  │ - Whisper   │ │ - Postgres  │ │             │       │
│  │ - TTS       │ │ - Filesystem│ │ - Timer     │       │
│  │   (toggle)  │ │ - Raw audio │ │ - State     │       │
│  └─────────────┘ └─────────────┘ └─────────────┘       │
└─────────────────────────────────────────────────────────┘
```

### Module Boundaries

- **Interviewer** — takes conversation history in, produces a response out. Knows nothing about audio, storage, or scoring. Stateless by default, with an optional briefing from the coach (toggle).
- **Evaluator** — takes a complete transcript + question in, produces structured scores and per-message annotations out. Pure function, no side effects. Annotates both candidate AND interviewer messages.
- **Coach** — takes all historical session/evaluation data in, produces recommendations and generated scenarios out. Runs on app load and after evaluations complete.
- **Speech** — wraps Whisper and TTS APIs. The rest of the system deals in text; speech is a translation layer at the edge.
- **Storage** — owns Postgres and the filesystem. Everything goes through it. No module touches the DB directly.
- **Session Manager** — orchestrates a live interview. Coordinates the state machine, wires speech to interviewer to TTS, manages the timer.

Each module has a typed interface (Pydantic models in, Pydantic models out). No module reaches into another's internals.

### Tech Stack

- **Backend**: Python 3.11+, FastAPI, uvicorn, asyncpg (raw SQL, no ORM)
- **Frontend**: React + Vite + TypeScript
- **Database**: PostgreSQL (ENUM types, JSONB, array types, STRICT constraints)
- **LLM**: Claude API — three system prompts, one SDK
- **Speech-to-text**: OpenAI Whisper API (whisper-1)
- **Text-to-speech**: OpenAI TTS API (tts-1), toggleable on/off
- **Storage**: Postgres for structured data, filesystem for audio artifacts and raw responses

**Not used:** Redis, vector DB, Docker, message queues, embeddings, ORM.

---

## Data Model

### Enums

```sql
CREATE TYPE difficulty AS ENUM ('medium', 'hard');
CREATE TYPE question_source AS ENUM ('seed', 'custom', 'coach_generated');
CREATE TYPE session_status AS ENUM ('active', 'completed', 'evaluating', 'reviewed');
CREATE TYPE message_role AS ENUM ('interviewer', 'candidate');
CREATE TYPE annotation_type AS ENUM ('strength', 'gap', 'missed_opportunity', 'note');
```

### Tables

```sql
CREATE TABLE questions (
    id              SERIAL PRIMARY KEY,
    title           TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    difficulty      difficulty NOT NULL,
    tags            TEXT[] NOT NULL DEFAULT '{}',
    hints           JSONB,
    source          question_source NOT NULL DEFAULT 'seed',
    source_detail   TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_questions_tags ON questions USING GIN (tags);
CREATE INDEX idx_questions_source ON questions (source);

CREATE TABLE sessions (
    id                  SERIAL PRIMARY KEY,
    question_id         INTEGER NOT NULL REFERENCES questions(id),
    status              session_status NOT NULL DEFAULT 'active',
    timer_setting_sec   INTEGER NOT NULL DEFAULT 2700,
    interviewer_briefed BOOLEAN NOT NULL DEFAULT false,
    started_at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at            TIMESTAMPTZ,
    duration_seconds    INTEGER,
    turn_count          INTEGER,
    audio_dir           TEXT,

    CONSTRAINT valid_duration CHECK (duration_seconds >= 0),
    CONSTRAINT valid_turn_count CHECK (turn_count >= 0),
    CONSTRAINT ended_after_started CHECK (ended_at IS NULL OR ended_at >= started_at)
);

CREATE INDEX idx_sessions_question ON sessions (question_id);
CREATE INDEX idx_sessions_status ON sessions (status);
CREATE INDEX idx_sessions_started ON sessions (started_at DESC);

CREATE TABLE messages (
    id                  SERIAL PRIMARY KEY,
    session_id          INTEGER NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    sequence            INTEGER NOT NULL,
    role                message_role NOT NULL,
    content             TEXT NOT NULL,
    raw_content         TEXT,
    timestamp           TIMESTAMPTZ NOT NULL DEFAULT now(),
    audio_path          TEXT,
    audio_duration_sec  REAL,

    CONSTRAINT unique_sequence UNIQUE (session_id, sequence),
    CONSTRAINT valid_audio_duration CHECK (audio_duration_sec IS NULL OR audio_duration_sec >= 0)
);

CREATE INDEX idx_messages_session ON messages (session_id, sequence);

CREATE TABLE evaluations (
    id                      SERIAL PRIMARY KEY,
    session_id              INTEGER NOT NULL REFERENCES sessions(id),
    score_requirements      INTEGER NOT NULL CHECK (score_requirements BETWEEN 1 AND 5),
    score_highlevel         INTEGER NOT NULL CHECK (score_highlevel BETWEEN 1 AND 5),
    score_deepdive          INTEGER NOT NULL CHECK (score_deepdive BETWEEN 1 AND 5),
    score_scalability       INTEGER NOT NULL CHECK (score_scalability BETWEEN 1 AND 5),
    score_communication     INTEGER NOT NULL CHECK (score_communication BETWEEN 1 AND 5),
    score_overall           INTEGER NOT NULL CHECK (score_overall BETWEEN 1 AND 5),
    strengths               JSONB NOT NULL,
    gaps                    JSONB NOT NULL,
    advice                  TEXT NOT NULL,
    raw_response            JSONB NOT NULL,
    evaluated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_evaluations_session ON evaluations (session_id);
CREATE INDEX idx_evaluations_date ON evaluations (evaluated_at DESC);

CREATE TABLE message_annotations (
    id              SERIAL PRIMARY KEY,
    evaluation_id   INTEGER NOT NULL REFERENCES evaluations(id),
    message_id      INTEGER NOT NULL REFERENCES messages(id),
    annotation_type annotation_type NOT NULL,
    content         TEXT NOT NULL,

    CONSTRAINT unique_annotation UNIQUE (evaluation_id, message_id, annotation_type)
);

CREATE INDEX idx_annotations_eval ON message_annotations (evaluation_id);
CREATE INDEX idx_annotations_message ON message_annotations (message_id);

CREATE TABLE coach_reviews (
    id                      SERIAL PRIMARY KEY,
    recommendation          TEXT NOT NULL,
    gap_analysis            JSONB NOT NULL,
    suggested_question_id   INTEGER REFERENCES questions(id),
    sessions_analyzed       INTEGER[] NOT NULL DEFAULT '{}',
    raw_response            JSONB NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_coach_reviews_date ON coach_reviews (created_at DESC);
```

### Key Design Decisions

- **Messages in the DB, not just JSON files.** Enables the coach to query across sessions. JSON archival files are also written for raw preservation.
- **Evaluations separated from sessions.** Last write wins — latest by `evaluated_at` is canonical. Supports re-evaluation with improved prompts.
- **Hints are optional.** Seed questions may have them as supplementary guidance. Coach-generated questions skip them. The evaluator scores against the five fixed rubric dimensions and can reason about what good coverage looks like from the question itself.
- **Message annotations are per-evaluation.** Re-evaluating a session produces a new set of annotations.
- **`raw_content` on messages.** Preserves original Whisper output before user edits. Null for interviewer messages.
- **`interviewer_briefed` on sessions.** Records whether the coach toggle was on — important context when reviewing scores.

### Filesystem Layout

```
data/
  sessions/
    session_012/
      audio_in/              # raw user audio chunks per turn
      audio_out/             # TTS audio chunks per turn
      full_audio_in.webm     # concatenated user audio
      full_audio_out.mp3     # concatenated interviewer audio
      transcript.json        # archival transcript
      evaluation.json        # raw evaluator response
```

---

## The Three LLM Roles

### 1. The Interviewer

A senior staff engineer conducting a system design interview. The system prompt encodes the behavior of a great interviewer:

**Core behaviors:**
- Opens with deliberate vagueness. The ambiguity is the test.
- Stays silent when the candidate should be driving. Does not jump in to help.
- Probes with WHY, not WHAT. "You said Kafka. Why not SQS? Why not Redis pub/sub?"
- Introduces constraints that break naive designs. "Now your user base is global — 40% Asia, 30% Americas, 30% Europe. What changes?"
- Tracks what hasn't been covered. If monitoring/failure modes/deployment haven't come up by the back half, steers there.
- Pushes past hand-waving. "You said 'we shard the database.' On what key? What's the distribution? What happens to cross-shard queries?"
- Never validates. Never says "that's correct." Stays neutral: "OK. What would you tackle next?"
- Keeps responses short. 2-4 sentences. Real interviewers don't lecture.
- Aware of elapsed time. Steers to uncovered areas in the back half. Wraps naturally in the last 5 minutes.

**Domain grounding:** For each question, the interviewer has embedded knowledge of the real technical landscape so probing is intelligent, not generic. For "Design a rate limiter," it knows to push on: sliding window vs fixed window vs token bucket, distributed rate limiting, race conditions, and what happens when the limiter becomes a bottleneck.

**Stateless by default.** Does not know the candidate's history. Optional toggle prepends the coach's gap analysis so the interviewer targets weak areas.

### 2. The Evaluator

Reads the full transcript after the session ends. Produces structured scores, qualitative feedback, and per-message annotations for both candidate AND interviewer messages.

**Five rubric dimensions, each 1-5:**

**Requirements & Scoping**
- 1: Jumped straight into drawing boxes.
- 2: Asked a couple of questions but missed critical dimensions.
- 3: Defined functional requirements, stated non-functional requirements, established rough scale, made explicit scope decisions.
- 4: Crisp requirements with back-of-envelope math that drives design decisions.
- 5: Requirements reveal deep product thinking and anticipate hard design decisions.

**High-Level Design**
- 1: Components don't connect logically.
- 2: Standard boxes without explaining data flow or reasoning.
- 3: Coherent architecture with clear data flow and reasonable APIs.
- 4: Architecture reflects specific requirements, not a generic template.
- 5: Architecture anticipates evolution and connects current choices to future needs.

**Deep Dive & Detail**
- 1: Stayed at box-and-arrow level throughout.
- 2: Went one level deeper on one component but couldn't sustain it.
- 3: Solid depth on 1-2 components with real understanding.
- 4: Deep and correct on critical path components. Production-aware decisions.
- 5: Novel or non-obvious insights. Second and third-order thinking. Systemic reasoning.

**Scalability & Tradeoffs**
- 1: Didn't discuss scale.
- 2: Mentioned scaling without substance.
- 3: Identified main bottleneck with a reasonable strategy. At least one explicit tradeoff.
- 4: Multiple bottlenecks with specific solutions. Tradeoffs articulated with reasoning.
- 5: Systemic scaling thinking. Cascading failures, back-pressure, graceful degradation.

**Communication**
- 1: Waited to be led. Rambling.
- 2: Needed frequent prompting.
- 3: Drove the conversation with organized thoughts.
- 4: Clear narrative. Signposted transitions. Engaged productively with pushback.
- 5: Made the interviewer's job easy. Proactively surfaced tradeoffs and uncertainties.

**Scores describe quality of thinking, not level gates.** No labels like "mid-level bar" or "staff-level." A 5 should be genuinely rare and describe thinking that would impress anyone.

**Additional evaluator outputs:**
- `overall` score (1-5)
- `strengths` — 2-3 specific things done well, with transcript evidence
- `gaps` — 2-4 specific things missed or done poorly, with transcript evidence
- `advice` — actionable guidance including a metacognitive prompt for the next session

**Per-message annotations:** The evaluator annotates specific moments in the transcript — both candidate responses (strengths and gaps) and interviewer messages (good probes and missed opportunities). Annotation types: `strength`, `gap`, `missed_opportunity`, `note`.

**Metacognitive feedback:** The evaluator identifies reasoning patterns and reflexes. "You defaulted to Redis three times without considering alternatives — conscious choice or reflex?" "You consistently nail the happy path but only address failure modes when prompted."

**Calibration:** Most early practice sessions should produce 2s and 3s. If the evaluator is handing out 4s and 5s regularly, the calibration is off. A 5 on any dimension should be genuinely exceptional.

### 3. The Coach

Runs on app load and after evaluations complete. Reads all historical session data and produces strategic guidance.

**Responsibilities:**
- **Dimension analysis.** Which dimensions are consistently weakest? Improving or flat?
- **Topic coverage.** What categories of systems have been practiced? What's missing? (Read-heavy, write-heavy, real-time, batch, storage, compute.)
- **Thinking pattern recognition.** Do you always skip failure modes? Rush past requirements? Go deep on storage but never discuss networking? Default to the same technologies without considering alternatives? These behavioral patterns are more actionable than dimension scores.
- **Scenario generation.** Creates new questions rooted in real systems at real companies. Not "design a generic data processing system" but "Design Stripe's payment retry and reconciliation system." Scenarios target specific gaps.
- **Progressive difficulty.** If scoring 3+ on medium consistently, push to hard. If struggling with medium, suggest targeted drills.
- **Metacognitive coaching.** Not just "practice write-heavy systems" but "next session, try narrating your decision process aloud: 'I'm choosing X over Y because...'" Identifies thinking habits, not just knowledge gaps.

**Proactive recommendations** surface on the home screen every time the app is opened. The coach doesn't wait to be asked.

---

## Interview Session Flow

### Latency Pipeline

```
You stop talking
  → audio sent to server           ~50ms  (WebSocket)
  → Whisper transcription          ~1-3s  (API)
  → transcript displayed, sent     ~50ms
    to Claude with full history
  → Claude streams response        ~1-3s  (first token)
  → TTS begins generating          ~0.5s  (if enabled)
  → audio starts playing           ~0.5s  (first chunk)
                                   ─────
                             Total: 2-7s
```

Everything streams. Don't wait for Claude to finish before starting TTS. Don't wait for TTS to finish before sending the first audio chunk. Each stage starts as soon as it gets first output from the previous stage.

### State Machine

```
IDLE
  │ user picks question
  ▼
STARTING
  │ session created, interviewer presents question
  ▼
INTERVIEWER_SPEAKING ◄─────────────────────┐
  │ TTS playing (or text displayed)        │
  │                                        │
  ├─── user presses talk key ──► INTERRUPTED
  │    (audio stops, text stays)     │
  │                                  │
  │ interviewer finishes             │
  ▼                                  │
WAITING_FOR_CANDIDATE                │
  │ user presses talk key            │
  ▼                                  ▼
CANDIDATE_SPEAKING
  │ recording audio, waveform visible
  │ user releases talk key
  ▼
PROCESSING
  │ audio → Whisper → display transcript
  │ transcript → Claude (streaming)
  │ Claude response → TTS (if enabled)
  ▼
INTERVIEWER_SPEAKING ──────────────────────┘
  │
  ├─── user ends session / timer expires
  ▼
ENDING
  │ interviewer wraps up naturally
  ▼
ENDED → EVALUATING → REVIEWED
```

### Interrupt Handling

When the user presses the talk key while the interviewer is speaking:
1. TTS audio stops immediately. Text response remains visible (keeps flowing if still streaming).
2. The partial/full interviewer response is preserved in messages.
3. Recording begins.
4. Claude sees the full interviewer response in conversation history on the next turn.

### Transcript Editing

After Whisper returns, the transcription appears with a brief editable window (2-3 seconds, or until the user presses talk again). If the user clicks into it, auto-send pauses until they confirm. Raw Whisper output → `raw_content`. Edited version → `content`.

### Audio Recording

Browser MediaRecorder captures WebM/Opus. Each talk turn is one chunk:
1. Streamed over WebSocket during recording
2. Saved to disk as individual files (`audio_in/turn_003.webm`)
3. After session ends, concatenated into `full_audio_in.webm`

TTS audio chunks similarly saved to `audio_out/` and concatenated after session.

### WebSocket Protocol

Single WebSocket connection per interview session. All messages JSON with a `type` field:

```
Client → Server:
  { "type": "start", "question_id": 5, "timer_sec": 2700, "tts_enabled": true, "briefed": false }
  { "type": "audio", "data": "<base64 audio chunk>" }
  { "type": "end_turn" }
  { "type": "edit_transcript", "text": "corrected text" }
  { "type": "end_session" }

Server → Client:
  { "type": "interviewer_text", "content": "...", "done": false }
  { "type": "interviewer_audio", "data": "<base64 audio chunk>" }
  { "type": "interviewer_done" }
  { "type": "transcription", "text": "what whisper heard" }
  { "type": "state", "state": "WAITING_FOR_CANDIDATE" }
  { "type": "timer", "elapsed_seconds": 120 }
  { "type": "session_ended", "session_id": 12 }
  { "type": "evaluation_complete", "session_id": 12 }
  { "type": "error", "message": "..." }
```

### Context Window Management

Full conversation history sent to Claude on every turn. For very long sessions (60+ minutes), summarize older turns: keep the system prompt + first 3 turns (requirements phase) + compressed summary of the middle + last 10-15 turns verbatim. Compression is a background Claude call when transcript crosses a threshold.

### Error Recovery

- **Whisper fails** → show error, let user re-record or type manually
- **Claude fails** → retry once, surface "interviewer is gathering thoughts," option to retry or end
- **TTS fails** → fall back to text-only for that turn, continue
- **WebSocket drops** → client auto-reconnects, server replays session state
- **App crashes** → session has `status: active` in DB with all messages saved. On next load, offer to resume or end and evaluate what exists.

---

## Frontend

Five screens. React + Vite + TypeScript. Minimal dependencies. All content in a centered column (~720px max-width).

### 1. Home / Coach Dashboard

- **Coach recommendation** at top — gap analysis, thinking pattern observations, suggested next scenario with rationale for why it targets your weaknesses
- **Summary stats** — sessions completed, average overall score, weakest dimension, most improved dimension
- **Dimension score bars** — color-coded (red for weak, green for improving)
- **Question bank** — seed, custom, and coach-generated. Filterable by tags, difficulty, attempted/unattempted. Shows: times attempted, best score, most recent score. Coach's suggestion highlighted.
- **Start Session** — pick question, set timer (default 45 min), toggle TTS, toggle interviewer briefing

### 2. Interview Screen

Minimal. Conversation is the focus.

- **Chat log** — centered column, interviewer messages left, candidate right. Text streams in live from Claude.
- **Timer** — corner, counts up, visual shift at 5 minutes remaining
- **Recording indicator** — waveform/amplitude bar when holding spacebar
- **Transcript edit** — editable state after Whisper returns, auto-sends after a couple seconds
- **End Session** button
- Nothing else. No scores, no hints, no distractions.

### 3. Results

Post-session evaluation screen.

- Five dimension scores (bar chart or radar)
- Overall score prominent
- Strengths with transcript quotes
- Gaps with transcript quotes
- Advice paragraph with metacognitive prompt
- Link to full transcript review

### 4. History

- **Score trend** — bar/line chart, left (oldest) to right (most recent)
- **Session list** — date, question, duration, turns, 5 dimension scores as chips, overall score badge. Left border color-coded to score. Clickable into session review.
- **Weakest dimension callout**

### 5. Session Review

Two-panel layout:

- **Left: full transcript.** Scrollable chat log with timestamps. Evaluator annotations inline — weak moments highlighted red with evaluator's note, strong moments highlighted green. Interviewer messages also annotated (good probes, missed opportunities) — toggleable, off by default.
- **Right: evaluation panel.** Scores, strengths, gaps, advice. Sticky, visible as you scroll.

### Frontend Tech Notes

- Spacebar as push-to-talk (prevent default scroll)
- `MediaRecorder` API for audio capture
- `AudioContext` for playback and waveform visualization
- WebSocket hook for interview session, REST for everything else
- Light chart library (recharts or similar) for score visualizations

---

## Configuration

```
# .env
ANTHROPIC_API_KEY=sk-ant-...
OPENAI_API_KEY=sk-...

# Optional overrides
INTERVIEWER_MODEL=claude-sonnet-4-20250514
EVALUATOR_MODEL=claude-sonnet-4-20250514
COACH_MODEL=claude-sonnet-4-20250514
TTS_VOICE=onyx
TTS_MODEL=tts-1
WHISPER_MODEL=whisper-1
DEFAULT_TIMER_MINUTES=45
```

### Setup

```bash
git clone <repo>
cd drill
cp .env.example .env
# Add API keys

pip install -r requirements.txt
cd frontend && npm install && cd ..

# Create database
createdb drill
python scripts/init_db.py

# Start
python run.py
# → Backend on localhost:8000, frontend on localhost:3000
```

`run.py` starts both backend and frontend dev server in parallel, opens browser to localhost:3000.

---

## Observability: Local Tracing

OpenTelemetry instrumentation with local file export. Every user action traces through the full pipeline. Zero additional infrastructure — no Jaeger, no collector, just JSON files on disk.

### Span Tree

```
interview.turn (one conversational turn)
├── ws.receive_audio          # WebSocket audio receipt, chunk size
├── speech.transcribe         # Whisper API: latency, audio duration, confidence
├── ws.send_transcription     # Transcription sent to client
├── llm.interviewer           # Claude API: tokens in/out, time to first token
│   ├── llm.stream_chunk      # Each streamed token batch
│   └── llm.complete          # Total response
├── speech.tts                # TTS API: latency to first audio chunk
│   └── speech.tts_chunk      # Each audio chunk streamed back
├── ws.send_audio             # Audio chunks sent to client
└── db.save_message           # Postgres write

interview.evaluate (post-session)
├── db.load_transcript
├── llm.evaluator
├── db.save_evaluation
└── db.save_annotations

coach.analyze (on app load)
├── db.load_history
├── llm.coach
├── db.save_question          # If new scenario generated
└── db.save_review
```

### Instrumentation Approach

- **Auto-instrumentation** for FastAPI (HTTP/WS requests), asyncpg (DB queries), httpx (outbound API calls to Whisper, Claude, TTS) — a few lines each.
- **Manual spans** for business logic: session state transitions, turn processing, evaluation pipeline.
- Each span carries custom attributes: `session_id`, `turn_sequence`, `audio_duration_sec`, `token_count`, `model`, error messages.

### Storage

```
data/
  traces/
    2026-03-28/
      a3f8c2_session_012_turn_001.json
      a3f8c2_session_012_turn_002.json
      b7e1d4_session_012_evaluate.json
      c9a2f1_coach_review.json
```

### Surfacing Trace IDs

- **On screen:** tiny monospace text in the bottom-left corner of every screen. Updates with each action. Click to copy full trace ID to clipboard. Muted, nearly invisible unless you're looking for it.

  ```
  t:a3f8c2
  ```

- **In browser console:** every trace logs `[trace:a3f8c2] interview.turn started` as a fallback.

- **Debugging flow:** something feels off → glance at corner → copy trace ID → open Claude Code → "trace a3f8c2 had a long pause" → read the trace JSON → see exactly where the time went.

---

## What Not to Build (v1)

- Whiteboard / diagramming (massive scope, v2)
- Multiple interviewer personalities
- Full session audio playback in browser (store audio, playback later)
- User accounts or multi-user support
- Mobile support
- Fancy analytics beyond what's described
- Embeddings or vector search
- Docker / containerization

---

## Success Criteria

1. You can start a session, talk through a design for 30-45 minutes, and the conversation feels like a real interview — the interviewer pushes back, probes gaps, never volunteers answers.
2. The speech pipeline feels conversational. Under 3-4 seconds from when you stop talking to when the interviewer starts responding.
3. Evaluation scores are calibrated honestly. 2s and 3s early on. 4s and 5s are earned.
4. Per-message annotations make the transcript review genuinely useful. You can see exactly where you were strong and where you were weak, and why.
5. After 10 sessions, the coach identifies real patterns in your thinking — not just "scalability is low" but "you default to the same three technologies without considering alternatives."
6. The coach generates scenarios that feel like real problems at real companies, targeting your specific gaps.
7. Looking at your score history shows clear trajectory and you can trace improvements to specific practice.
