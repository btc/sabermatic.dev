import { useState } from "react";
import type { CoachReview, Question } from "../types";
import api from "../api/client";

interface CoachCardProps {
  review: CoachReview;
  suggestedQuestion: Question | null;
  onStartSession?: (questionId: number) => void;
  onRefresh?: () => void;
}

export default function CoachCard({ review, suggestedQuestion, onStartSession, onRefresh }: CoachCardProps) {
  const [refreshing, setRefreshing] = useState(false);

  async function handleRefresh() {
    setRefreshing(true);
    try {
      await api.coach.analyze(true);
      onRefresh?.();
    } catch {
      // handle error silently
    } finally {
      setRefreshing(false);
    }
  }

  return (
    <div className="coach-card">
      <div className="coach-card-header">
        <span className="coach-dot" />
        <span className="coach-label">Coach</span>
        <button
          className="coach-refresh-btn"
          onClick={handleRefresh}
          disabled={refreshing}
          title="Refresh coach analysis"
        >
          {refreshing ? "..." : "Refresh"}
        </button>
      </div>
      <p className="coach-recommendation">{review.recommendation}</p>
      {suggestedQuestion && (
        <div className="coach-suggestion">
          <div className="coach-suggestion-info">
            <span className="coach-suggestion-title">{suggestedQuestion.title}</span>
            <div className="coach-suggestion-tags">
              {suggestedQuestion.tags.map((t) => (
                <span key={t} className="tag">{t}</span>
              ))}
              <span className="tag badge-orange">{suggestedQuestion.difficulty}</span>
            </div>
          </div>
          <button
            className="coach-start-btn"
            onClick={() => onStartSession?.(suggestedQuestion.id)}
          >
            Start This Session
          </button>
        </div>
      )}
    </div>
  );
}
