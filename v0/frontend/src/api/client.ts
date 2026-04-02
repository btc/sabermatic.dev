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

async function patch<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(path, {
    method: "PATCH",
    headers: body !== undefined ? { "Content-Type": "application/json" } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`PATCH ${path} ${res.status}: ${text}`);
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
    list: (includeArchived?: boolean) =>
      get<Session[]>(`/api/sessions/${includeArchived ? "?include_archived=true" : ""}`),
    get: (id: number) => get<Session>(`/api/sessions/${id}`),
    create: (body: { question_id: number; timer_setting_sec: number; tts_enabled: boolean }) =>
      post<Session>("/api/sessions", body),
    messages: (id: number) => get<Message[]>(`/api/sessions/${id}/messages`),
    evaluation: (id: number) =>
      get<{ evaluation: Evaluation; annotations: MessageAnnotation[] }>(
        `/api/sessions/${id}/evaluation`
      ),
    stats: () => get<SessionStatsResponse>("/api/sessions/stats"),
    archive: (id: number) => patch<Session>(`/api/sessions/${id}/archive`),
    unarchive: (id: number) => patch<Session>(`/api/sessions/${id}/unarchive`),
    archiveBulk: (ids: number[]) =>
      post<{ archived_count: number }>("/api/sessions/archive-bulk", { session_ids: ids }),
    unarchiveBulk: (ids: number[]) =>
      post<{ archived_count: number }>("/api/sessions/unarchive-bulk", { session_ids: ids }),
    educator: (id: number) =>
      get<{ evaluation_id: number; model_answer: string; gap_deepdives: string }>(`/api/evaluate/${id}/educator`),
    triggerEducator: (id: number) =>
      post<{ status: string }>(`/api/evaluate/${id}/educate`),
  },

  evaluate: {
    trigger: (sessionId: number) =>
      post<{ status: string; session_id?: number; evaluation_id?: number }>(
        `/api/evaluate/${sessionId}`
      ),
  },

  coach: {
    latest: () => get<CoachReview>("/api/coach/latest"),
    analyze: (force?: boolean) =>
      post<{ status: string }>(`/api/coach/analyze${force ? "?force=true" : ""}`),
  },

  traces: {
    recent: (limit = 50) => get<TraceEntry[]>(`/api/traces/recent?limit=${limit}`),
  },

  health: {
    check: () => get<{ status: string; db: boolean; anthropic: boolean; openai: boolean }>("/api/health"),
  },
};

export default api;
