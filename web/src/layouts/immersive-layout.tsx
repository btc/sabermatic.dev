import { Outlet } from "react-router-dom";

export function ImmersiveLayout() {
  return (
    <div className="flex h-screen flex-col bg-background text-foreground">
      <Outlet />
    </div>
  );
}
