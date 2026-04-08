import { useScrollReveal } from "@/hooks/use-scroll-reveal";
import { useSampleEvaluation, useSampleSession } from "@/api/sample-queries";
import { AnnotationType } from "@/pb/drill/v1/evaluation_pb";
import { cn } from "@/lib/utils";

const ANNOTATION_COLORS: Record<AnnotationType, string> = {
  [AnnotationType.UNSPECIFIED]: "border-muted text-muted",
  [AnnotationType.STRENGTH]: "border-strength text-strength",
  [AnnotationType.GAP]: "border-gap text-gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "border-missed text-missed",
  [AnnotationType.NOTE]: "border-note text-note",
};

const ANNOTATION_LABELS: Record<AnnotationType, string> = {
  [AnnotationType.UNSPECIFIED]: "Unspecified",
  [AnnotationType.STRENGTH]: "Strength",
  [AnnotationType.GAP]: "Gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "Missed opportunity",
  [AnnotationType.NOTE]: "Note",
};

export function Annotations() {
  const { ref, isVisible } = useScrollReveal<HTMLElement>();
  const { data: sessionData } = useSampleSession();
  const { data: evalData } = useSampleEvaluation();

  const evaluation = evalData?.evaluation;
  if (!sessionData?.messages || !evaluation?.annotations) return null;

  // Sort messages by seq to ensure correct window selection
  const sorted = [...sessionData.messages].sort((a, b) => a.seq - b.seq);

  // Pick a 3-5 message window that has annotations
  const annotatedSeqs = new Set(evaluation.annotations.map((a) => a.messageSeq));
  const firstAnnotated = sorted.find((m) => annotatedSeqs.has(m.seq));
  if (!firstAnnotated) return null;

  const startSeq = Math.max(1, firstAnnotated.seq - 1);
  const window = sorted.filter(
    (m) => m.seq >= startSeq && m.seq < startSeq + 5,
  );
  const windowAnnotations = evaluation.annotations.filter(
    (a) => a.messageSeq >= startSeq && a.messageSeq < startSeq + 5,
  );

  return (
    <section ref={ref} aria-labelledby="annotations-heading" className="flex min-h-screen flex-col items-center justify-center gap-12 px-4">
      <div className="max-w-2xl text-center">
        <h2 id="annotations-heading" className="text-3xl font-light text-foreground sm:text-4xl">
          Feedback on what you actually said
        </h2>
      </div>
      <div className="w-full max-w-2xl space-y-4">
        {window.map((msg, msgIdx) => {
          const msgAnnotations = windowAnnotations.filter(
            (a) => a.messageSeq === msg.seq,
          );
          return (
            <div key={msg.seq} className="space-y-2">
              <div
                className={cn(
                  "rounded-lg px-4 py-3 text-sm",
                  msg.role === "interviewer"
                    ? "bg-muted text-foreground mr-12"
                    : "bg-primary/10 text-foreground ml-12",
                )}
              >
                <span className="text-xs font-medium text-muted-foreground">
                  {msg.role === "interviewer" ? "Interviewer" : "Candidate"}
                </span>
                <p className="mt-1">{msg.content.slice(0, 200)}{msg.content.length > 200 ? "..." : ""}</p>
              </div>
              {msgAnnotations.map((ann, annIdx) => (
                <div
                  key={`${ann.messageSeq}-${ann.type}-${annIdx}`}
                  className={cn(
                    "ml-8 border-l-2 pl-3 py-1 text-xs transition-all duration-500 motion-reduce:transition-none",
                    ANNOTATION_COLORS[ann.type],
                    isVisible
                      ? "opacity-100 translate-x-0"
                      : "opacity-0 -translate-x-4",
                  )}
                  style={{
                    transitionDelay: `${(msgIdx * 300) + (annIdx * 150)}ms`,
                  }}
                >
                  <span className="font-medium">{ANNOTATION_LABELS[ann.type]}:</span>{" "}
                  {ann.content}
                </div>
              ))}
            </div>
          );
        })}
      </div>
    </section>
  );
}
