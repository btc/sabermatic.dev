import { useSampleEvaluation } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { AnnotationType } from "@/pb/drill/v1/evaluation_pb";

type Kind = "strength" | "gap" | "missed";

const KIND_CLASS: Record<Kind, string> = {
  strength: "border-strength",
  gap: "border-gap",
  missed: "border-missed",
};
const KIND_LABEL_CLASS: Record<Kind, string> = {
  strength: "text-strength",
  gap: "text-gap",
  missed: "text-missed",
};
const KIND_HEADING: Record<Kind, string> = {
  strength: "Strengths",
  gap: "Gaps",
  missed: "Missed opportunities",
};

const KIND_TO_TYPE: Record<Kind, AnnotationType> = {
  strength: AnnotationType.STRENGTH,
  gap: AnnotationType.GAP,
  missed: AnnotationType.MISSED_OPPORTUNITY,
};

export function StrengthsGaps() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: evalData } = useSampleEvaluation();

  const annotations = evalData?.evaluation?.annotations ?? [];
  if (annotations.length === 0) return null;

  const byKind = {
    strength: annotations.filter((a) => a.type === KIND_TO_TYPE.strength),
    gap: annotations.filter((a) => a.type === KIND_TO_TYPE.gap),
    missed: annotations.filter((a) => a.type === KIND_TO_TYPE.missed),
  };

  return (
    <section ref={ref} id="strengths" className="py-28 px-10 sm:px-6">
      <div className="mx-auto grid max-w-[1120px] grid-cols-1 items-start gap-20 md:grid-cols-[5fr_6fr]">
        <header>
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 02 — Evidence
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Know exactly where you stand.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Specific moments from your transcript, not generic advice. Strengths, gaps, and the ones you nearly caught.
          </p>
        </header>

        <div
          className={`flex flex-col gap-1 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {(["strength", "gap", "missed"] as const).map((kind) =>
            byKind[kind].length === 0 ? null : (
              <div key={kind}>
                <div className="mt-5 mb-2.5 font-mono text-[11px] uppercase tracking-[0.12em] text-muted-foreground first:mt-0">
                  {KIND_HEADING[kind]}
                </div>
                {byKind[kind].map((a, i) => (
                  <div
                    key={`${kind}-${i}`}
                    className={`border-l-2 py-2.5 pl-4 text-sm leading-relaxed ${KIND_CLASS[kind]}`}
                  >
                    <span
                      className={`mr-2 font-mono text-[11px] font-medium uppercase tracking-[0.08em] ${KIND_LABEL_CLASS[kind]}`}
                    >
                      {KIND_HEADING[kind].replace(/ opportunities$/, "").replace(/s$/, "")}
                    </span>
                    {a.content}
                  </div>
                ))}
              </div>
            ),
          )}
        </div>
      </div>
    </section>
  );
}
