import type { DimensionAverages } from "../types";
import ScoreBar from "./ScoreBar";

interface ScoreDashboardProps {
  averages: DimensionAverages;
}

const DIMENSIONS: { key: keyof DimensionAverages; label: string }[] = [
  { key: "requirements", label: "Requirements" },
  { key: "highlevel", label: "High-Level" },
  { key: "deepdive", label: "Deep Dive" },
  { key: "scalability", label: "Scalability" },
  { key: "communication", label: "Communication" },
];

export default function ScoreDashboard({ averages }: ScoreDashboardProps) {
  return (
    <div className="score-dashboard card">
      <h2>Dimension Scores</h2>
      {DIMENSIONS.map((d) => (
        <ScoreBar key={d.key} label={d.label} score={averages[d.key]} />
      ))}
    </div>
  );
}
