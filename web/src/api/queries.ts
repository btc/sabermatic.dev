import {
  createConnectQueryKey,
  useMutation as useConnectMutation,
  useQuery,
} from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";

import {
  deleteAccount as deleteAccountMethod,
  forgotPassword as forgotPasswordMethod,
  login as loginMethod,
  logout as logoutMethod,
  resetPassword as resetPasswordMethod,
  signup as signupMethod,
  verifyEmail as verifyEmailMethod,
} from "@/pb/drill/v1/auth-AuthService_connectquery";
import { EducatorStatus } from "@/pb/drill/v1/educator_pb";
import {
  getEducatorAnalysis,
  requestEducatorAnalysis as requestEducatorMethod,
} from "@/pb/drill/v1/educator-EducatorService_connectquery";
import {
  getEvaluation,
  retryEvaluation as retryEvaluationMethod,
} from "@/pb/drill/v1/evaluation-EvaluationService_connectquery";
import {
  createQuestion as createQuestionMethod,
  listQuestions,
} from "@/pb/drill/v1/question-QuestionService_connectquery";
import {
  getSession,
  getTranscript,
} from "@/pb/drill/v1/session-SessionService_connectquery";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";

// --- Questions ---

export function useCreateQuestion() {
  const qc = useQueryClient();
  return useConnectMutation(createQuestionMethod, {
    onSuccess: () =>
      qc.invalidateQueries({
        queryKey: createConnectQueryKey({ schema: listQuestions, input: {}, cardinality: undefined }),
      }),
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
  return useConnectMutation(retryEvaluationMethod, {
    onSuccess: () => qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: getEvaluation, input: { sessionId }, cardinality: undefined }),
    }),
  });
}

export function useRequestEducator(sessionId: string) {
  const qc = useQueryClient();
  return useConnectMutation(requestEducatorMethod, {
    onSuccess: () => qc.invalidateQueries({
      queryKey: createConnectQueryKey({ schema: getEducatorAnalysis, input: { sessionId }, cardinality: undefined }),
    }),
  });
}

// --- Auth ---

export function useLogin() {
  const qc = useQueryClient();
  return useConnectMutation(loginMethod, {
    onSuccess: () => qc.invalidateQueries({ queryKey: createConnectQueryKey({ schema: getMe, input: {}, cardinality: undefined }) }),
  });
}

export function useSignup() {
  return useConnectMutation(signupMethod);
}

export function useLogout() {
  const qc = useQueryClient();
  return useConnectMutation(logoutMethod, {
    onSuccess: () => qc.clear(),
  });
}

export function useForgotPassword() {
  return useConnectMutation(forgotPasswordMethod);
}

export function useResetPassword() {
  return useConnectMutation(resetPasswordMethod);
}

export function useVerifyEmail() {
  return useConnectMutation(verifyEmailMethod);
}

export function useDeleteAccount() {
  return useConnectMutation(deleteAccountMethod);
}
