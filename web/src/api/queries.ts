import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useMutation as useConnectMutation } from "@connectrpc/connect-query";
import { createConnectQueryKey } from "@connectrpc/connect-query";
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
import { apiClient } from "./client";
import type {
  Question,
} from "./types";

// --- Questions ---

export function useCreateQuestion() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { title: string; prompt: string; difficulty: string; tags: string[] }) =>
      apiClient.post<Question>("/api/questions", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["questions"] }),
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
