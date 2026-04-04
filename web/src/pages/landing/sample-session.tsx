import { Link } from "react-router-dom";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleSession, useSampleEvaluation } from "@/api/sample-queries";

export function SampleSessionLink() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: sessionData } = useSampleSession();
  const { data: evaluation } = useSampleEvaluation();

  return (
    <section ref={ref} aria-labelledby="sample-heading" className="flex min-h-screen flex-col items-center justify-center gap-8 px-4">
      <div className="max-w-2xl text-center">
        <h2 id="sample-heading" className="text-3xl font-light text-foreground sm:text-4xl">
          See a real evaluation
        </h2>
      </div>
      <div
        className={`w-full max-w-md rounded-lg border border-border bg-card p-6 transition-all duration-500 motion-reduce:transition-none ${
          isVisible ? "opacity-100 translate-y-0" : "opacity-0 translate-y-4"
        }`}
      >
        {sessionData?.session && (
          <div className="space-y-2 text-sm text-muted-foreground">
            <p className="text-foreground font-medium">{sessionData.session.question_title}</p>
            <p>{sessionData.messages?.length ?? 0} turns · {sessionData.session.config_duration_minutes} min</p>
            {evaluation?.scores && (
              <p className="text-primary font-medium">Overall score: {evaluation.scores.overall}/5</p>
            )}
          </div>
        )}
        <Link
          to="/sample"
          className="mt-4 inline-block rounded-md bg-muted px-6 py-2 text-sm font-medium text-foreground transition-colors hover:bg-muted/80"
        >
          View full session →
        </Link>
      </div>
    </section>
  );
}
