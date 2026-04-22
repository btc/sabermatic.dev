import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { AnnotationType } from "@/pb/drill/v1/evaluation_pb";

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

export function Transcript() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: sessionData } = useSampleSession();
  const { data: evalData } = useSampleEvaluation();

  const messages = sessionData?.messages ?? [];
  const annotations = evalData?.evaluation?.annotations ?? [];
  if (messages.length === 0) return null;

  // Index annotations by message seq for lookup during render.
  const byMessageSeq = new Map<number, typeof annotations>();
  for (const a of annotations) {
    const existing = byMessageSeq.get(a.messageSeq) ?? [];
    existing.push(a);
    byMessageSeq.set(a.messageSeq, existing);
  }

  return (
    <section ref={ref} id="transcript" className="py-28 px-10 sm:px-6">
      <div className="mx-auto max-w-[1120px]">
        <header className="mb-16">
          <div className="mb-5 flex items-center gap-3 font-mono text-[11px] uppercase tracking-[0.14em] text-muted-foreground">
            <span className="block h-px w-6 bg-border-strong" aria-hidden /> 03 — Transcript
          </div>
          <h2 className="mb-5 max-w-[20ch] text-[clamp(32px,4.2vw,56px)] font-light leading-[1.05] tracking-[-0.025em]">
            Feedback on what you actually said.
          </h2>
          <p className="max-w-[56ch] text-[17px] text-muted-foreground">
            Annotations anchored to the turn. Replay the session and see the moment the call was made — or wasn't.
          </p>
        </header>

        <div
          className={`flex flex-col gap-5 rounded-xl border border-border bg-card p-7 transition-opacity duration-700 motion-reduce:transition-none ${
            isVisible ? "opacity-100" : "opacity-0"
          }`}
        >
          {messages.map((m) => {
            const isInterviewer = m.role === "interviewer";
            const anns = byMessageSeq.get(m.seq) ?? [];
            return (
              <div key={m.id} className={`flex flex-col gap-2 ${isInterviewer ? "" : "items-end"}`}>
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
                  {m.content}
                </div>
                {anns.map((a, i) => (
                  <div
                    key={`${m.id}-ann-${i}`}
                    className={`ml-6 inline-flex max-w-[80%] items-start gap-2.5 border-l-2 px-3 py-2 text-xs leading-snug ${
                      INLINE_CLASS[a.type] ?? "border-muted text-muted-foreground"
                    }`}
                  >
                    <span>
                      <b className="font-mono text-[10px] font-medium uppercase tracking-[0.08em]">
                        {LABEL[a.type] ?? "Note"}
                      </b>
                      <br />
                      {a.content}
                    </span>
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </div>
    </section>
  );
}
