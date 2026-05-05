import type { ReactNode } from "react";
import { Link } from "react-router-dom";

import { BrandName } from "@/components/brand-name";
import { PublicFooter } from "@/components/public-footer";
import { useNoindex } from "@/hooks/use-noindex";

export function AuthLayout({ children }: { children: ReactNode }) {
  // Every auth-flow page is hidden from search engines. Hoisting here keeps
  // login/signup/forgot-password/reset-password/verify-email in lockstep with
  // robots.txt without per-page hook calls drifting out of sync.
  useNoindex();
  return (
    <div className="flex min-h-screen flex-col bg-background">
      <div className="flex flex-1 items-center justify-center px-4">
        <div className="w-full max-w-sm">
          <div className="mb-10 text-center">
            <Link to="/" className="inline-block">
              <BrandName className="text-base font-semibold tracking-wider text-muted-foreground" />
            </Link>
            <p className="mt-2 text-sm text-muted-foreground/70">system design, measured.</p>
          </div>
          {children}
        </div>
      </div>
      <PublicFooter />
    </div>
  );
}
