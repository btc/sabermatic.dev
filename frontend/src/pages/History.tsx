import { useState, useEffect } from "react";
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

interface SessionRow {
  session: Session;
  evaluation: Evaluation | null;
  questionTitle: string;
}

export default function History() {
  const navigate = useNavigate();
  const [rows, setRows] = useState<SessionRow[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const [sessions, questions] = await Promise.all([
          api.sessions.list(),
          api.questions.list(),
        ]);
        if (cancelled) return;

        const qMap = new Map<number, Question>(questions.map((q) => [q.id, q]));

        // Fetch evaluations for completed/reviewed sessions
        const sessionRows: SessionRow[] = await Promise.all(
          sessions.map(async (s) => {
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

        if (cancelled) return;
        // Sort by started_at ascending (oldest first)
        sessionRows.sort(
          (a, b) =>
            new Date(a.session.started_at).getTime() -
            new Date(b.session.started_at).getTime()
        );
        setRows(sessionRows);
      } catch {
        // error loading
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    load();
    return () => { cancelled = true; };
  }, []);

  // Chart data: sessions with evaluations
  const chartData = rows
    .filter((r) => r.evaluation)
    .map((r, i) => ({
      name: `#${i + 1}`,
      score: r.evaluation!.score_overall,
      sessionId: r.session.id,
    }));

  if (loading) {
    return (
      <div className="container">
        <p className="text-muted">Loading history...</p>
      </div>
    );
  }

  return (
    <div className="container">
      <div className="home-header">
        <h1>Session History</h1>
        <Link to="/" className="home-history-btn">
          Home
        </Link>
      </div>

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
        {rows.length === 0 && (
          <p className="text-muted">No sessions yet. Start a practice interview from the home page!</p>
        )}
        {[...rows].reverse().map((r) => {
          const e = r.evaluation;
          const borderColor = e ? scoreColor(e.score_overall) : "var(--border)";
          const date = new Date(r.session.started_at).toLocaleDateString();
          const duration = r.session.duration_seconds
            ? `${Math.round(r.session.duration_seconds / 60)}m`
            : "--";

          return (
            <div
              key={r.session.id}
              className="session-list-item card"
              style={{ borderLeftColor: borderColor }}
              onClick={() => navigate(`/session/${r.session.id}`)}
            >
              <div className="session-item-top">
                <span className="session-item-title">{r.questionTitle}</span>
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

      <TraceWidget />
    </div>
  );
}
