interface ScoreBarProps {
  label: string;
  score: number;
}

function scoreColor(score: number): string {
  if (score >= 4) return "var(--accent-green)";
  if (score >= 3) return "var(--accent-blue)";
  if (score >= 2) return "var(--accent-orange)";
  return "var(--accent-red)";
}

export default function ScoreBar({ label, score }: ScoreBarProps) {
  const pct = Math.min(Math.max((score / 5) * 100, 0), 100);
  const color = scoreColor(score);

  return (
    <div className="score-bar">
      <span className="score-bar-label">{label}</span>
      <div className="score-bar-track">
        <div
          className="score-bar-fill"
          style={{ width: `${pct}%`, background: color }}
        />
      </div>
      <span className="score-bar-value" style={{ color }}>
        {score.toFixed(1)}
      </span>
    </div>
  );
}

export { scoreColor };
