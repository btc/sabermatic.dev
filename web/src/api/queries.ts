import { useQuery, useMutation, createConnectQueryKey } from "@connectrpc/connect-query";
import { useMutation as useTanStackMutation, useQueryClient } from "@tanstack/react-query";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import {
  login as loginMethod,
  signup as signupMethod,
  logout as logoutMethod,
  forgotPassword as forgotPasswordMethod,
  resetPassword as resetPasswordMethod,
  verifyEmail as verifyEmailMethod,
  deleteAccount as deleteAccountMethod,
} from "@/pb/drill/v1/auth-AuthService_connectquery";
import {
  getSession,
  getTranscript,
} from "@/pb/drill/v1/session-SessionService_connectquery";
import {
  getEvaluation,
  retryEvaluation as retryEvaluationMethod,
} from "@/pb/drill/v1/evaluation-EvaluationService_connectquery";
import {
  getEducatorAnalysis,
  requestEducatorAnalysis as requestEducatorMethod,
} from "@/pb/drill/v1/educator-EducatorService_connectquery";
import { EducatorStatus } from "@/pb/drill/v1/educator_pb";
import { apiClient } from "./client";
import type { Question } from "./types";

// --- Questions (still REST until QuestionService gets CreateQuestion) ---

export function useCreateQuestion() {
  const qc = useQueryClient();
  return useTanStackMutation({
    mutationFn: (data: { title: string; prompt: string; difficulty: string; tags: string[] }) =>
      apiClient.post<Question>("/api/questions", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["questions"] }),
  });
}

// --- User ---

export function useMe(options?: { enabled?: boolean }) {
  return useQuery(getMe, {}, {
    retry: false,
    enabled: options?.enabled,
  });
}

// --- Session detail queries ---

export function useSession(id: string, options?: { enabled?: boolean }) {
  return useQuery(getSession, { id }, {
    enabled: options?.enabled ?? !!id,
  });
}

export function useTranscript(sessionId: string, enabled?: boolean) {
  return useQuery(getTranscript, { sessionId }, { enabled });
}

export function useEvaluation(sessionId: string, enabled?: boolean) {
  return useQuery(getEvaluation, { sessionId }, { enabled });
}

export function useEducator(sessionId: string, enabled?: boolean) {
  return useQuery(getEducatorAnalysis, { sessionId }, {
    enabled,
    refetchInterval: (query) => {
      const data = query.state.data;
      if (data?.analysis?.status === EducatorStatus.GENERATING) return 3000;
      return false;
    },
  });
}

export function useRetryEvaluation(sessionId: string) {
  const qc = useQueryClient();
  return useMutation(retryEvaluationMethod, {
    onSuccess: () => qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: getEvaluation, input: { sessionId }, cardinality: undefined }),
    }),
  });
}

export function useRequestEducator(sessionId: string) {
  const qc = useQueryClient();
  return useMutation(requestEducatorMethod, {
    onSuccess: () => qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: getEducatorAnalysis, input: { sessionId }, cardinality: undefined }),
    }),
  });
}

// --- Auth ---

export function useLogin() {
  const qc = useQueryClient();
  return useMutation(loginMethod, {
    onSuccess: () => qc.invalidateQueries({ queryKey: createConnectQueryKey({ schema: getMe, input: {}, cardinality: undefined }) }),
  });
}

export function useSignup() {
  return useMutation(signupMethod);
}

export function useLogout() {
  const qc = useQueryClient();
  return useMutation(logoutMethod, {
    onSuccess: () => qc.clear(),
  });
}

export function useForgotPassword() {
  return useMutation(forgotPasswordMethod);
}

export function useResetPassword() {
  return useMutation(resetPasswordMethod);
}

export function useVerifyEmail() {
  return useMutation(verifyEmailMethod);
}

export function useDeleteAccount() {
  return useMutation(deleteAccountMethod);
}
