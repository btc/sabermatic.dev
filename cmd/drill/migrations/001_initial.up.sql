-- 001_initial.up.sql: Full schema for Drill v1

-- Auth tables

CREATE TABLE users (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    email           TEXT UNIQUE NOT NULL,
    email_verified  BOOLEAN NOT NULL DEFAULT FALSE,
    password_hash   TEXT,
    display_name    TEXT NOT NULL,
    role            TEXT NOT NULL DEFAULT 'candidate'
                    CHECK (role IN ('candidate', 'admin')),
    stripe_customer_id TEXT,
    plan            TEXT NOT NULL DEFAULT 'free',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at      TIMESTAMPTZ
);

CREATE TABLE oauth_accounts (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id),
    provider    TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (provider, provider_id)
);

CREATE TABLE auth_sessions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      UUID NOT NULL REFERENCES users(id),
    token_hash   TEXT UNIQUE NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL,
    last_active  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ip_address   INET,
    user_agent   TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Core tables

CREATE TABLE questions (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID REFERENCES users(id),
    title           TEXT NOT NULL,
    prompt          TEXT NOT NULL,
    difficulty      TEXT NOT NULL CHECK (difficulty IN ('medium', 'hard')),
    tags            TEXT[] NOT NULL DEFAULT '{}',
    hints           TEXT,
    source          TEXT NOT NULL CHECK (source IN ('seed', 'custom', 'coach_generated')),
    coach_rationale TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE interview_sessions (
    id                      UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id                 UUID NOT NULL REFERENCES users(id),
    question_id             UUID NOT NULL REFERENCES questions(id),
    status                  TEXT NOT NULL DEFAULT 'active'
                            CHECK (status IN (
                                'active','completed','evaluating',
                                'reviewed','evaluation_failed'
                            )),
    config_duration_minutes INT NOT NULL,
    config_tts_enabled      BOOLEAN NOT NULL DEFAULT FALSE,
    config_coach_briefing   BOOLEAN NOT NULL DEFAULT FALSE,
    started_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at                TIMESTAMPTZ,
    turn_count              INT NOT NULL DEFAULT 0,
    archived                BOOLEAN NOT NULL DEFAULT FALSE,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE messages (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id   UUID NOT NULL REFERENCES interview_sessions(id),
    seq          INT NOT NULL,
    role         TEXT NOT NULL CHECK (role IN ('interviewer', 'candidate')),
    content      TEXT NOT NULL,
    input_method TEXT,
    audio_url    TEXT,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (session_id, seq)
);

CREATE TABLE evaluations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id          UUID UNIQUE NOT NULL REFERENCES interview_sessions(id),
    score_requirements  INT NOT NULL CHECK (score_requirements BETWEEN 1 AND 5),
    score_architecture  INT NOT NULL CHECK (score_architecture BETWEEN 1 AND 5),
    score_deep_dive     INT NOT NULL CHECK (score_deep_dive BETWEEN 1 AND 5),
    score_scalability   INT NOT NULL CHECK (score_scalability BETWEEN 1 AND 5),
    score_communication INT NOT NULL CHECK (score_communication BETWEEN 1 AND 5),
    score_overall       INT NOT NULL CHECK (score_overall BETWEEN 1 AND 5),
    strengths           JSONB NOT NULL,
    gaps                JSONB NOT NULL,
    advice              TEXT NOT NULL,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE annotations (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    evaluation_id   UUID NOT NULL REFERENCES evaluations(id),
    message_id      UUID NOT NULL REFERENCES messages(id),
    annotation_type TEXT NOT NULL
                    CHECK (annotation_type IN (
                        'strength','gap','missed_opportunity','note'
                    )),
    content         TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE educator_analyses (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID UNIQUE NOT NULL REFERENCES interview_sessions(id),
    status          TEXT NOT NULL DEFAULT 'generating'
                    CHECK (status IN ('generating', 'completed')),
    model_answer    TEXT,
    gap_deep_dives  TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE coach_analyses (
    id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               UUID NOT NULL REFERENCES users(id),
    narrative             TEXT NOT NULL,
    weakest_dimension     TEXT,
    improving_dimensions  TEXT[],
    topic_gaps            TEXT[],
    suggested_question_id UUID REFERENCES questions(id),
    sessions_analyzed     UUID[] NOT NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Observability & billing tables

CREATE TABLE llm_calls (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id     UUID REFERENCES interview_sessions(id),
    user_id        UUID NOT NULL REFERENCES users(id),
    role           TEXT NOT NULL,
    model          TEXT NOT NULL,
    input_tokens   INT NOT NULL,
    output_tokens  INT NOT NULL,
    estimated_cost NUMERIC(10,6) NOT NULL,
    latency_ms     INT NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE llm_call_content (
    llm_call_id UUID PRIMARY KEY REFERENCES llm_calls(id),
    prompt      JSONB NOT NULL,
    response    JSONB NOT NULL
);

CREATE TABLE user_events (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id),
    session_id UUID REFERENCES interview_sessions(id),
    event_type TEXT NOT NULL,
    metadata   JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE usage_periods (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id       UUID NOT NULL REFERENCES users(id),
    period_start  TIMESTAMPTZ NOT NULL,
    period_end    TIMESTAMPTZ NOT NULL,
    sessions_used INT NOT NULL DEFAULT 0,
    UNIQUE (user_id, period_start)
);

-- Indexes

CREATE INDEX idx_sessions_user_status ON interview_sessions(user_id, status)
    WHERE archived = FALSE;
CREATE INDEX idx_sessions_user_archived ON interview_sessions(user_id, archived);
CREATE INDEX idx_sessions_question ON interview_sessions(question_id);
CREATE INDEX idx_messages_session_seq ON messages(session_id, seq);
CREATE INDEX idx_annotations_evaluation ON annotations(evaluation_id);
CREATE INDEX idx_annotations_message ON annotations(message_id);
CREATE INDEX idx_llm_calls_session ON llm_calls(session_id);
CREATE INDEX idx_llm_calls_user_created ON llm_calls(user_id, created_at);
CREATE INDEX idx_user_events_user_created ON user_events(user_id, created_at);
CREATE INDEX idx_questions_user ON questions(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_questions_seed ON questions(id) WHERE source = 'seed';
CREATE INDEX idx_usage_periods_user ON usage_periods(user_id, period_start);
CREATE INDEX idx_coach_analyses_user ON coach_analyses(user_id, created_at DESC);
