import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleCoach } from "@/api/sample-queries";

function Sparkline({ data, animate }: { data: { date: string; overallScore: number }[]; animate: boolean }) {
  if (data.length < 2) return null;

  const width = 280;
  const height = 60;
  const padding = 4;
  const maxScore = 5;

  const points = data.map((d, i) => {
    const score = Math.min(Math.max(d.overallScore, 0), maxScore);
    return {
      x: padding + (i / (data.length - 1)) * (width - padding * 2),
      y: padding + ((maxScore - score) / maxScore) * (height - padding * 2),
    };
  });

  const pathData = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`).join(" ");

  return (
    <svg
      viewBox={`0 0 ${width} ${height}`}
      className={`w-full max-w-[280px] transition-opacity duration-700 motion-reduce:transition-none ${
        animate ? "opacity-100" : "opacity-0"
      }`}
      role="img"
      aria-label="Score trend chart showing overall scores across sessions"
    >
      <path
        d={pathData}
        fill="none"
        stroke="var(--primary)"
        strokeWidth="2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      {points.map((p, i) => (
        <circle
          key={i}
          cx={p.x}
          cy={p.y}
          r="3"
          fill="var(--primary)"
          className="transition-opacity duration-300 motion-reduce:transition-none"
          style={{ transitionDelay: `${i * 100}ms`, opacity: animate ? 1 : 0 }}
        />
      ))}
    </svg>
  );
}

export function Coaching() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data } = useSampleCoach();

  if (!data?.analysis?.narrative) return null;

  return (
    <section ref={ref} aria-labelledby="coaching-heading" className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 id="coaching-heading" className="text-3xl font-light text-foreground sm:text-4xl">
          Track your growth across sessions
        </h2>
      </div>
      <div className="w-full max-w-lg space-y-6">
        {/* Score trend sparkline */}
        {data.scoreTrend && data.scoreTrend.length > 1 && (
          <div
            className={`flex flex-col items-center gap-2 transition-all duration-500 motion-reduce:transition-none ${
              isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
            }`}
          >
            <Sparkline data={data.scoreTrend} animate={isVisible} />
            <span className="text-xs text-muted-foreground">Score trend over sessions</span>
          </div>
        )}

        {/* Weakest dimension badge */}
        {data.analysis.weakestDimension && (
          <div
            className={`flex items-center justify-center gap-2 transition-all duration-500 motion-reduce:transition-none ${
              isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
            }`}
            style={{ transitionDelay: "300ms" }}
          >
            <span className="rounded-full bg-gap/10 px-3 py-1 text-xs font-medium text-gap">
              Focus area: {data.analysis.weakestDimension}
            </span>
          </div>
        )}

        {/* Coach narrative excerpt */}
        <div
          className={`rounded-lg border border-border bg-card p-6 transition-all duration-500 motion-reduce:transition-none ${
            isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
          }`}
          style={{ transitionDelay: "500ms" }}
        >
          <p className="text-sm text-foreground leading-relaxed">
            {data.analysis.narrative.slice(0, 300)}{data.analysis.narrative.length > 300 ? "..." : ""}
          </p>
        </div>
      </div>
    </section>
  );
}
