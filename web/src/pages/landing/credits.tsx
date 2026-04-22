import { useScrollReveal } from "@/hooks/use-scroll-reveal";

const CREDITS = [
  { role: "Interviewer", tech: "Claude Sonnet" },
  { role: "Evaluator", tech: "Claude Opus" },
  { role: "Educator", tech: "Claude Opus" },
  { role: "Coach", tech: "Claude Sonnet" },
  { role: "Speech-to-text", tech: "Whisper" },
  { role: "Text-to-speech", tech: "OpenAI TTS" },
  { role: "Backend", tech: "Go on Google Cloud Run" },
  { role: "Database", tech: "PostgreSQL" },
  { role: "Payments", tech: "Stripe" },
];

export function Credits() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();

  return (
    <section ref={ref} id="stack" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-10 text-center">
          <div className="mb-5 flex items-center justify-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 07 — Built with
          </div>
          <h2 className="mx-auto max-w-none text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            No magic. Good tools.
          </h2>
        </header>

        <div
          className={`grid grid-cols-1 border-t border-border sm:grid-cols-3 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {CREDITS.map((c) => (
            <div
              key={c.role}
              className="flex items-baseline justify-between border-b border-border px-6 py-5 sm:border-r sm:[&:nth-child(3n)]:border-r-0"
            >
              <span className="font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">{c.role}</span>
              <span className="text-sm font-medium">{c.tech}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
