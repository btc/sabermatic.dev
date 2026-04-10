import { Outlet } from "react-router-dom";

import { useRequireAuth } from "@/hooks/use-auth";

export function ImmersiveLayout() {
  const { isLoading, isAuthenticated } = useRequireAuth();

  if (isLoading || !isAuthenticated) {
    return (
      <div className="flex h-screen items-center justify-center text-muted-foreground bg-background">
        Loading...
      </div>
    );
  }

  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <Outlet />
    </div>
  );
}
