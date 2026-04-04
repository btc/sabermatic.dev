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
    <section ref={ref} aria-labelledby="credits-heading" className="py-24 px-4">
      <div className="mx-auto max-w-sm">
        <h3
          id="credits-heading"
          className="mb-6 text-xs font-medium uppercase tracking-wider text-muted-foreground text-center"
        >
          Built with
        </h3>
        <div className="space-y-2">
          {CREDITS.map((c, i) => (
            <div
              key={c.role}
              className={`flex justify-between text-sm transition-opacity duration-300 motion-reduce:transition-none ${
                isVisible ? "opacity-100" : "opacity-0"
              }`}
              style={{ transitionDelay: `${i * 50}ms` }}
            >
              <span className="text-muted-foreground">{c.role}</span>
              <span className="text-foreground">{c.tech}</span>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}
