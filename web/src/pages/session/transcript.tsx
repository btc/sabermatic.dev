import { useState, useRef, useCallback } from "react";
import { useParams } from "react-router-dom";
import { useTranscript, useEvaluation } from "@/api/queries";
import { cn } from "@/lib/utils";
import type { AnnotationType, AnnotationResponse, Message } from "@/api/types";

// ---------------------------------------------------------------------------
// Constants & helpers
// ---------------------------------------------------------------------------

const ANNOTATION_LABELS: Record<AnnotationType, string> = {
  strength: "strengths",
  gap: "gaps",
  missed_opportunity: "missed opportunities",
  note: "notes",
};

const ANNOTATION_BORDER: Record<AnnotationType, string> = {
  strength: "border-strength",
  gap: "border-gap",
  missed_opportunity: "border-missed",
  note: "border-note",
};

const ANNOTATION_TEXT: Record<AnnotationType, string> = {
  strength: "text-strength",
  gap: "text-gap",
  missed_opportunity: "text-missed",
  note: "text-note",
};

const ANNOTATION_BG: Record<AnnotationType, string> = {
  strength: "bg-strength",
  gap: "bg-gap",
  missed_opportunity: "bg-missed",
  note: "bg-note",
};

const ANNOTATION_TYPES: AnnotationType[] = [
  "strength",
  "gap",
  "missed_opportunity",
  "note",
];

type FilterType = "all" | AnnotationType;

// ---------------------------------------------------------------------------
// Summary bar
// ---------------------------------------------------------------------------

interface SummaryBarProps {
  annotations: AnnotationResponse[];
  activeFilter: FilterType;
  onFilterChange: (filter: FilterType) => void;
  onJumpToFirst: (type: AnnotationType) => void;
}

function SummaryBar({
  annotations,
  activeFilter,
  onFilterChange,
  onJumpToFirst,
}: SummaryBarProps) {
  const counts = ANNOTATION_TYPES.reduce<Record<AnnotationType, number>>(
    (acc, type) => {
      acc[type] = annotations.filter((a) => a.type === type).length;
      return acc;
    },
    { strength: 0, gap: 0, missed_opportunity: 0, note: 0 },
  );

  const total = annotations.length;

  const handleTypeClick = (type: AnnotationType) => {
    if (activeFilter === type) {
      // Toggle off — return to "all"
      onFilterChange("all");
    } else {
      onFilterChange(type);
      onJumpToFirst(type);
    }
  };

  return (
    <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5 text-sm text-muted-foreground py-3 px-1">
      <span className="font-medium text-foreground">{total} annotation{total !== 1 ? "s" : ""}:</span>
      {ANNOTATION_TYPES.map((type) => {
        const count = counts[type];
        if (count === 0) return null;
        const isActive = activeFilter === type;
        return (
          <button
            key={type}
            onClick={() => handleTypeClick(type)}
            className={cn(
              "transition-colors hover:text-foreground",
              isActive && cn("font-medium", ANNOTATION_TEXT[type]),
              !isActive && activeFilter !== "all" && "opacity-40",
            )}
          >
            {count} {ANNOTATION_LABELS[type]}
          </button>
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Annotation callout card
// ---------------------------------------------------------------------------

interface AnnotationCardProps {
  annotation: AnnotationResponse;
  dimmed: boolean;
}

function AnnotationCard({ annotation, dimmed }: AnnotationCardProps) {
  return (
    <div
      className={cn(
        "ml-4 mt-1.5 border border-border border-l-4 bg-card rounded-lg px-3 py-2 text-xs leading-relaxed transition-opacity duration-150",
        ANNOTATION_BORDER[annotation.type],
        dimmed && "opacity-30",
      )}
    >
      <span className={cn("font-medium uppercase tracking-wide text-[10px] mr-2", ANNOTATION_TEXT[annotation.type])}>
        {annotation.type.replace("_", " ")}
      </span>
      {annotation.content}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Message row
// ---------------------------------------------------------------------------

interface MessageRowProps {
  message: Message;
  annotations: AnnotationResponse[];
  activeFilter: FilterType;
  msgRef: (el: HTMLDivElement | null) => void;
}

function MessageRow({ message, annotations, activeFilter, msgRef }: MessageRowProps) {
  const isInterviewer = message.role === "interviewer";

  return (
    <div ref={msgRef} className="space-y-0">
      {/* Bubble */}
      <div className={cn("flex", isInterviewer ? "justify-start" : "justify-end")}>
        <div
          className={cn(
            "max-w-[80%] rounded-xl px-4 py-3 text-sm leading-relaxed whitespace-pre-wrap",
            isInterviewer
              ? "rounded-tl-sm bg-card border border-border"
              : "rounded-tr-sm bg-primary/10 dark:bg-primary/20",
          )}
        >
          {message.content}
        </div>
      </div>

      {/* Annotation callout cards */}
      {annotations.map((ann, i) => {
        const dimmed = activeFilter !== "all" && activeFilter !== ann.type;
        return <AnnotationCard key={i} annotation={ann} dimmed={dimmed} />;
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Annotation rail
// ---------------------------------------------------------------------------

interface RailDotProps {
  annotation: AnnotationResponse;
  totalMessages: number;
  msgIndex: number;
  onClick: () => void;
}

function RailDot({ annotation, totalMessages, msgIndex, onClick }: RailDotProps) {
  // Proportional position: map msgIndex to 0%–100%
  const pct = totalMessages > 1 ? (msgIndex / (totalMessages - 1)) * 100 : 50;

  return (
    <button
      onClick={onClick}
      title={`${annotation.type.replace("_", " ")}: ${annotation.content.slice(0, 60)}...`}
      className={cn(
        "absolute size-2 rounded-full -translate-x-1/2 -translate-y-1/2 transition-transform hover:scale-150 cursor-pointer",
        ANNOTATION_BG[annotation.type],
      )}
      style={{ top: `${pct}%`, left: "50%" }}
    />
  );
}

interface AnnotationRailProps {
  annotations: AnnotationResponse[];
  messages: Message[];
  onDotClick: (seq: number) => void;
}

function AnnotationRail({ annotations, messages, onDotClick }: AnnotationRailProps) {
  if (annotations.length === 0) return null;

  // Build seq -> index lookup
  const seqToIndex = new Map(messages.map((m, i) => [m.seq, i]));

  return (
    <div className="relative w-5 shrink-0 ml-2" aria-hidden="true">
      {/* Track line */}
      <div className="absolute inset-y-0 left-1/2 w-px bg-border -translate-x-1/2" />

      {annotations.map((ann, i) => {
        const msgIndex = seqToIndex.get(ann.message_seq) ?? 0;
        return (
          <RailDot
            key={i}
            annotation={ann}
            totalMessages={messages.length}
            msgIndex={msgIndex}
            onClick={() => onDotClick(ann.message_seq)}
          />
        );
      })}
    </div>
  );
}

// ---------------------------------------------------------------------------
// Empty / no-annotation state
// ---------------------------------------------------------------------------

function NoAnnotationsView() {
  return (
    <div className="py-12 text-center text-sm text-muted-foreground">
      No annotations for this session.
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function TranscriptPage() {
  const { id: sessionId } = useParams<{ id: string }>();
  const { data: messages } = useTranscript(sessionId!);
  const { data: evaluation } = useEvaluation(sessionId!);

  const [activeFilter, setActiveFilter] = useState<FilterType>("all");

  // Map of seq -> DOM element ref for scroll targets
  const msgRefs = useRef<Map<number, HTMLDivElement>>(new Map());

  const setRef = useCallback(
    (seq: number) => (el: HTMLDivElement | null) => {
      if (el) {
        msgRefs.current.set(seq, el);
      } else {
        msgRefs.current.delete(seq);
      }
    },
    [],
  );

  const scrollToSeq = useCallback((seq: number) => {
    const el = msgRefs.current.get(seq);
    if (el) {
      el.scrollIntoView({ behavior: "smooth", block: "center" });
    }
  }, []);

  if (!messages) {
    return null;
  }

  const sorted = [...messages].sort((a, b) => a.seq - b.seq);
  const annotations = evaluation?.annotations ?? [];

  // Group annotations by message_seq for efficient lookup
  const annotationsBySeq = annotations.reduce<Map<number, AnnotationResponse[]>>(
    (acc, ann) => {
      const existing = acc.get(ann.message_seq) ?? [];
      acc.set(ann.message_seq, [...existing, ann]);
      return acc;
    },
    new Map(),
  );

  const handleJumpToFirst = (type: AnnotationType) => {
    const first = annotations.find((a) => a.type === type);
    if (first) {
      scrollToSeq(first.message_seq);
    }
  };

  return (
    <div className="flex gap-0 max-w-3xl mx-auto">
      {/* Main content column */}
      <div className="flex-1 min-w-0">
        {/* Summary bar */}
        {annotations.length > 0 ? (
          <SummaryBar
            annotations={annotations}
            activeFilter={activeFilter}
            onFilterChange={setActiveFilter}
            onJumpToFirst={handleJumpToFirst}
          />
        ) : (
          <NoAnnotationsView />
        )}

        {/* Message list */}
        <div className="space-y-5 pt-2 pb-10">
          {sorted.map((msg) => {
            const msgAnnotations = annotationsBySeq.get(msg.seq) ?? [];
            return (
              <MessageRow
                key={msg.id}
                message={msg}
                annotations={msgAnnotations}
                activeFilter={activeFilter}
                msgRef={setRef(msg.seq)}
              />
            );
          })}
        </div>
      </div>

      {/* Annotation rail */}
      {annotations.length > 0 && (
        <AnnotationRail
          annotations={annotations}
          messages={sorted}
          onDotClick={scrollToSeq}
        />
      )}
    </div>
  );
}
