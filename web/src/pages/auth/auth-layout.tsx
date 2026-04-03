import type { ReactNode } from "react";

export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="w-full max-w-sm">
        <div className="mb-8 text-center text-sm font-semibold tracking-wider text-muted-foreground">
          DRILL
        </div>
        {children}
      </div>
    </div>
  );
}
