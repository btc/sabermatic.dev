import { Link,Outlet } from "react-router-dom";

import { PublicHeader } from "@/components/public-header";
import { SessionDetailCtx } from "@/pages/session/layout";
import { TabLink } from "@/pages/session/layout";

export default function SampleSession() {
  return (
    <SessionDetailCtx.Provider value={{ dataSource: "sample", sessionId: "sample" }}>
      <div className="min-h-screen bg-background text-foreground">
        <PublicHeader />

        <main className="mx-auto max-w-5xl px-4 py-6">
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
      </div>
    </SessionDetailCtx.Provider>
  );
}
