// --- Auth ---
export interface User {
  id: string;
  email: string;
  display_name: string;
  role: "candidate" | "admin";
  plan: "free" | "pro";
  email_verified: boolean;
}

export interface AuthResponse {
  user: User;
}

// --- Questions ---
export type Difficulty = "medium" | "hard";
export type QuestionSource = "seed" | "custom" | "coach_generated";

export interface Question {
  id: string;
  user_id: string | null;
  title: string;
  prompt: string;
  difficulty: Difficulty;
  tags: string[];
  hints: string | null;
  source: QuestionSource;
  coach_rationale: string | null;
  created_at: string;
  attempt_count?: number;
  best_score?: number | null;
}

// --- Sessions ---
export type SessionStatus = "active" | "completed" | "evaluating" | "reviewed" | "evaluation_failed";

export interface Session {
  id: string;
  user_id: string;
  question_id: string;
  status: SessionStatus;
  config_duration_minutes: number;
  config_tts_enabled: boolean;
  config_coach_briefing: boolean;
  started_at: string;
  ended_at: string | null;
  turn_count: number;
  archived: boolean;
  created_at: string;
  updated_at: string;
  question_title?: string;
}

export interface CreateSessionRequest {
  question_id: string;
  duration_minutes: number;
  tts_enabled: boolean;
  coach_briefing?: boolean; // TODO(backend): handler does not yet accept this field
}

// --- Messages ---
export type MessageRole = "interviewer" | "candidate";

export interface Message {
  id: string;
  session_id: string;
  seq: number;
  role: MessageRole;
  content: string;
  input_method: string | null;
  audio_url: string | null;
  created_at: string;
}

// --- Evaluation ---
// NB: Matches EvaluationResponse from internal/backend/evaluation.go
export interface EvaluationResponse {
  status: string;
  scores?: EvaluationScores;
  strengths?: string[];
  gaps?: string[];
  advice?: string;
  annotations?: AnnotationResponse[];
}

export interface EvaluationScores {
  requirements: number;
  architecture: number;
  deep_dive: number;
  scalability: number;
  communication: number;
  overall: number;
}

export type AnnotationType = "strength" | "gap" | "missed_opportunity" | "note";

export interface AnnotationResponse {
  message_seq: number;
  type: AnnotationType;
  content: string;
}

// --- Educator ---
export type EducatorStatus = "generating" | "completed" | "failed";

export interface EducatorAnalysis {
  id: string;
  session_id: string;
  status: EducatorStatus;
  model_answer: string | null;
  gap_deep_dives: string | null;
  created_at: string;
}

// --- Coach ---
export interface CoachAnalysis {
  id: string;
  user_id: string;
  narrative: string;
  weakest_dimension: string | null;
  improving_dimensions: string[] | null;
  topic_gaps: string[] | null;
  suggested_question_id: string | null;
  sessions_analyzed: string[];
  created_at: string;
}

// --- Usage ---
export interface Usage {
  sessions_used: number;
  sessions_limit: number;
  period_start: string;
  period_end: string;
}
