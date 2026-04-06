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

