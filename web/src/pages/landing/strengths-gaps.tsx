import type { ReactNode } from "react";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleEvaluation } from "@/api/sample-queries";

function FadeInCard({
  children,
  delay,
  animate,
  accent,
}: {
  children: ReactNode;
  delay: number;
  animate: boolean;
  accent: "strength" | "gap" | "primary";
}) {
  return (
    <div
      className={`border-l-2 pl-4 py-2 transition-all duration-500 motion-reduce:transition-none ${
        accent === "strength" ? "border-strength" : accent === "gap" ? "border-gap" : "border-primary"
      } ${animate ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"}`}
      style={{
        transitionDelay: `${delay}ms`,
      }}
    >
      {children}
    </div>
  );
}

export function StrengthsGaps() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evalData } = useSampleEvaluation();

  const evaluation = evalData?.evaluation;
  if (!evaluation?.strengths) return null;

  // Show first 2 of each for the landing page
  const strengths = evaluation.strengths.slice(0, 2);
  const gaps = evaluation.gaps?.slice(0, 2) ?? [];

  return (
    <section ref={ref} aria-labelledby="strengths-gaps-heading" className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 id="strengths-gaps-heading" className="text-3xl font-light text-foreground sm:text-4xl">
          Know exactly where you stand
        </h2>
      </div>
      <div className="w-full max-w-2xl space-y-8">
        {/* Strengths */}
        <div className="space-y-3">
          <h3 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">Strengths</h3>
          {strengths.map((s, i) => (
            <FadeInCard key={i} delay={i * 200} animate={isVisible} accent="strength">
              <p className="text-sm text-foreground">{s}</p>
            </FadeInCard>
          ))}
        </div>
        {/* Gaps */}
        <div className="space-y-3">
          <h3 className="text-xs font-medium uppercase tracking-wider text-muted-foreground">Areas to improve</h3>
          {gaps.map((g, i) => (
            <FadeInCard key={i} delay={(strengths.length + i) * 200} animate={isVisible} accent="gap">
              <p className="text-sm text-foreground">{g}</p>
            </FadeInCard>
          ))}
        </div>
        {/* Advice */}
        {evaluation.advice && (
          <FadeInCard
            delay={(strengths.length + gaps.length) * 200}
            animate={isVisible}
            accent="primary"
          >
            <p className="text-sm text-muted-foreground">{evaluation.advice}</p>
          </FadeInCard>
        )}
      </div>
    </section>
  );
}
