import { useEffect, useState } from "react";

import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import type { EvaluationScores } from "@/pb/drill/v1/evaluation_pb";

const DIMENSIONS = [
  { key: "requirements", label: "Requirements" },
  { key: "architecture", label: "Architecture" },
  { key: "deepDive", label: "Deep Dive" },
  { key: "scalability", label: "Scalability" },
  { key: "communication", label: "Communication" },
] as const;

function AnimatedBar({ value, delay, animate }: { value: number; delay: number; animate: boolean }) {
  const [width, setWidth] = useState(0);

  useEffect(() => {
    if (!animate) return;
    const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const timer = setTimeout(
      () => setWidth((value / 5) * 100),
      reducedMotion ? 0 : delay,
    );
    return () => clearTimeout(timer);
  }, [animate, value, delay]);

  return (
    <div className="h-2 w-full rounded-full bg-muted">
      <div
        className="h-2 rounded-full bg-primary transition-all duration-700 ease-out motion-reduce:transition-none"
        style={{ width: `${width}%` }}
      />
    </div>
  );
}

export function Scoring() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evalData } = useSampleEvaluation();
  const { data: sessionData } = useSampleSession();

  if (!evalData?.evaluation?.scores) return null;
  const scores = evalData.evaluation.scores;

  return (
    <section ref={ref} aria-labelledby="scoring-heading" className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl space-y-4 text-center">
        <h2 id="scoring-heading" className="text-3xl font-light text-foreground sm:text-4xl">
          Scored across five dimensions
        </h2>
        {sessionData?.session?.questionTitle && (
          <p className="text-sm text-muted-foreground">
            From a session on: {sessionData.session.questionTitle}
          </p>
        )}
      </div>
      <div className="w-full max-w-lg space-y-6">
        {DIMENSIONS.map((dim, i) => (
          <div key={dim.key} className="space-y-1.5">
            <div className="flex items-center justify-between text-sm">
              <span className="text-muted-foreground">{dim.label}</span>
              <span className="font-medium text-foreground">
                {isVisible ? scores[dim.key as keyof EvaluationScores] as number : 0}/5
              </span>
            </div>
            <AnimatedBar
              value={scores[dim.key as keyof EvaluationScores] as number}
              delay={i * 150}
              animate={isVisible}
            />
          </div>
        ))}
        {/* Overall score — larger, last */}
        <div className="border-t border-border pt-6 space-y-1.5">
          <div className="flex items-center justify-between">
            <span className="text-lg text-foreground font-medium">Overall</span>
            <span className="text-2xl font-semibold text-primary">
              {isVisible ? scores.overall : 0}/5
            </span>
          </div>
          <AnimatedBar
            value={scores.overall}
            delay={DIMENSIONS.length * 150}
            animate={isVisible}
          />
        </div>
      </div>
    </section>
  );
}
