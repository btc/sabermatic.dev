import { useEffect, useRef } from "react";
import { Link, Outlet } from "react-router-dom";

import { PublicFooter } from "@/components/public-footer";
import { PublicHeader } from "@/components/public-header";
import { track } from "@/lib/analytics";
import { TabLink } from "@/pages/session/layout";
import { SessionDetailCtx } from "@/pages/session/session-detail-ctx";

// Module-scoped guard so sample_view fires at most once per page load —
// not on auth-state-flip remounts, not on browser-back to /sample, not on
// StrictMode dev double-invoke. Mirrors landing/index.tsx.
let sampleViewFired = false;

export default function SampleSession() {
  const fired = useRef(sampleViewFired);
  useEffect(() => {
    if (fired.current) return;
    fired.current = true;
    sampleViewFired = true;
    track({ event: "sample_view" });
  }, []);

  return (
    <SessionDetailCtx.Provider value={{ dataSource: "sample", sessionId: "sample" }}>
      <div className="min-h-screen bg-background text-foreground flex flex-col">
        <PublicHeader />

        <main className="flex-1 mx-auto max-w-5xl w-full px-4 py-6">
          <div className="border-b border-border -mx-4 px-4">
            <nav aria-label="Session tabs" className="flex items-end max-w-5xl mx-auto -mb-px">
              <TabLink to="/sample">Overview</TabLink>
              <TabLink to="/sample/transcript">Transcript</TabLink>
              <TabLink to="/sample/deep-dive">Deep Dive</TabLink>
            </nav>
          </div>

          <div className="pt-6">
            <Outlet />
          </div>

          <div className="border-t border-border mt-12 py-12 text-center">
            <p className="text-lg text-foreground mb-4">Want feedback on your own design?</p>
            <Link
              to="/signup"
              className="rounded-md bg-primary px-6 py-2.5 text-sm font-medium text-primary-foreground"
            >
              Sign up
            </Link>
          </div>
        </main>
        <PublicFooter />
      </div>
    </SessionDetailCtx.Provider>
  );
}
