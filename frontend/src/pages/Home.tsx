import { useState, useEffect } from "react";
import { Link, useNavigate } from "react-router-dom";
import type { Question, CoachReview, SessionStatsResponse } from "../types";
import api from "../api/client";
import CoachCard from "../components/CoachCard";
import ScoreDashboard from "../components/ScoreDashboard";
import QuestionList from "../components/QuestionList";
import TraceWidget from "../components/TraceWidget";

export default function Home() {
  const navigate = useNavigate();
  const [questions, setQuestions] = useState<Question[]>([]);
  const [stats, setStats] = useState<SessionStatsResponse | null>(null);
  const [coachReview, setCoachReview] = useState<CoachReview | null>(null);
  const [suggestedQuestion, setSuggestedQuestion] = useState<Question | null>(null);
  const [filterDifficulty, setFilterDifficulty] = useState("all");
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const [qs, st] = await Promise.all([
          api.questions.list(),
          api.sessions.stats(),
        ]);
        if (cancelled) return;
        setQuestions(qs);
        setStats(st);

        // Try to fetch latest coach review
        try {
          const review = await api.coach.latest();
          if (cancelled) return;
          setCoachReview(review);

          // Find suggested question
          if (review.suggested_question_id) {
            const sq = qs.find((q) => q.id === review.suggested_question_id) ?? null;
            setSuggestedQuestion(sq);
          }

          // Trigger new analysis if there are sessions newer than the review
          const sessions = await api.sessions.list();
          if (cancelled) return;
          const reviewTime = new Date(review.created_at).getTime();
          const hasNewSessions = sessions.some(
            (s) => s.status === "reviewed" && new Date(s.started_at).getTime() > reviewTime
          );
          if (hasNewSessions) {
            api.coach.analyze().catch(() => {});
          }
        } catch {
          // No coach review yet — trigger analysis if there are reviewed sessions
          try {
            const sessions = await api.sessions.list();
            if (cancelled) return;
            const hasReviewed = sessions.some((s) => s.status === "reviewed");
            if (hasReviewed) {
              api.coach.analyze().catch(() => {});
            }
          } catch {
            // no sessions yet
          }
        }
      } catch {
        // api error
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    load();
    return () => { cancelled = true; };
  }, []);

  async function handleStartSession(questionId: number) {
    const session = await api.sessions.create({
      question_id: questionId,
      timer_setting_sec: 2700,
      tts_enabled: true,
    });
    navigate(`/sessions/${session.id}`);
  }

  // Derived stats
  const sessionCount = stats?.question_stats.reduce((sum, qs) => sum + qs.session_count, 0) ?? 0;
  const avgScore = stats?.averages?.overall ?? 0;

  const weakestDimension = (() => {
    if (!stats?.averages) return "N/A";
    const dims = [
      { key: "Requirements", val: stats.averages.requirements },
      { key: "High-Level", val: stats.averages.highlevel },
      { key: "Deep Dive", val: stats.averages.deepdive },
      { key: "Scalability", val: stats.averages.scalability },
      { key: "Communication", val: stats.averages.communication },
    ];
    dims.sort((a, b) => a.val - b.val);
    return dims[0].key;
  })();

  if (loading) {
    return (
      <div className="container">
        <p className="text-muted">Loading...</p>
      </div>
    );
  }

  return (
    <div className="container">
      <div className="home-header">
        <h1>System Design Drill</h1>
        <Link to="/history" className="home-history-btn">
          History
        </Link>
      </div>

      {coachReview && (
        <CoachCard
          review={coachReview}
          suggestedQuestion={suggestedQuestion}
          onStartSession={handleStartSession}
        />
      )}

      {stats?.averages && (
        <>
          <div className="stats-row">
            <div className="stat-card card">
              <div className="stat-value">{sessionCount}</div>
              <div className="stat-label">Sessions</div>
            </div>
            <div className="stat-card card">
              <div className="stat-value">{avgScore.toFixed(1)}</div>
              <div className="stat-label">Avg Score</div>
            </div>
            <div className="stat-card card">
              <div className="stat-value">{weakestDimension}</div>
              <div className="stat-label">Weakest</div>
            </div>
          </div>

          <ScoreDashboard averages={stats.averages} />
        </>
      )}

      <QuestionList
        questions={questions}
        questionStats={stats?.question_stats ?? []}
        suggestedQuestionId={coachReview?.suggested_question_id ?? null}
        filterDifficulty={filterDifficulty}
        onFilterChange={setFilterDifficulty}
        onSelect={handleStartSession}
      />

      <TraceWidget />
    </div>
  );
}
