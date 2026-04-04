import { useQuery } from "@connectrpc/connect-query";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import { useMe } from "@/api/queries";
import { ApiError } from "@/api/client";
import { useNavigate, useLocation } from "react-router-dom";
import { useEffect } from "react";

/**
 * Checks auth state without redirecting. For routes that render
 * different content based on auth (e.g., landing page vs dashboard).
 * Distinguishes 401 (not authenticated) from 5xx (server error).
 */
export function useOptionalAuth() {
  const { data: user, isLoading, error } = useMe();
  const isAuthError = error instanceof ApiError && error.status === 401;
  return { user, isLoading, isAuthenticated: !!user, isAuthError };
}

export function useRequireAuth() {
  const { data, isLoading, isError } = useQuery(getMe, {});
  const user = data?.user;
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    if (!isLoading && isError) {
      navigate(`/login?redirect=${encodeURIComponent(location.pathname)}`, {
        replace: true,
      });
    }
  }, [isLoading, isError, navigate, location.pathname]);

  return { user, isLoading, isAuthenticated: !!user };
}
