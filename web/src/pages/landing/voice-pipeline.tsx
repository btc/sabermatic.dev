import { useEffect, useMemo, useState } from "react";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

type Stage = "waveform" | "transcript" | "annotations";

export function VoicePipeline() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const [stage, setStage] = useState<Stage>("waveform");

  const barHeights = useMemo(
    () => Array.from({ length: 24 }, (_, i) => 12 + Math.sin(i * 0.5) * 12 + Math.random() * 8),
    [],
  );

  useEffect(() => {
    if (!isVisible) return;
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
      setStage("annotations");
      return;
    }
    const t1 = setTimeout(() => setStage("transcript"), 1500);
    const t2 = setTimeout(() => setStage("annotations"), 3000);
    return () => {
      clearTimeout(t1);
      clearTimeout(t2);
    };
  }, [isVisible]);

  return (
    <section ref={ref} aria-labelledby="voice-heading" className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 id="voice-heading" className="text-3xl font-light text-foreground sm:text-4xl">
          Speak naturally. We handle the rest.
        </h2>
      </div>
      <div className="flex w-full max-w-md flex-col items-center gap-6">
        {/* Waveform */}
        <div
          className={`flex items-center gap-1 transition-opacity duration-500 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {barHeights.map((h, i) => (
            <div
              key={i}
              className={`w-1 rounded-full bg-primary ${stage === "waveform" ? "animate-pulse" : ""}`}
              style={{ height: `${h}px` }}
            />
          ))}
        </div>

        {/* Transcript — illustrative example, not verbatim session 27 data */}
        <div
          className={`w-full rounded-lg bg-muted px-4 py-3 text-sm text-foreground transition-all duration-500 motion-reduce:transition-none ${
            stage === "transcript" || stage === "annotations"
              ? "opacity-100 translate-y-0"
              : "opacity-0 translate-y-4"
          }`}
        >
          "I'd start by defining the API contract — the key endpoints for creating and retrieving resources..."
        </div>

        {/* Annotation — illustrative example */}
        <div
          className={`ml-8 border-l-2 border-strength pl-3 py-1 text-xs text-strength transition-all duration-500 motion-reduce:transition-none ${
            stage === "annotations"
              ? "opacity-100 translate-x-0"
              : "opacity-0 -translate-x-4"
          }`}
        >
          <span className="font-medium">Strength:</span> Candidate leads with API design before jumping to infrastructure
        </div>
      </div>
    </section>
  );
}
