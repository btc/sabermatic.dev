import { Link } from "react-router-dom";

import { useEvaluation, useRetryEvaluation, useSession } from "@/api/queries";
import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils";
import type { EvaluationScores } from "@/pb/drill/v1/evaluation_pb";
import { SessionStatus } from "@/pb/drill/v1/session_pb";

import { useSessionDetail } from "./session-detail-ctx";

// ---------------------------------------------------------------------------
// Score bar
// ---------------------------------------------------------------------------

interface ScoreBarProps {
  label: string;
  score: number;
  large?: boolean;
}

function scoreColor(score: number): string {
  if (score <= 2) return "text-muted-foreground";
  if (score === 3) return "text-warning";
  return "text-strength";
}

function ScoreBar({ label, score, large = false }: ScoreBarProps) {
  const fillPct = (score / 5) * 100;
  const color = scoreColor(score);

  return (
    <div className="flex items-center gap-3">
      <span
        className={cn(
          "shrink-0 text-right text-muted-foreground",
          large ? "w-20 text-sm font-medium" : "w-28 text-xs",
        )}
      >
        {label}
      </span>

      {/* Bar track */}
      <div className="flex-1 h-1.5 rounded-full bg-muted overflow-hidden">
        <div
          className="h-full rounded-full bg-current transition-all duration-500"
          style={{ width: `${fillPct}%` }}
        />
      </div>

      <span className={cn("shrink-0 tabular-nums font-medium", color, large ? "text-xl w-6" : "text-sm w-4")}>
        {score}
      </span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Scores section
// ---------------------------------------------------------------------------

const DIMENSION_LABELS: Record<string, string> = {
  requirements: "Requirements",
  architecture: "Architecture",
  deepDive: "Deep Dive",
  scalability: "Scalability",
  communication: "Communication",
};

interface ScoresSectionProps {
  scores: EvaluationScores;
}

function ScoresSection({ scores }: ScoresSectionProps) {
  const dimensions = Object.entries(DIMENSION_LABELS) as [keyof typeof DIMENSION_LABELS, string][];

  return (
    <div className="space-y-4">
      <h2 className="text-sm font-medium text-muted-foreground uppercase tracking-wider">
        Scores
      </h2>

      <div className="space-y-3">
        {dimensions.map(([key, label]) => (
          <ScoreBar key={key} label={label} score={scores[key as keyof EvaluationScores] as number} />
        ))}
      </div>

      <div className="pt-2 border-t border-border">
        <ScoreBar label="Overall" score={scores.overall} large />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Strength / gap cards
// ---------------------------------------------------------------------------

interface InsightCardProps {
  text: string;
  variant: "strength" | "gap";
  sessionId: string;
  dataSource: "api" | "sample";
}

function InsightCard({ text, variant, sessionId, dataSource }: InsightCardProps) {
  const borderColor = variant === "strength" ? "border-strength" : "border-gap";
  const transcriptPath = dataSource === "sample"
    ? "/sample/transcript"
    : `/sessions/${sessionId}/transcript`;

  return (
    <div className={cn("bg-card rounded-lg border border-border border-l-4 px-4 py-3 space-y-1.5", borderColor)}>
      <p className="text-sm leading-relaxed">{text}</p>
      <Link
        to={transcriptPath}
        className="text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        View in transcript
      </Link>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function Overview() {
  return <OverviewInner />;
}

function OverviewInner() {
  const { dataSource, sessionId } = useSessionDetail();

  const authSession = useSession(sessionId, { enabled: dataSource === "api" });
  const authEval = useEvaluation(sessionId, dataSource === "api");
  const sampleSession = useSampleSession({ enabled: dataSource === "sample" });
  const sampleEval = useSampleEvaluation({ enabled: dataSource === "sample" });

  const session = dataSource === "api" ? authSession.data?.session : sampleSession.data?.session;
  const evaluation = dataSource === "api" ? authEval.data?.evaluation : sampleEval.data?.evaluation;
  const retryMutation = useRetryEvaluation(sessionId);

  if (!session || !evaluation) {
    return (
      <div className="space-y-6 max-w-2xl">
        <Skeleton className="h-6 w-32" />
        <div className="space-y-3">
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-full" />
          <Skeleton className="h-4 w-full" />
        </div>
      </div>
    );
  }

  // Evaluation failed state
  if (session.status === SessionStatus.EVALUATION_FAILED) {
    return (
      <div className="flex flex-col items-center gap-4 py-16 text-center">
        <p className="text-sm text-muted-foreground">Evaluation could not be completed.</p>
        <Button
          variant="outline"
          onClick={() => retryMutation.mutate({ sessionId })}
          disabled={retryMutation.isPending}
          type="button"
        >
          {retryMutation.isPending ? "Retrying..." : "Retry evaluation"}
        </Button>
      </div>
    );
  }

  // Not yet reviewed — nothing to show in this tab
  if (session.status !== SessionStatus.REVIEWED) {
    return null;
  }

  const { scores, strengths, gaps, advice } = evaluation;

  return (
    <div className="space-y-10 max-w-2xl">
      {/* Scores */}
      {scores && <ScoresSection scores={scores} />}

      {/* Strengths */}
      {strengths && strengths.length > 0 && (
        <section className="space-y-3">
          <h2 className="text-sm font-medium text-muted-foreground uppercase tracking-wider">
            Strengths
          </h2>
          <div className="space-y-2">
            {strengths.map((text, i) => (
              <InsightCard key={i} text={text} variant="strength" sessionId={sessionId} dataSource={dataSource} />
            ))}
          </div>
        </section>
      )}

      {/* Gaps */}
      {gaps && gaps.length > 0 && (
        <section className="space-y-3">
          <h2 className="text-sm font-medium text-muted-foreground uppercase tracking-wider">
            Gaps
          </h2>
          <div className="space-y-2">
            {gaps.map((text, i) => (
              <InsightCard key={i} text={text} variant="gap" sessionId={sessionId} dataSource={dataSource} />
            ))}
          </div>
        </section>
      )}

      {/* Advice */}
      {advice && (
        <section className="space-y-3">
          <h2 className="text-sm font-medium text-muted-foreground uppercase tracking-wider">
            Advice
          </h2>
          <p className="text-sm leading-relaxed text-foreground">{advice}</p>
        </section>
      )}
    </div>
  );
}
