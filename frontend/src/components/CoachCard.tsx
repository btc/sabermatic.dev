import type { CoachReview, Question } from "../types";

interface CoachCardProps {
  review: CoachReview;
  suggestedQuestion: Question | null;
  onStartSession?: (questionId: number) => void;
}

export default function CoachCard({ review, suggestedQuestion, onStartSession }: CoachCardProps) {
  return (
    <div className="coach-card">
      <div className="coach-card-header">
        <span className="coach-dot" />
        <span className="coach-label">Coach</span>
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
