import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { AnnotationType } from "@/pb/drill/v1/evaluation_pb";
import type { Message } from "@/pb/drill/v1/session_pb";

const INLINE_CLASS: Record<number, string> = {
  [AnnotationType.STRENGTH]: "border-strength text-strength",
  [AnnotationType.GAP]: "border-gap text-gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "border-missed text-missed",
  [AnnotationType.NOTE]: "border-note text-note",
};

const LABEL: Record<number, string> = {
  [AnnotationType.STRENGTH]: "Strength",
  [AnnotationType.GAP]: "Gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "Missed",
  [AnnotationType.NOTE]: "Note",
};

// Hand-picked highlights from the sample evaluation. Marketing landing wants
// the juiciest exchange per annotation type rather than every annotation on
// the session. If the fixture changes and a pick goes missing, the section
// falls back to rendering whichever picks are still resolvable.
const FEATURED: ReadonlyArray<{ seq: number; type: AnnotationType }> = [
  { seq: 10, type: AnnotationType.MISSED_OPPORTUNITY },
  { seq: 14, type: AnnotationType.STRENGTH },
  { seq: 24, type: AnnotationType.GAP },
];

export function Transcript() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: sessionData } = useSampleSession();
  const { data: evalData } = useSampleEvaluation();

  const messages = sessionData?.messages ?? [];
  const annotations = evalData?.evaluation?.annotations ?? [];
  if (messages.length === 0) return null;

  const bySeq = new Map(messages.map((m) => [m.seq, m]));
  const features = FEATURED.flatMap(({ seq, type }) => {
    const annotation = annotations.find((a) => a.messageSeq === seq && a.type === type);
    const annotated = bySeq.get(seq);
    if (!annotation || !annotated) return [];
    return [{ annotation, annotated, preceding: bySeq.get(seq - 1) }];
  });
  if (features.length === 0) return null;

  return (
    <section ref={ref} id="transcript" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 04 — Transcript
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Feedback on what you actually said.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Annotations anchored to the turn. Replay the session and see the moment the call was made — or wasn't.
          </p>
        </header>

        <div
          className={`flex flex-col gap-10 rounded-xl border border-border bg-card p-7 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {features.map(({ annotation, annotated, preceding }, i) => (
            <div
              key={annotated.id}
              className={`flex flex-col gap-3 ${i > 0 ? "border-t border-border pt-10" : ""}`}
            >
              {preceding ? <MessageBubble message={preceding} /> : null}
              <MessageBubble message={annotated} />
              <div
                className={`ml-6 inline-flex max-w-[80%] items-start gap-2.5 border-l-2 px-3 py-2 text-xs leading-snug ${
                  INLINE_CLASS[annotation.type] ?? "border-muted text-muted-foreground"
                }`}
              >
                <span>
                  <b className="font-mono text-[10px] font-medium uppercase tracking-[0.08em]">
                    {LABEL[annotation.type] ?? "Note"}
                  </b>
                  <br />
                  {annotation.content}
                </span>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

function MessageBubble({ message }: { message: Message }) {
  const isInterviewer = message.role === "interviewer";
  return (
    <div className={`flex flex-col gap-2 ${isInterviewer ? "" : "items-end"}`}>
      <div
        className={`font-mono text-[10px] uppercase tracking-[0.1em] text-muted-foreground ${
          isInterviewer ? "" : "mr-1 text-right"
        }`}
      >
        {isInterviewer ? "Interviewer" : "Candidate"}
      </div>
      <div
        className={`max-w-[85%] rounded-[10px] px-[18px] py-3.5 text-sm leading-relaxed ${
          isInterviewer ? "mr-[15%] bg-muted" : "ml-[15%] bg-primary-soft text-left"
        }`}
      >
        {message.content}
      </div>
    </div>
  );
}
