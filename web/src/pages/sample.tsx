import { SessionDetailCtx } from "@/pages/session/layout";
import { TabLink } from "@/pages/session/layout";
import { Outlet, Link } from "react-router-dom";

export default function SampleSession() {
  return (
    <SessionDetailCtx.Provider value={{ dataSource: "sample", sessionId: "sample" }}>
      <div className="min-h-screen bg-background text-foreground">
        <header className="border-b border-border">
          <div className="mx-auto flex h-12 max-w-5xl items-center justify-between px-4">
            <Link to="/" className="text-sm font-semibold tracking-wider text-muted-foreground">
              sabermetric
            </Link>
            <Link
              to="/signup"
              className="rounded-md bg-primary px-4 py-1.5 text-xs font-medium text-primary-foreground"
            >
              Start practicing
            </Link>
          </div>
        </header>

        <main className="mx-auto max-w-5xl px-4 py-6">
          <div className="border-b border-border -mx-4 px-4">
            <nav className="flex items-end max-w-5xl mx-auto -mb-px">
              <TabLink to="/sample">Overview</TabLink>
              <TabLink to="/sample/transcript">Transcript</TabLink>
              <TabLink to="/sample/deep-dive">Deep Dive</TabLink>
            </nav>
          </div>

          <div className="pt-6">
            <Outlet />
          </div>
        </main>
      </div>
    </SessionDetailCtx.Provider>
  );
}
