import { useMe } from "@/api/queries";
import { useNavigate, useLocation } from "react-router-dom";
import { useEffect } from "react";

export function useRequireAuth() {
  const { data: user, isLoading, isError } = useMe();
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
