import { useState, useMemo } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { useQuery, useMutation } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { listSessions, archiveSessions } from "@/pb/drill/v1/session-SessionService_connectquery";
import { SessionStatus } from "@/pb/drill/v1/session_pb";
import type { SessionSummary } from "@/pb/drill/v1/session_pb";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn, formatRelativeDate } from "@/lib/utils";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

const VALID_TABS = ["all", "in_progress", "reviewed", "archived"] as const;
const VALID_SORTS = ["newest", "oldest", "score_high", "score_low"] as const;
type FilterTab = (typeof VALID_TABS)[number];
type SortOrder = (typeof VALID_SORTS)[number];

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// Helper to get sortable timestamp from proto Timestamp
function getCreateTimeMs(s: SessionSummary): number {
  return s.createTime ? Number(s.createTime.seconds) * 1000 + s.createTime.nanos / 1_000_000 : 0;
}

const IN_PROGRESS_STATUSES: SessionStatus[] = [SessionStatus.ACTIVE, SessionStatus.COMPLETED, SessionStatus.EVALUATING];
const REVIEWED_STATUSES: SessionStatus[] = [SessionStatus.REVIEWED, SessionStatus.EVALUATION_FAILED];

function applyFilter(sessions: SessionSummary[], tab: FilterTab): SessionSummary[] {
  switch (tab) {
    case "all":
      return sessions.filter((s) => !s.archiveTime);
    case "in_progress":
      return sessions.filter(
        (s) => !s.archiveTime && IN_PROGRESS_STATUSES.includes(s.status),
      );
    case "reviewed":
      return sessions.filter(
        (s) => !s.archiveTime && REVIEWED_STATUSES.includes(s.status),
      );
    case "archived":
      return sessions.filter((s) => !!s.archiveTime);
    default:
      return sessions;
  }
}

function applySort(sessions: SessionSummary[], sort: SortOrder): SessionSummary[] {
  const copy = [...sessions];
  switch (sort) {
    case "newest":
      return copy.sort((a, b) => getCreateTimeMs(b) - getCreateTimeMs(a));
    case "oldest":
      return copy.sort((a, b) => getCreateTimeMs(a) - getCreateTimeMs(b));
    // TODO: score_high / score_low require score_overall on Session, not yet
    // returned by the backend ListSessions endpoint. Sort by date as fallback.
    case "score_high":
    case "score_low":
      return copy.sort((a, b) => getCreateTimeMs(b) - getCreateTimeMs(a));
    default:
      return copy;
  }
}

function formatDurationMinutes(minutes: number): string {
  return `${minutes}m`;
}

function formatTimestamp(ts: { seconds: bigint; nanos: number } | undefined): string {
  if (!ts) return "";
  return new Date(Number(ts.seconds) * 1000).toISOString();
}

// ---------------------------------------------------------------------------
// Score trend chart
// ---------------------------------------------------------------------------

interface TrendPoint {
  date: string;
  title: string;
  score: number;
  sessionId: string;
}

interface TrendTooltipProps {
  active?: boolean;
  payload?: Array<{ payload: TrendPoint }>;
}

function TrendTooltip({ active, payload }: TrendTooltipProps) {
  if (!active || !payload?.length) return null;
  const point = payload[0]!.payload;
  return (
    <div className="rounded-lg border border-border bg-popover text-popover-foreground px-3 py-2 text-xs shadow-lg">
      <p className="font-medium">{point.title}</p>
      <p className="text-muted-foreground">{point.date}</p>
      <p className="mt-1 font-semibold text-amber-400">{point.score}/5</p>
    </div>
  );
}

// TODO: score_overall is not yet returned by ListSessions. The trend chart
// will render once the backend enriches session list rows with evaluation scores.
function ScoreTrendChart({ sessions }: { sessions: SessionSummary[] }) {
  const points = useMemo<TrendPoint[]>(() => {
    return sessions
      .filter((s) => s.status === SessionStatus.REVIEWED)
      .sort((a, b) => getCreateTimeMs(a) - getCreateTimeMs(b))
      .map((s) => ({
        date: formatRelativeDate(formatTimestamp(s.createTime)),
        title: s.questionTitle || "Session",
        // score_overall not yet on Session type — placeholder
        score: 0,
        sessionId: s.id,
      }));
  }, [sessions]);

  // Only render when scores are real (non-zero). Deferred until backend provides scores.
  const hasScores = points.some((p) => p.score > 0);
  if (!hasScores) return null;

  return (
    <div className="rounded-lg border border-border bg-card px-4 py-5">
      <h2 className="mb-4 text-sm font-medium text-muted-foreground uppercase tracking-wider">
        Score Trend
      </h2>
      <div className="h-48">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={points} margin={{ top: 4, right: 4, bottom: 4, left: 0 }}>
            <XAxis
              dataKey="date"
              tick={{ fontSize: 11, fill: "hsl(var(--muted-foreground))" }}
              axisLine={false}
              tickLine={false}
            />
            <YAxis
              domain={[0, 5]}
              ticks={[1, 2, 3, 4, 5]}
              tick={{ fontSize: 11, fill: "hsl(var(--muted-foreground))" }}
              axisLine={false}
              tickLine={false}
              width={20}
            />
            <Tooltip content={<TrendTooltip />} />
            <Line
              type="monotone"
              dataKey="score"
              stroke="hsl(32 95% 44%)"
              strokeWidth={2}
              dot={{ fill: "hsl(32 95% 44%)", r: 3, strokeWidth: 0 }}
              activeDot={{ r: 5, fill: "hsl(32 95% 44%)", strokeWidth: 0 }}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Status badge
// ---------------------------------------------------------------------------

function StatusBadge({ status }: { status: SessionStatus }) {
  if (status === SessionStatus.COMPLETED || status === SessionStatus.EVALUATING) {
    return (
      <Badge variant="secondary" className="text-xs animate-pulse">
        Evaluating...
      </Badge>
    );
  }
  if (status === SessionStatus.EVALUATION_FAILED) {
    return (
      <Badge variant="destructive" className="text-xs">
        Failed
      </Badge>
    );
  }
  return null;
}

// ---------------------------------------------------------------------------
// Session row
// ---------------------------------------------------------------------------

interface SessionRowProps {
  session: SessionSummary;
  checked: boolean;
  onToggle: (id: string) => void;
}

function SessionRow({ session, checked, onToggle }: SessionRowProps) {
  const isArchived = !!session.archiveTime;

  return (
    <div
      className={cn(
        "flex items-start gap-3 rounded-lg border border-border bg-card px-4 py-3 transition-opacity",
        isArchived && "opacity-50",
      )}
    >
      {/* Checkbox */}
      <input
        type="checkbox"
        checked={checked}
        onChange={() => onToggle(session.id)}
        className="mt-0.5 h-4 w-4 shrink-0 cursor-pointer accent-primary"
        aria-label={`Select session: ${session.questionTitle || session.id}`}
      />

      {/* Main content */}
      <Link
        to={`/sessions/${session.id}/overview`}
        className="min-w-0 flex-1 space-y-1 hover:opacity-80 transition-opacity"
      >
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium">
            {session.questionTitle || "Unknown question"}
          </span>
          <StatusBadge status={session.status} />
        </div>

        <div className="flex items-center gap-3 flex-wrap text-xs text-muted-foreground">
          <span>{formatRelativeDate(formatTimestamp(session.createTime))}</span>
          <span>{formatDurationMinutes(session.configDurationMinutes)}</span>
          {session.turnCount > 0 && (
            <span>{session.turnCount} turns</span>
          )}
        </div>
      </Link>

      {/* Score placeholder — shown only when status is reviewed */}
      {/* TODO: display actual scores once ListSessions returns score_overall */}
      {session.status === SessionStatus.REVIEWED && (
        <div className="shrink-0 text-right">
          <span className="text-xs text-muted-foreground">—/5</span>
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Filter tabs
// ---------------------------------------------------------------------------

const TABS: { id: FilterTab; label: string }[] = [
  { id: "all", label: "All" },
  { id: "in_progress", label: "In Progress" },
  { id: "reviewed", label: "Reviewed" },
  { id: "archived", label: "Archived" },
];

interface FilterTabsProps {
  active: FilterTab;
  onChange: (tab: FilterTab) => void;
}

function FilterTabs({ active, onChange }: FilterTabsProps) {
  return (
    <div className="flex items-center rounded-lg border border-border bg-muted/40 p-0.5 gap-0.5">
      {TABS.map((tab) => (
        <button
          key={tab.id}
          type="button"
          onClick={() => onChange(tab.id)}
          className={cn(
            "rounded-md px-3 py-1.5 text-sm font-medium transition-colors cursor-pointer",
            active === tab.id
              ? "bg-background text-foreground shadow-sm"
              : "text-muted-foreground hover:text-foreground",
          )}
        >
          {tab.label}
        </button>
      ))}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Sort dropdown
// ---------------------------------------------------------------------------

interface SortSelectProps {
  value: SortOrder;
  onChange: (v: SortOrder) => void;
}

function SortSelect({ value, onChange }: SortSelectProps) {
  return (
    <select
      value={value}
      onChange={(e) => onChange(e.target.value as SortOrder)}
      className="h-8 rounded-lg border border-input bg-transparent px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/50"
    >
      <option value="newest">Newest first</option>
      <option value="oldest">Oldest first</option>
      <option value="score_high">Highest score</option>
      <option value="score_low">Lowest score</option>
    </select>
  );
}

// ---------------------------------------------------------------------------
// Bulk action bar
// ---------------------------------------------------------------------------

interface ActionBarProps {
  selectedIds: Set<string>;
  sessions: SessionSummary[];
  onClear: () => void;
}

function ActionBar({ selectedIds, sessions, onClear }: ActionBarProps) {
  const qc = useQueryClient();
  const archive = useMutation(archiveSessions, {
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: createConnectQueryKey({ schema: listSessions, input: {}, cardinality: undefined }) });
    },
  });

  if (selectedIds.size === 0) return null;

  // Determine if selected sessions are mostly archived or not
  const selectedSessions = sessions.filter((s) => selectedIds.has(s.id));
  const allArchived = selectedSessions.every((s) => !!s.archiveTime);
  const shouldArchive = !allArchived;

  function handleAction() {
    const count = selectedIds.size;
    archive.mutate(
      { sessionIds: Array.from(selectedIds), archive: shouldArchive },
      {
        onSuccess: () => {
          if (shouldArchive) {
            toast.success(`${count} session${count === 1 ? "" : "s"} archived`);
          }
          onClear();
        },
      },
    );
  }

  return (
    <div className="flex items-center justify-between rounded-lg border border-border bg-card px-4 py-2.5">
      <span className="text-sm text-muted-foreground">
        {selectedIds.size} selected
      </span>
      <div className="flex items-center gap-2">
        <Button
          variant="ghost"
          size="sm"
          onClick={onClear}
          disabled={archive.isPending}
        >
          Cancel
        </Button>
        <Button
          variant="outline"
          size="sm"
          onClick={handleAction}
          disabled={archive.isPending}
        >
          {archive.isPending
            ? "Working..."
            : shouldArchive
              ? "Archive selected"
              : "Unarchive selected"}
        </Button>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// History page
// ---------------------------------------------------------------------------

export default function History() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [selectedIds, setSelectedIds] = useState<Set<string>>(new Set());

  // URL-synced filter state — validate against known values to avoid illegal casts
  const rawTab = searchParams.get("tab");
  const tab: FilterTab = VALID_TABS.includes(rawTab as FilterTab) ? (rawTab as FilterTab) : "all";
  const rawSort = searchParams.get("sort");
  const sort: SortOrder = VALID_SORTS.includes(rawSort as SortOrder) ? (rawSort as SortOrder) : "newest";

  function setTab(next: FilterTab) {
    setSelectedIds(new Set());
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("tab", next);
      return p;
    });
  }

  function setSort(next: SortOrder) {
    setSearchParams((prev) => {
      const p = new URLSearchParams(prev);
      p.set("sort", next);
      return p;
    });
  }

  // Fetch all sessions (backend doesn't filter server-side yet)
  const { data: sessionsResp, isLoading } = useQuery(listSessions, {});
  const allSessions = useMemo(() => sessionsResp?.sessions ?? [], [sessionsResp]);

  const filtered = useMemo(() => applyFilter(allSessions, tab), [allSessions, tab]);
  const sorted = useMemo(() => applySort(filtered, sort), [filtered, sort]);

  // Reviewed sessions for trend chart (all, not tab-filtered)
  const reviewedSessions = useMemo(
    () => allSessions.filter((s) => s.status === SessionStatus.REVIEWED && !s.archiveTime),
    [allSessions],
  );

  // Selection helpers
  function toggleSelect(id: string) {
    setSelectedIds((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  function clearSelection() {
    setSelectedIds(new Set());
  }

  if (isLoading) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-6 w-24" />
        <div className="space-y-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
        </div>
      </div>
    );
  }

  const hasAnySessions = allSessions.length > 0;

  return (
    <div className="space-y-6">
      <h1 className="text-base font-medium">History</h1>

      {/* Score trend chart — only when there are reviewed sessions */}
      {reviewedSessions.length > 0 && (
        <ScoreTrendChart sessions={reviewedSessions} />
      )}

      {/* Filters row */}
      {hasAnySessions && (
        <div className="flex flex-wrap items-center justify-between gap-3">
          <FilterTabs active={tab} onChange={setTab} />
          <SortSelect value={sort} onChange={setSort} />
        </div>
      )}

      {/* Bulk action bar */}
      <ActionBar
        selectedIds={selectedIds}
        sessions={allSessions}
        onClear={clearSelection}
      />

      {/* Session list */}
      {sorted.length === 0 ? (
        <div className="py-16 text-center">
          {!hasAnySessions ? (
            <p className="text-sm text-muted-foreground">
              No sessions yet.{" "}
              <Link to="/" className="text-foreground underline underline-offset-4 hover:text-muted-foreground">
                Start your first practice from the home page.
              </Link>
            </p>
          ) : (
            <p className="text-sm text-muted-foreground">
              No sessions match this filter.
            </p>
          )}
        </div>
      ) : (
        <div className="space-y-2">
          {sorted.map((session) => (
            <SessionRow
              key={session.id}
              session={session}
              checked={selectedIds.has(session.id)}
              onToggle={toggleSelect}
            />
          ))}
        </div>
      )}
    </div>
  );
}
