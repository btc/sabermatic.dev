import { useScrollReveal } from "@/hooks/use-scroll-reveal";

function barHeights(seedShift: number) {
  return Array.from({ length: 28 }, (_, i) => {
    const seed = i + seedShift;
    return 12 + Math.sin(seed * 0.5) * 14 + ((seed * 7 + 3) % 11) * 0.8;
  });
}

function Waveform({ bars }: { bars: number[] }) {
  return (
    <div className="flex h-12 items-center gap-[3px]" aria-hidden>
      {bars.map((h, i) => (
        <span
          key={i}
          className="w-[3px] rounded-sm bg-primary animate-[wav_1.4s_ease-in-out_infinite] motion-reduce:animate-none"
          style={{ height: `${h}px`, animationDelay: `${i * 0.05}s` }}
        />
      ))}
    </div>
  );
}

export function VoicePipeline() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();

  return (
    <section ref={ref} id="pipeline" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 01 — Conversation
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Conversational mock interviews with an expert interviewer.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Adaptive follow-ups. Pushback when you hand-wave. Patient when you're mid-thought.
          </p>
        </header>

        <div
          className={`grid grid-cols-1 overflow-hidden rounded-xl border border-border bg-card md:grid-cols-3 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {/* Col 1 — You */}
          <div className="flex min-h-[220px] flex-col gap-4.5 border-b border-border p-8 md:border-b-0 md:border-r">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              <span>01 · You</span>
              <span className="text-primary">Voice in</span>
            </div>
            <div className="text-[15px] font-medium">Think out loud.</div>
            <div className="flex flex-1 flex-col justify-center">
              <Waveform bars={barHeights(0)} />
              <div className="mt-4 font-mono text-xs leading-[1.7] text-muted-foreground">
                "I'd start by defining the API contract — POST /shorten, GET /:slug
                <span className="ml-1 inline-block h-3.5 w-2 animate-[blink_1s_steps(2)_infinite] bg-primary align-middle motion-reduce:animate-none" />
                "
              </div>
            </div>
          </div>

          {/* Col 2 — Interviewer */}
          <div className="flex min-h-[220px] flex-col gap-4.5 border-b border-border p-8 md:border-b-0 md:border-r">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              <span>02 · Interviewer</span>
              <span className="text-primary">Voice back</span>
            </div>
            <div className="text-[15px] font-medium">A follow-up, out loud.</div>
            <div className="flex flex-1 flex-col justify-center">
              <Waveform bars={barHeights(5)} />
              <div className="mt-4 font-mono text-xs leading-[1.7] text-foreground">
                "Good start. What happens when two users generate the same slug at the same time?"
              </div>
            </div>
          </div>

          {/* Col 3 — Afterward */}
          <div className="flex min-h-[220px] flex-col gap-4.5 p-8">
            <div className="flex justify-between font-mono text-[10px] uppercase tracking-[0.12em] text-muted-foreground">
              <span>03 · Afterward</span>
              <span className="text-primary">Annotated</span>
            </div>
            <div className="text-[15px] font-medium">Every moment, graded.</div>
            <div className="flex flex-1 flex-col justify-start gap-2.5 pt-1.5">
              <div className="border-l-2 border-strength px-3 py-2.5 text-xs text-strength leading-snug">
                <b className="mb-1 block font-mono text-[10px] font-medium uppercase tracking-[0.08em]">Strength</b>
                Leads with API contract.
              </div>
              <div className="border-l-2 border-gap px-3 py-2.5 text-xs text-gap leading-snug">
                <b className="mb-1 block font-mono text-[10px] font-medium uppercase tracking-[0.08em]">Gap</b>
                Collision strategy hand-waved.
              </div>
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
