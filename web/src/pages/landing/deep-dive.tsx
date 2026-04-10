import { useSampleEducator } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";

export function DeepDivePreview() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data } = useSampleEducator();

  const analysis = data?.analysis;
  if (!analysis?.modelAnswer) return null;

  // Show first ~500 chars of model answer and first gap deep dive section
  const modelPreview = analysis.modelAnswer.slice(0, 500);
  const gapPreview = analysis.gapDeepDives?.slice(0, 400) ?? "";

  return (
    <section ref={ref} aria-labelledby="deep-dive-heading" className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 id="deep-dive-heading" className="text-3xl font-light text-foreground sm:text-4xl">
          Learn what you should have known
        </h2>
      </div>
      <div className="w-full max-w-2xl space-y-8">
        {/* Model answer preview */}
        <div
          className={`rounded-lg border border-border bg-card p-6 transition-all duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-8"
          }`}
        >
          <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
            Model Answer
          </h3>
          <div className="prose prose-sm prose-stone dark:prose-invert max-w-none">
            <p className="text-sm text-foreground leading-relaxed">
              {modelPreview}{analysis.modelAnswer.length > 500 ? "..." : ""}
            </p>
          </div>
        </div>
        {/* Gap deep dive preview */}
        {gapPreview && (
          <div
            className={`rounded-lg border border-border bg-card p-6 transition-all duration-700 motion-reduce:transition-none ${
              isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-8"
            }`}
            style={{ transitionDelay: "300ms" }}
          >
            <h3 className="mb-3 text-xs font-medium uppercase tracking-wider text-muted-foreground">
              Gap Analysis
            </h3>
            <div className="prose prose-sm prose-stone dark:prose-invert max-w-none">
              <p className="text-sm text-foreground leading-relaxed">
                {gapPreview}{(analysis.gapDeepDives?.length ?? 0) > 400 ? "..." : ""}
              </p>
            </div>
          </div>
        )}
      </div>
    </section>
  );
}
