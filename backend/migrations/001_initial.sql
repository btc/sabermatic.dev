-- 001_initial.sql: Full schema for drill application

-- ENUMs (using DO blocks to handle "already exists" gracefully)

DO $$ BEGIN
    CREATE TYPE difficulty AS ENUM ('medium', 'hard');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE question_source AS ENUM ('seed', 'custom', 'coach_generated');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE session_status AS ENUM ('active', 'completed', 'evaluating', 'reviewed');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE message_role AS ENUM ('interviewer', 'candidate');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

DO $$ BEGIN
    CREATE TYPE annotation_type AS ENUM ('strength', 'gap', 'missed_opportunity', 'note');
EXCEPTION WHEN duplicate_object THEN NULL;
END $$;

-- Tables

CREATE TABLE IF NOT EXISTS questions (
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

CREATE INDEX IF NOT EXISTS idx_questions_tags ON questions USING GIN (tags);
CREATE INDEX IF NOT EXISTS idx_questions_source ON questions (source);

CREATE TABLE IF NOT EXISTS sessions (
    id                  SERIAL PRIMARY KEY,
    question_id         INTEGER NOT NULL REFERENCES questions(id),
    status              session_status NOT NULL DEFAULT 'active',
    status_detail       TEXT,
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

CREATE INDEX IF NOT EXISTS idx_sessions_question ON sessions (question_id);
CREATE INDEX IF NOT EXISTS idx_sessions_status ON sessions (status);
CREATE INDEX IF NOT EXISTS idx_sessions_started ON sessions (started_at DESC);

CREATE TABLE IF NOT EXISTS messages (
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

CREATE INDEX IF NOT EXISTS idx_messages_session ON messages (session_id, sequence);

CREATE TABLE IF NOT EXISTS evaluations (
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

CREATE INDEX IF NOT EXISTS idx_evaluations_session ON evaluations (session_id);
CREATE INDEX IF NOT EXISTS idx_evaluations_date ON evaluations (evaluated_at DESC);

CREATE TABLE IF NOT EXISTS message_annotations (
    id              SERIAL PRIMARY KEY,
    evaluation_id   INTEGER NOT NULL REFERENCES evaluations(id),
    message_id      INTEGER NOT NULL REFERENCES messages(id),
    annotation_type annotation_type NOT NULL,
    content         TEXT NOT NULL,

    CONSTRAINT unique_annotation UNIQUE (evaluation_id, message_id, annotation_type)
);

CREATE INDEX IF NOT EXISTS idx_annotations_eval ON message_annotations (evaluation_id);
CREATE INDEX IF NOT EXISTS idx_annotations_message ON message_annotations (message_id);

CREATE TABLE IF NOT EXISTS coach_reviews (
    id                      SERIAL PRIMARY KEY,
    recommendation          TEXT NOT NULL,
    gap_analysis            JSONB NOT NULL,
    suggested_question_id   INTEGER REFERENCES questions(id),
    sessions_analyzed       INTEGER[] NOT NULL DEFAULT '{}',
    raw_response            JSONB NOT NULL,
    created_at              TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_coach_reviews_date ON coach_reviews (created_at DESC);
