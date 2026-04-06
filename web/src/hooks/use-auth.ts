import { useQuery } from "@connectrpc/connect-query";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import { useNavigate, useLocation } from "react-router-dom";
import { useEffect } from "react";

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
