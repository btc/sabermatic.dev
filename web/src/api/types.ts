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

// --- Usage & Billing ---
export interface Grant {
  id: string;
  source: "free_grant" | "subscription" | "purchase" | "admin";
  initial_minutes: number;
  remaining_minutes: number;
  expires_at: string | null;
  created_at: string;
}

export interface LedgerEntry {
  amount: number;
  reason: string;
  session_id: string | null;
  created_at: string;
}

export interface Usage {
  total_balance: number;
  free_balance: number;
  paid_balance: number;
  grants: Grant[];
  recent_activity: LedgerEntry[];
}
