import { useState, useMemo } from "react";
import { Link, useSearchParams } from "react-router-dom";
import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
} from "recharts";
import { useSessions, useArchiveSessions } from "@/api/queries";
import type { Session, SessionStatus } from "@/api/types";
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

const IN_PROGRESS_STATUSES: SessionStatus[] = ["active", "completed", "evaluating"];
const REVIEWED_STATUSES: SessionStatus[] = ["reviewed", "evaluation_failed"];

function applyFilter(sessions: Session[], tab: FilterTab): Session[] {
  switch (tab) {
    case "all":
      return sessions.filter((s) => !s.archived_at);
    case "in_progress":
      return sessions.filter(
        (s) => !s.archived_at && IN_PROGRESS_STATUSES.includes(s.status),
      );
    case "reviewed":
      return sessions.filter(
        (s) => !s.archived_at && REVIEWED_STATUSES.includes(s.status),
      );
    case "archived":
      return sessions.filter((s) => s.archived_at);
    default:
      return sessions;
  }
}

function applySort(sessions: Session[], sort: SortOrder): Session[] {
  const copy = [...sessions];
  switch (sort) {
    case "newest":
      return copy.sort(
        (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
      );
    case "oldest":
      return copy.sort(
        (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      );
    // TODO: score_high / score_low require score_overall on Session, not yet
    // returned by the backend ListSessions endpoint. Sort by date as fallback.
    case "score_high":
    case "score_low":
      return copy.sort(
        (a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime(),
      );
    default:
      return copy;
  }
}

function formatDurationMinutes(minutes: number): string {
  return `${minutes}m`;
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
function ScoreTrendChart({ sessions }: { sessions: Session[] }) {
  const points = useMemo<TrendPoint[]>(() => {
    return sessions
      .filter((s) => s.status === "reviewed")
      .sort(
        (a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime(),
      )
      .map((s) => ({
        date: formatRelativeDate(s.created_at),
        title: s.question_title ?? "Session",
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
  if (status === "completed" || status === "evaluating") {
    return (
      <Badge variant="secondary" className="text-xs animate-pulse">
        Evaluating...
      </Badge>
    );
  }
  if (status === "evaluation_failed") {
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
  session: Session;
  checked: boolean;
  onToggle: (id: string) => void;
}

function SessionRow({ session, checked, onToggle }: SessionRowProps) {
  const isArchived = !!session.archived_at;

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
        aria-label={`Select session: ${session.question_title ?? session.id}`}
      />

      {/* Main content */}
      <Link
        to={`/sessions/${session.id}/overview`}
        className="min-w-0 flex-1 space-y-1 hover:opacity-80 transition-opacity"
      >
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium">
            {session.question_title ?? "Unknown question"}
          </span>
          <StatusBadge status={session.status} />
        </div>

        <div className="flex items-center gap-3 flex-wrap text-xs text-muted-foreground">
          <span>{formatRelativeDate(session.created_at)}</span>
          <span>{formatDurationMinutes(session.config_duration_minutes)}</span>
          {session.turn_count > 0 && (
            <span>{session.turn_count} turns</span>
          )}
        </div>
      </Link>

      {/* Score placeholder — shown only when status is reviewed */}
      {/* TODO: display actual scores once ListSessions returns score_overall */}
      {session.status === "reviewed" && (
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
  sessions: Session[];
  onClear: () => void;
}

function ActionBar({ selectedIds, sessions, onClear }: ActionBarProps) {
  const archive = useArchiveSessions();

  if (selectedIds.size === 0) return null;

  // Determine if selected sessions are mostly archived or not
  const selectedSessions = sessions.filter((s) => selectedIds.has(s.id));
  const allArchived = selectedSessions.every((s) => s.archived_at);
  const shouldArchive = !allArchived;

  function handleAction() {
    archive.mutate(
      { session_ids: Array.from(selectedIds), archive: shouldArchive },
      { onSuccess: onClear },
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
  const { data: allSessions = [], isLoading } = useSessions();

  const filtered = useMemo(() => applyFilter(allSessions, tab), [allSessions, tab]);
  const sorted = useMemo(() => applySort(filtered, sort), [filtered, sort]);

  // Reviewed sessions for trend chart (all, not tab-filtered)
  const reviewedSessions = useMemo(
    () => allSessions.filter((s) => s.status === "reviewed" && !s.archived_at),
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
