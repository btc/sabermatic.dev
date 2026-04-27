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

function DimRow({
  label,
  value,
  animate,
  delay,
  isOverall = false,
}: {
  label: string;
  value: number;
  animate: boolean;
  delay: number;
  isOverall?: boolean;
}) {
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

  const barThickness = isOverall ? "h-[10px] rounded-[5px]" : "h-1.5 rounded-sm";
  const fillBg = isOverall ? "bg-primary" : "bg-foreground";
  const valueClass = isOverall ? "text-[22px] text-primary" : "text-[13px] text-foreground";
  const rowBorder = isOverall ? "border-t-2 border-foreground mt-2 pt-[22px]" : "border-b border-border";

  return (
    <div className={`grid grid-cols-[160px_1fr_80px] items-center gap-5 py-[18px] last:border-b-0 ${rowBorder}`}>
      <div className="text-sm font-medium">{label}</div>
      <div className={`relative overflow-hidden bg-muted ${barThickness}`}>
        <div
          className={`absolute inset-y-0 left-0 transition-[width] duration-1000 ease-[cubic-bezier(.2,.7,.2,1)] motion-reduce:transition-none ${fillBg} ${barThickness}`}
          style={{ width: `${width}%` }}
        />
      </div>
      <div className={`text-right font-mono font-medium ${valueClass}`}>
        {isOverall ? value.toFixed(1) : `${value} / 5`}
      </div>
    </div>
  );
}

export function Scoring() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evalData } = useSampleEvaluation();
  const { data: sessionData } = useSampleSession();

  if (!evalData?.evaluation?.scores) return null;
  const scores = evalData.evaluation.scores as EvaluationScores;

  const overall =
    (scores.requirements + scores.architecture + scores.deepDive + scores.scalability + scores.communication) / 5;

  const questionTitle = sessionData?.session?.questionTitle ?? "Sample Session";

  return (
    <section ref={ref} id="scoring" className="py-28 px-5 sm:px-6 lg:px-10">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 02 — Evaluation
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Scored across five dimensions.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Graded the way an interviewer would. Requirements, architecture, depth, scale, communication. Not vibes.
          </p>
        </header>

        <div className="overflow-hidden rounded-xl border border-border bg-card">
          <div className="flex items-center justify-between border-b border-border px-6 py-[18px] font-mono text-[11px] uppercase tracking-[0.1em] text-muted-foreground">
            <span>Sample session · {questionTitle} · 30 min</span>
            <span>Rubric v2.1</span>
          </div>
          <div className="px-6 py-3">
            {DIMENSIONS.map((dim, i) => (
              <DimRow
                key={dim.key}
                label={dim.label}
                value={scores[dim.key]}
                animate={isVisible}
                delay={i * 80}
              />
            ))}
            <DimRow label="Overall" value={overall} animate={isVisible} delay={DIMENSIONS.length * 80} isOverall />
          </div>
        </div>
      </div>
    </section>
  );
}
