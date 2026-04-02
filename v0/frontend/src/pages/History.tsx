import { useState, useEffect, useCallback } from "react";
import { useNavigate, Link } from "react-router-dom";
import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  Cell,
} from "recharts";
import type { Session, Evaluation, Question } from "../types";
import api from "../api/client";
import { scoreColor } from "../components/ScoreBar";
import TraceWidget from "../components/TraceWidget";

type FilterMode = "active" | "archived" | "all";

interface SessionRow {
  session: Session;
  evaluation: Evaluation | null;
  questionTitle: string;
}

export default function History() {
  const navigate = useNavigate();
  const [rows, setRows] = useState<SessionRow[]>([]);
  const [loading, setLoading] = useState(true);
  const [filterMode, setFilterMode] = useState<FilterMode>("active");
  const [selected, setSelected] = useState<Set<number>>(new Set());

  const loadSessions = useCallback(async (mode: FilterMode) => {
    setLoading(true);
    setSelected(new Set());
    try {
      const includeArchived = mode === "archived" || mode === "all";
      const [sessions, questions] = await Promise.all([
        api.sessions.list(includeArchived),
        api.questions.list(),
      ]);

      const qMap = new Map<number, Question>(questions.map((q) => [q.id, q]));

      // Filter to only the requested set
      const filtered = mode === "archived"
        ? sessions.filter((s) => s.archived)
        : mode === "active"
        ? sessions.filter((s) => !s.archived)
        : sessions;

      const sessionRows: SessionRow[] = await Promise.all(
        filtered.map(async (s) => {
          let evaluation: Evaluation | null = null;
          if (s.status === "reviewed" || s.status === "evaluating") {
            try {
              const evalData = await api.sessions.evaluation(s.id);
              evaluation = evalData.evaluation;
            } catch {
              // no evaluation yet
            }
          }
          return {
            session: s,
            evaluation,
            questionTitle: qMap.get(s.question_id)?.title ?? `Session #${s.id}`,
          };
        })
      );

      // Sort by started_at ascending (oldest first), then reverse for display
      sessionRows.sort(
        (a, b) =>
          new Date(a.session.started_at).getTime() -
          new Date(b.session.started_at).getTime()
      );
      setRows(sessionRows);
    } catch {
      // error loading
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    loadSessions(filterMode);
  }, [filterMode, loadSessions]);

  function toggleSelect(id: number, e: React.MouseEvent) {
    e.stopPropagation();
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) {
        next.delete(id);
      } else {
        next.add(id);
      }
      return next;
    });
  }

  function selectAll() {
    setSelected(new Set(rows.map((r) => r.session.id)));
  }

  function clearSelection() {
    setSelected(new Set());
  }

  async function handleBulkAction() {
    const ids = Array.from(selected);
    try {
      if (filterMode === "archived") {
        await api.sessions.unarchiveBulk(ids);
      } else {
        await api.sessions.archiveBulk(ids);
      }
      await loadSessions(filterMode);
    } catch {
      // handle error silently
    }
  }

  // Chart data: sessions with evaluations (displayed in reverse order, so use reversed rows)
  const displayRows = [...rows].reverse();
  const chartData = rows
    .filter((r) => r.evaluation)
    .map((r, i) => ({
      name: `#${i + 1}`,
      score: r.evaluation!.score_overall,
      sessionId: r.session.id,
    }));

  return (
    <div className="container">
      <div className="home-header">
        <h1>Session History</h1>
        <Link to="/" className="home-history-btn">
          Home
        </Link>
      </div>

      {/* Filter toggle */}
      <div className="history-filter-row">
        {(["active", "archived", "all"] as FilterMode[]).map((mode) => (
          <button
            key={mode}
            className={`question-filter-btn${filterMode === mode ? " question-filter-btn-active" : ""}`}
            onClick={() => setFilterMode(mode)}
          >
            {mode.charAt(0).toUpperCase() + mode.slice(1)}
          </button>
        ))}
      </div>

      {/* Bulk action bar */}
      {selected.size > 0 && (
        <div className="history-action-bar">
          <span className="history-action-count">{selected.size} selected</span>
          <button className="history-action-btn" onClick={handleBulkAction}>
            {filterMode === "archived" ? "Unarchive" : "Archive"}
          </button>
          <button className="history-action-btn-ghost" onClick={selectAll}>
            Select All
          </button>
          <button className="history-action-btn-ghost" onClick={clearSelection}>
            Clear
          </button>
        </div>
      )}

      {loading ? (
        <p className="text-muted">Loading history...</p>
      ) : (
        <>
          {chartData.length > 0 && (
            <div className="card history-chart">
              <h2>Score Trend</h2>
              <ResponsiveContainer width="100%" height={200}>
                <BarChart data={chartData}>
                  <XAxis dataKey="name" stroke="#999" fontSize={12} />
                  <YAxis domain={[0, 5]} stroke="#999" fontSize={12} />
                  <Tooltip
                    contentStyle={{ background: "#2a2a4a", border: "1px solid #3a3a5a", color: "#e0e0e0" }}
                  />
                  <Bar dataKey="score" radius={[4, 4, 0, 0]}>
                    {chartData.map((entry, idx) => (
                      <Cell key={idx} fill={scoreColor(entry.score)} />
                    ))}
                  </Bar>
                </BarChart>
              </ResponsiveContainer>
            </div>
          )}

          <div className="session-list">
            {displayRows.length === 0 && (
              <p className="text-muted">
                {filterMode === "archived"
                  ? "No archived sessions."
                  : "No sessions yet. Start a practice interview from the home page!"}
              </p>
            )}
            {displayRows.map((r) => {
              const e = r.evaluation;
              const borderColor = e ? scoreColor(e.score_overall) : "var(--border)";
              const date = new Date(r.session.started_at).toLocaleDateString();
              const duration = r.session.duration_seconds
                ? `${Math.round(r.session.duration_seconds / 60)}m`
                : "--";
              const isSelected = selected.has(r.session.id);

              return (
                <div
                  key={r.session.id}
                  className={`session-list-item card${isSelected ? " session-list-item-selected" : ""}`}
                  style={{ borderLeftColor: borderColor }}
                  onClick={() => navigate(`/sessions/${r.session.id}`)}
                >
                  <div className="session-item-top">
                    <div className="session-item-left">
                      <input
                        type="checkbox"
                        className="session-checkbox"
                        checked={isSelected}
                        onClick={(e) => toggleSelect(r.session.id, e)}
                        onChange={() => {}}
                        aria-label={`Select session ${r.session.id}`}
                      />
                      <span className="session-item-title">{r.questionTitle}</span>
                    </div>
                    {e && (
                      <span
                        className="session-item-badge"
                        style={{ background: scoreColor(e.score_overall) }}
                      >
                        {e.score_overall.toFixed(1)}
                      </span>
                    )}
                  </div>
                  <div className="session-item-meta">
                    <span>{date}</span>
                    <span>{duration}</span>
                    {r.session.turn_count != null && (
                      <span>{r.session.turn_count} turns</span>
                    )}
                    {r.session.archived && (
                      <span className="session-archived-tag">archived</span>
                    )}
                  </div>
                  {e && (
                    <div className="session-item-chips">
                      {[
                        { label: "Req", score: e.score_requirements },
                        { label: "HL", score: e.score_highlevel },
                        { label: "DD", score: e.score_deepdive },
                        { label: "Sc", score: e.score_scalability },
                        { label: "Cm", score: e.score_communication },
                      ].map((d) => (
                        <span
                          key={d.label}
                          className="score-chip"
                          style={{ background: scoreColor(d.score), color: d.score >= 3 ? "#000" : "#fff" }}
                        >
                          {d.label} {d.score}
                        </span>
                      ))}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        </>
      )}

      <TraceWidget />
    </div>
  );
}
