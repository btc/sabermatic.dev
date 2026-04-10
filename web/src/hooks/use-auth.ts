import { useQuery } from "@connectrpc/connect-query";
import { useEffect } from "react";
import { useLocation, useNavigate } from "react-router-dom";

import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";

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
