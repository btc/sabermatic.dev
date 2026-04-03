import { useState, useEffect } from "react";
import { Outlet, NavLink, useParams, useNavigate } from "react-router-dom";
import { useSession, useEvaluation } from "@/api/queries";
import { cn } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Waiting state — shown when evaluation is still in progress
// ---------------------------------------------------------------------------

const WAITING_MESSAGES = [
  "Reviewing your requirements gathering...",
  "Analyzing architecture decisions...",
  "Assessing depth of technical discussion...",
  "Evaluating scalability reasoning...",
  "Reviewing communication clarity...",
];

function EvaluatingView() {
  const [index, setIndex] = useState(0);

  useEffect(() => {
    const id = setInterval(() => {
      setIndex((i) => (i + 1) % WAITING_MESSAGES.length);
    }, 4000);
    return () => clearInterval(id);
  }, []);

  return (
    <div className="flex flex-col items-center justify-center py-24 gap-6 px-4">
      <div className="size-3 rounded-full bg-primary animate-pulse" />
      <p className="text-sm text-muted-foreground text-center max-w-xs">
        {WAITING_MESSAGES[index]}
      </p>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Tab nav
// ---------------------------------------------------------------------------

interface TabLinkProps {
  to: string;
  children: React.ReactNode;
  disabled?: boolean;
}

function TabLink({ to, children, disabled }: TabLinkProps) {
  if (disabled) {
    return (
      <span className="px-4 py-2.5 text-sm text-muted-foreground/50 cursor-default select-none">
        {children}
      </span>
    );
  }

  return (
    <NavLink
      to={to}
      end
      className={({ isActive }) =>
        cn(
          "px-4 py-2.5 text-sm transition-colors",
          isActive
            ? "border-b-2 border-primary text-foreground font-medium"
            : "text-muted-foreground hover:text-foreground",
        )
      }
    >
      {children}
    </NavLink>
  );
}

// ---------------------------------------------------------------------------
// Main layout
// ---------------------------------------------------------------------------

export default function SessionLayout() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const { data: session } = useSession(id!);
  const { data: evaluation } = useEvaluation(id!, session?.status === "reviewed" || session?.status === "evaluation_failed");

  // Redirect active sessions to the interview page
  useEffect(() => {
    if (session?.status === "active") {
      navigate(`/sessions/${id}/interview`, { replace: true });
    }
  }, [session?.status, id, navigate]);

  const status = session?.status;

  // Tabs are only fully enabled when reviewed or evaluation_failed
  const overviewEnabled = status === "reviewed" || status === "evaluation_failed";
  const transcriptEnabled = status === "reviewed" || status === "evaluation_failed" || status === "completed" || status === "evaluating";
  const deepDiveEnabled = status === "reviewed";

  // Show the evaluating spinner in the content area while processing
  const showWaiting = status === "completed" || status === "evaluating";

  // Suppress void warning — evaluation is fetched for cache priming
  void evaluation;

  return (
    <div className="space-y-0">
      {/* Tab bar */}
      <div className="border-b border-border -mx-4 px-4">
        <nav className="flex items-end max-w-5xl mx-auto -mb-px">
          <TabLink to={`/sessions/${id}/overview`} disabled={!overviewEnabled}>
            Overview
          </TabLink>
          <TabLink to={`/sessions/${id}/transcript`} disabled={!transcriptEnabled}>
            Transcript
          </TabLink>
          <TabLink to={`/sessions/${id}/deep-dive`} disabled={!deepDiveEnabled}>
            Deep Dive
          </TabLink>
        </nav>
      </div>

      {/* Content area */}
      <div className="pt-6">
        {showWaiting ? (
          <EvaluatingView />
        ) : (
          <Outlet />
        )}
      </div>
    </div>
  );
}
