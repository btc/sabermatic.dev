import { useNavigate } from "react-router-dom";
import type { Question, QuestionStats } from "../types";

interface QuestionListProps {
  questions: Question[];
  questionStats: QuestionStats[];
  suggestedQuestionId: number | null;
  filterDifficulty: string;
  onFilterChange: (difficulty: string) => void;
}

export default function QuestionList({
  questions,
  questionStats,
  suggestedQuestionId,
  filterDifficulty,
  onFilterChange,
}: QuestionListProps) {
  const navigate = useNavigate();
  const statsMap = new Map(questionStats.map((qs) => [qs.question_id, qs]));

  const filtered =
    filterDifficulty === "all"
      ? questions
      : questions.filter((q) => q.difficulty === filterDifficulty);

  return (
    <div className="question-list">
      <div className="question-list-header">
        <h2>Question Bank</h2>
        <div className="question-filter">
          {["all", "medium", "hard"].map((d) => (
            <button
              key={d}
              className={`question-filter-btn ${filterDifficulty === d ? "question-filter-btn-active" : ""}`}
              onClick={() => onFilterChange(d)}
            >
              {d === "all" ? "All" : d.charAt(0).toUpperCase() + d.slice(1)}
            </button>
          ))}
        </div>
      </div>
      <div className="question-items">
        {filtered.map((q) => {
          const stats = statsMap.get(q.id);
          const isSuggested = q.id === suggestedQuestionId;
          return (
            <div
              key={q.id}
              className={`question-item card ${isSuggested ? "question-item-suggested" : ""}`}
              onClick={() => navigate(`/interview/${q.id}`)}
            >
              <div className="question-item-top">
                <span className="question-item-title">
                  {isSuggested && <span className="coach-dot" />}
                  {q.title}
                </span>
                <span className={`tag ${q.difficulty === "hard" ? "badge-red" : "badge-orange"}`}>
                  {q.difficulty}
                </span>
              </div>
              <div className="question-item-meta">
                {stats && stats.session_count > 0 ? (
                  <>
                    <span>{stats.session_count} attempt{stats.session_count !== 1 ? "s" : ""}</span>
                    {stats.avg_overall != null && (
                      <span>Best: {stats.avg_overall.toFixed(1)}</span>
                    )}
                  </>
                ) : (
                  <span className="text-muted">No attempts yet</span>
                )}
                {q.tags.slice(0, 3).map((t) => (
                  <span key={t} className="tag">{t}</span>
                ))}
              </div>
            </div>
          );
        })}
        {filtered.length === 0 && (
          <p className="text-muted" style={{ padding: "1rem" }}>No questions match this filter.</p>
        )}
      </div>
    </div>
  );
}
