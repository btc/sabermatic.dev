import { useMutation as useConnectMutation } from "@connectrpc/connect-query";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { useMutation, useQueryClient } from "@tanstack/react-query";

import {
  deleteAccount as deleteAccountMethod,
  forgotPassword as forgotPasswordMethod,
  login as loginMethod,
  logout as logoutMethod,
  resetPassword as resetPasswordMethod,
  signup as signupMethod,
  verifyEmail as verifyEmailMethod,
} from "@/pb/drill/v1/auth-AuthService_connectquery";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";

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
