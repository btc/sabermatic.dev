import type {
  Question,
  Session,
  Message,
  Evaluation,
  MessageAnnotation,
  CoachReview,
  SessionStatsResponse,
  TraceEntry,
} from "../types";

// ---- helpers ----

async function get<T>(path: string): Promise<T> {
  const res = await fetch(path);
  if (!res.ok) {
    const body = await res.text().catch(() => "");
    throw new Error(`GET ${path} ${res.status}: ${body}`);
  }
  return res.json() as Promise<T>;
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "POST",
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`POST ${path} ${res.status}: ${text}`);
  }
  return res.json() as Promise<T>;
}

// ---- API namespace ----

const api = {
  questions: {
    list: () => get<Question[]>("/api/questions/"),
    get: (id: number) => get<Question>(`/api/questions/${id}`),
  },

  sessions: {
    list: () => get<Session[]>("/api/sessions/"),
    get: (id: number) => get<Session>(`/api/sessions/${id}`),
    messages: (id: number) => get<Message[]>(`/api/sessions/${id}/messages`),
    evaluation: (id: number) =>
      get<{ evaluation: Evaluation; annotations: MessageAnnotation[] }>(
        `/api/sessions/${id}/evaluation`
      ),
    stats: () => get<SessionStatsResponse>("/api/sessions/stats"),
  },

  evaluate: {
    trigger: (sessionId: number) =>
      post<{ status: string; session_id?: number; evaluation_id?: number }>(
        `/api/evaluate/${sessionId}`
      ),
  },

  coach: {
    latest: () => get<CoachReview>("/api/coach/latest"),
    analyze: () =>
      post<{ status: string }>("/api/coach/analyze"),
  },

  traces: {
    recent: (limit = 50) => get<TraceEntry[]>(`/api/traces/recent?limit=${limit}`),
  },

  health: {
    check: () => get<{ status: string; db: boolean }>("/api/health"),
  },
};

export default api;
