// ---- Enums ----

export type Difficulty = "medium" | "hard";
export type QuestionSource = "seed" | "custom" | "coach_generated";
export type SessionStatus =
  | "active"
  | "completed"
  | "evaluating"
  | "reviewed"
  | "evaluation_failed";
export type MessageRole = "interviewer" | "candidate";
export type AnnotationType = "strength" | "gap" | "missed_opportunity" | "note";

// ---- Domain models (mirror backend Pydantic "read" models) ----

export interface Question {
  id: number;
  title: string;
  prompt: string;
  difficulty: Difficulty;
  tags: string[];
  hints: Record<string, unknown> | null;
  source: QuestionSource;
  source_detail: string | null;
  created_at: string; // ISO datetime
}

export interface Session {
  id: number;
  question_id: number;
  status: SessionStatus;
  status_detail: string | null;
  timer_setting_sec: number;
  interviewer_briefed: boolean;
  started_at: string;
  ended_at: string | null;
  duration_seconds: number | null;
  turn_count: number | null;
  audio_dir: string | null;
  archived: boolean;
  tts_enabled: boolean;
}

export interface Message {
  id: number;
  session_id: number;
  sequence: number;
  role: MessageRole;
  content: string;
  raw_content: string | null;
  timestamp: string;
  audio_path: string | null;
  audio_duration_sec: number | null;
}

export interface Evaluation {
  id: number;
  session_id: number;
  score_requirements: number;
  score_highlevel: number;
  score_deepdive: number;
  score_scalability: number;
  score_communication: number;
  score_overall: number;
  strengths: string[];
  gaps: string[];
  advice: string;
  raw_response: Record<string, unknown>;
  evaluated_at: string;
}

export interface MessageAnnotation {
  id: number;
  evaluation_id: number;
  message_id: number;
  annotation_type: AnnotationType;
  content: string;
}

export interface CoachReview {
  id: number;
  recommendation: string;
  gap_analysis: Record<string, unknown>;
  suggested_question_id: number | null;
  sessions_analyzed: number[];
  raw_response: Record<string, unknown>;
  created_at: string;
}

// ---- Aggregates ----

export interface DimensionAverages {
  requirements: number;
  highlevel: number;
  deepdive: number;
  scalability: number;
  communication: number;
  overall: number;
}

export interface QuestionStats {
  question_id: number;
  title: string;
  session_count: number;
  avg_overall: number | null;
}

export interface SessionStatsResponse {
  averages: DimensionAverages | null;
  question_stats: QuestionStats[];
}

// ---- Traces ----

export interface TraceEntry {
  trace_id: string;
  trace_id_short: string;
  span_id: string;
  name: string;
  start_time: string;
  duration_ms: number | null;
  status: string | null;
  attributes: Record<string, unknown>;
  action_label: string;
}

// ---- WebSocket messages ----

// Client -> Server
export type WSClientMessage =
  | { type: "start"; question_id: number; timer_sec?: number; tts_enabled?: boolean; briefed?: boolean }
  | { type: "end_turn"; audio_data: string }
  | { type: "text_input"; text: string }
  | { type: "edit_transcript"; text: string }
  | { type: "end_session" };

// Server -> Client
export type WSServerMessage =
  | { type: "interviewer_text"; content: string; done: boolean }
  | { type: "interviewer_audio"; data: string }
  | { type: "interviewer_done" }
  | { type: "transcription"; text: string }
  | { type: "state"; state: string }
  | { type: "timer"; elapsed_seconds: number }
  | { type: "session_ended"; session_id: number }
  | { type: "error"; message: string }
  | { type: "message_history"; sequence: number; role: string; content: string; timestamp: string | null }
  | { type: "tts_error"; message: string }
  | { type: "session_loaded"; session_id: number; started_at: string; timer_sec: number; tts_enabled: boolean };
