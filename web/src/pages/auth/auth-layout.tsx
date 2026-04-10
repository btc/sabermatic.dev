import type { ReactNode } from "react";

import { BrandName } from "@/components/brand-name";

export function AuthLayout({ children }: { children: ReactNode }) {
  return (
    <div className="flex min-h-screen items-center justify-center bg-background px-4">
      <div className="w-full max-w-sm">
        <div className="mb-10 text-center">
          <BrandName className="text-base font-semibold tracking-wider text-muted-foreground" />
          <p className="mt-2 text-sm text-muted-foreground/70">system design, measured.</p>
        </div>
        {children}
      </div>
    </div>
  );
}
