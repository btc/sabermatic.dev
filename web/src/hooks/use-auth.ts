import { useQuery } from "@connectrpc/connect-query";
import { ConnectError, Code } from "@connectrpc/connect";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import { useNavigate, useLocation } from "react-router-dom";
import { useEffect } from "react";

/**
 * Checks auth state without redirecting. For routes that render
 * different content based on auth (e.g., landing page vs dashboard).
 * Distinguishes unauthenticated from server error.
 */
export function useOptionalAuth() {
  const { data, isLoading, error } = useQuery(getMe, {}, { retry: false });
  const user = data?.user;
  const isAuthError = error instanceof ConnectError && error.code === Code.Unauthenticated;
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
