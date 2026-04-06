import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import { apiClient } from "./client";
import type {
  User, Question,
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

// --- Auth mutations ---

export function useLogin() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: { email: string; password: string }) =>
      apiClient.post<User>("/api/auth/login", data),
    onSuccess: () => qc.invalidateQueries({ queryKey: createConnectQueryKey({ schema: getMe, input: {}, cardinality: undefined }) }),
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

// --- Account deletion ---

export function useDeleteAccount() {
  return useMutation({
    mutationFn: () => apiClient.delete("/api/auth/account"),
  });
}

// (Billing hooks migrated to ConnectRPC BillingService)
