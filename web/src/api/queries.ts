import { useQuery, useMutation, useQueryClient, type UseQueryOptions } from "@tanstack/react-query";
import { apiClient } from "./client";
import type {
  User, Question, Session, CreateSessionRequest,
  EvaluationResponse, EducatorAnalysis, CoachAnalysis, Usage,
  Message,
} from "./types";

// --- Auth ---

export function useMe() {
  return useQuery({
    queryKey: ["me"],
    queryFn: () => apiClient.get<User>("/api/me"),
    retry: false,
  });
}

export function useUsage() {
  return useQuery({
    queryKey: ["usage"],
    queryFn: () => apiClient.get<Usage>("/api/me/usage"),
  });
}

// --- Questions ---

export function useCreateQuestion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { title: string; prompt: string; difficulty: string; tags: string[] }) =>
      apiClient.post<Question>("/api/questions", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["questions"] }),
  });
}

// --- Sessions ---

export function useSessions(params?: { archived?: boolean; status?: string; sort?: string }) {
  const searchParams = new URLSearchParams();
  if (params?.archived !== undefined) searchParams.set("archived", String(params.archived));
  if (params?.status) searchParams.set("status", params.status);
  if (params?.sort) searchParams.set("sort", params.sort);
  const qs = searchParams.toString();
  const url = `/api/sessions${qs ? `?${qs}` : ""}`;

  return useQuery({
    queryKey: ["sessions", params],
    queryFn: () => apiClient.get<Session[]>(url),
  });
}

export function useSession(
  id: string,
  options?: Partial<Pick<UseQueryOptions<Session>, "refetchInterval">>,
) {
  return useQuery({
    queryKey: ["sessions", id],
    queryFn: () => apiClient.get<Session>(`/api/sessions/${id}`),
    enabled: !!id,
    ...options,
  });
}

export function useCreateSession() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateSessionRequest) =>
      apiClient.post<Session>("/api/sessions", data),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["sessions"] });
      qc.invalidateQueries({ queryKey: ["usage"] });
    },
  });
}

export function useArchiveSessions() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { session_ids: string[]; archive: boolean }) =>
      apiClient.post("/api/sessions/archive-bulk", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sessions"] }),
  });
}

export function useRetryEvaluation(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiClient.post(`/api/sessions/${sessionId}/evaluate`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["sessions", sessionId] }),
  });
}

// --- Evaluation ---

export function useEvaluation(sessionId: string, enabled = true) {
  return useQuery({
    queryKey: ["evaluation", sessionId],
    queryFn: () => apiClient.get<EvaluationResponse>(`/api/sessions/${sessionId}/evaluation`),
    enabled,
  });
}

// --- Transcript ---

export function useTranscript(sessionId: string) {
  return useQuery({
    queryKey: ["transcript", sessionId],
    queryFn: () => apiClient.get<Message[]>(`/api/sessions/${sessionId}/transcript`),
  });
}

// --- Educator ---

export function useEducator(sessionId: string, enabled = true) {
  return useQuery({
    queryKey: ["educator", sessionId],
    queryFn: () => apiClient.get<EducatorAnalysis>(`/api/sessions/${sessionId}/educator`),
    enabled,
    refetchInterval: (query) => {
      const data = query.state.data as EducatorAnalysis | undefined;
      if (data?.status === "generating") return 3000;
      return false;
    },
  });
}

export function useRequestEducator(sessionId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiClient.post(`/api/sessions/${sessionId}/educator`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["educator", sessionId] }),
  });
}

// --- Coach ---

export function useCoachLatest() {
  return useQuery({
    queryKey: ["coach"],
    queryFn: () => apiClient.get<CoachAnalysis | null>("/api/coach/latest"),
  });
}

export function useRequestCoachAnalysis() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiClient.post("/api/coach/analyze"),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["coach"] }),
  });
}

// --- Auth mutations ---

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { email: string; password: string }) =>
      apiClient.post<User>("/api/auth/login", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
}

export function useSignup() {
  return useMutation({
    mutationFn: (data: { email: string; password: string; display_name: string }) =>
      apiClient.post<User>("/api/auth/signup", data),
  });
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => apiClient.post("/api/auth/logout"),
    onSuccess: () => qc.clear(),
  });
}

// --- Auth mutations (password, email verification) ---

export function useForgotPassword() {
  return useMutation({
    mutationFn: (data: { email: string }) =>
      apiClient.post("/api/auth/forgot-password", data),
  });
}

export function useResetPassword() {
  return useMutation({
    mutationFn: (data: { token: string; password: string }) =>
      apiClient.post("/api/auth/reset-password", data),
  });
}

export function useVerifyEmail() {
  return useMutation({
    mutationFn: (data: { token: string }) =>
      apiClient.post("/api/auth/verify-email", data),
  });
}

// --- Profile ---

export function useUpdateProfile() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { display_name: string }) =>
      apiClient.patch("/api/me", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["me"] }),
  });
}

// --- Data export ---

export function useExportData() {
  // POST because the backend queues an async export job (side effect, not idempotent read)
  return useMutation({
    mutationFn: () => apiClient.post("/api/me/export"),
  });
}

// --- Account deletion ---

export function useDeleteAccount() {
  return useMutation({
    mutationFn: () => apiClient.delete("/api/auth/account"),
  });
}

// --- Billing ---

export type CheckoutRequest =
  | { type: "subscription"; plan: "pro" }
  | { type: "pack"; minutes: number };

export function useCheckout() {
  return useMutation({
    mutationFn: (body: CheckoutRequest) =>
      apiClient.post<{ url: string }>("/api/billing/checkout", body),
  });
}

export function usePortal() {
  return useMutation({
    mutationFn: () => apiClient.post<{ url: string }>("/api/billing/portal"),
  });
}
