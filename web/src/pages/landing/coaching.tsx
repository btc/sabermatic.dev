import { useSampleCoach } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

function Sparkline({ points }: { points: { x: number; y: number }[] }) {
  if (points.length < 2) return null;
  const path = points.map((p, i) => `${i === 0 ? "M" : "L"} ${p.x} ${p.y}`).join(" ");
  return (
    <svg viewBox="0 0 280 72" className="mt-5 block w-full max-w-[320px]" aria-hidden>
      <path d={path} stroke="var(--primary)" strokeWidth={2} fill="none" strokeLinecap="round" strokeLinejoin="round" />
      <g fill="var(--primary)">
        {points.map((p, i) => (
          <circle
            key={i}
            cx={p.x}
            cy={p.y}
            r={i === points.length - 1 ? 4 : 3}
            stroke={i === points.length - 1 ? "var(--background)" : undefined}
            strokeWidth={i === points.length - 1 ? 2 : undefined}
          />
        ))}
      </g>
    </svg>
  );
}

export function Coaching() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: coachData } = useSampleCoach();

  const trend = coachData?.scoreTrend ?? [];
  const first = trend[0];
  const last = trend[trend.length - 1];
  if (!first || !last) return null;

  const latest = last.overallScore;
  const earliest = first.overallScore;
  const delta = latest - earliest;

  // Map trend to svg coordinates (width=280, height=72, padding=4, scoreMax=5).
  const width = 280;
  const height = 72;
  const padding = 4;
  const maxScore = 5;
  const points = trend.map((d, i) => ({
    x: padding + (i / (trend.length - 1)) * (width - padding * 2),
    y: padding + (1 - d.overallScore / maxScore) * (height - padding * 2),
  }));

  return (
    <section ref={ref} id="coach" className="py-28 px-5 sm:px-6 lg:px-10">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 05 — Coaching
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Track your growth across sessions.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            A short letter after every few sessions: what's improving, what to drill, when you're ready.
          </p>
        </header>

        <div
          className={`grid grid-cols-1 items-center gap-10 rounded-xl border border-border bg-card p-8 md:grid-cols-2 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          <div>
            <div className="mb-1 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground">
              Overall · last {trend.length} sessions
            </div>
            <div className="font-mono text-[64px] font-medium leading-none tracking-[-0.03em] text-primary">
              {latest.toFixed(1)}
              <span className="ml-1.5 text-[28px] text-muted-foreground">/ 5</span>
            </div>
            <div className="mt-2 font-mono text-[13px] text-strength">
              {delta > 0 ? "▲" : "▼"} {Math.abs(delta).toFixed(1)} over last {trend.length - 1} sessions
            </div>
            <Sparkline points={points} />
            <div className="mt-5 inline-flex items-center gap-2 rounded-full bg-[hsl(var(--gap)/.1)] px-3 py-1.5 font-mono text-xs font-medium text-gap">
              Focus area: Deep Dive
            </div>
          </div>

          <div className="border-l-2 border-primary pl-5 text-sm leading-[1.7] text-foreground">
            You've gone from surface-level to structured in four sessions. Requirements framing is reliable now — you're anchoring every session in SLOs before touching boxes.
            <br />
            <br />
            The work ahead is <b>depth</b>. You name the right components but stop one question short of the trade-off. Next drill: one subsystem per session. Failure modes and two alternatives before moving on.
          </div>
        </div>
      </div>
    </section>
  );
}
