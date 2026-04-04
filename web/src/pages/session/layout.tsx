import { createContext, useContext, useState, useEffect } from "react";
import { Outlet, NavLink, useParams, useNavigate, Navigate } from "react-router-dom";
import { useQuery } from "@connectrpc/connect-query";
import { skipToken } from "@tanstack/react-query";
import { getSession } from "@/pb/drill/v1/session-SessionService_connectquery";
import { getEvaluation } from "@/pb/drill/v1/evaluation-EvaluationService_connectquery";
import { SessionStatus } from "@/pb/drill/v1/session_pb";
import { cn } from "@/lib/utils";
import { WAITING_MESSAGES } from "@/lib/constants";

// ---------------------------------------------------------------------------
// Session detail context — allows child tabs to read from API or sample data
// ---------------------------------------------------------------------------

type DataSource = "api" | "sample";

interface SessionDetailContext {
  dataSource: DataSource;
  sessionId: string;
}

const SessionDetailCtx = createContext<SessionDetailContext>({
  dataSource: "api",
  sessionId: "",
});

export function useSessionDetail() {
  return useContext(SessionDetailCtx);
}

// ---------------------------------------------------------------------------
// Waiting state — shown when evaluation is still in progress
// ---------------------------------------------------------------------------

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
  if (!id) return <Navigate to="/" replace />;
  return (
    <SessionDetailCtx.Provider value={{ dataSource: "api", sessionId: id }}>
      <SessionLayoutInner id={id} />
    </SessionDetailCtx.Provider>
  );
}

export { SessionDetailCtx, TabLink };

function SessionLayoutInner({ id }: { id: string }) {
  const navigate = useNavigate();

  const { data: sessionResp } = useQuery(getSession, { id }, {
    refetchInterval: (query) => {
      const s = query.state.data?.session;
      if (s?.status === SessionStatus.COMPLETED || s?.status === SessionStatus.EVALUATING) return 3000;
      return false;
    },
  });
  const session = sessionResp?.session;
  const evalEnabled = session?.status === SessionStatus.REVIEWED || session?.status === SessionStatus.EVALUATION_FAILED;
  const { data: evaluation } = useQuery(getEvaluation, evalEnabled ? { sessionId: id } : skipToken);

  // Redirect active sessions to the interview page
  useEffect(() => {
    if (session?.status === SessionStatus.ACTIVE) {
      navigate(`/sessions/${id}/interview`, { replace: true });
    }
  }, [session?.status, id, navigate]);

  const status = session?.status;

  // Tabs are only fully enabled when reviewed or evaluation_failed
  const overviewEnabled = status === SessionStatus.REVIEWED || status === SessionStatus.EVALUATION_FAILED;
  const transcriptEnabled = status === SessionStatus.REVIEWED || status === SessionStatus.EVALUATION_FAILED || status === SessionStatus.COMPLETED || status === SessionStatus.EVALUATING;
  const deepDiveEnabled = status === SessionStatus.REVIEWED;

  // Show the evaluating spinner in the content area while processing
  const showWaiting = status === SessionStatus.COMPLETED || status === SessionStatus.EVALUATING;

  // Suppress void warning — evaluation is fetched for cache priming
  void evaluation;

  return (
    <div className="space-y-0">
      {/* Tab bar */}
      <div className="border-b border-border -mx-4 px-4">
        <nav aria-label="Session tabs" className="flex items-end max-w-5xl mx-auto -mb-px">
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
