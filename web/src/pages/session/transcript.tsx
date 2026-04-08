import { useState, useRef, useCallback, useMemo } from "react";
import { useSession, useTranscript, useEvaluation } from "@/api/queries";
import { useSampleSession, useSampleEvaluation } from "@/api/sample-queries";
import { useSessionDetail } from "./layout";
import { useReplayEngine } from "@/components/replay/engine";
import { ReplayControls } from "@/components/replay/controls";
import { cn } from "@/lib/utils";
import { Skeleton } from "@/components/ui/skeleton";
import { AnnotationType } from "@/pb/drill/v1/evaluation_pb";
import type { Annotation } from "@/pb/drill/v1/evaluation_pb";
import type { Message } from "@/pb/drill/v1/session_pb";

// ---------------------------------------------------------------------------
// Constants & helpers
// ---------------------------------------------------------------------------

const ANNOTATION_LABELS: Record<AnnotationType, string> = {
  [AnnotationType.UNSPECIFIED]: "unspecified",
  [AnnotationType.STRENGTH]: "strengths",
  [AnnotationType.GAP]: "gaps",
  [AnnotationType.MISSED_OPPORTUNITY]: "missed opportunities",
  [AnnotationType.NOTE]: "notes",
};

const ANNOTATION_BORDER: Record<AnnotationType, string> = {
  [AnnotationType.UNSPECIFIED]: "border-muted",
  [AnnotationType.STRENGTH]: "border-strength",
  [AnnotationType.GAP]: "border-gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "border-missed",
  [AnnotationType.NOTE]: "border-note",
};

const ANNOTATION_TEXT: Record<AnnotationType, string> = {
  [AnnotationType.UNSPECIFIED]: "text-muted",
  [AnnotationType.STRENGTH]: "text-strength",
  [AnnotationType.GAP]: "text-gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "text-missed",
  [AnnotationType.NOTE]: "text-note",
};

const ANNOTATION_BG: Record<AnnotationType, string> = {
  [AnnotationType.UNSPECIFIED]: "bg-muted",
  [AnnotationType.STRENGTH]: "bg-strength",
  [AnnotationType.GAP]: "bg-gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "bg-missed",
  [AnnotationType.NOTE]: "bg-note",
};

const ANNOTATION_DISPLAY_NAMES: Record<AnnotationType, string> = {
  [AnnotationType.UNSPECIFIED]: "unspecified",
  [AnnotationType.STRENGTH]: "strength",
  [AnnotationType.GAP]: "gap",
  [AnnotationType.MISSED_OPPORTUNITY]: "missed opportunity",
  [AnnotationType.NOTE]: "note",
};

const ANNOTATION_TYPES: AnnotationType[] = [
  AnnotationType.STRENGTH,
  AnnotationType.GAP,
  AnnotationType.MISSED_OPPORTUNITY,
  AnnotationType.NOTE,
];

type FilterType = "all" | AnnotationType;

// ---------------------------------------------------------------------------
// Summary bar
// ---------------------------------------------------------------------------

interface SummaryBarProps {
  annotations: Annotation[];
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
    {
      [AnnotationType.UNSPECIFIED]: 0,
      [AnnotationType.STRENGTH]: 0,
      [AnnotationType.GAP]: 0,
      [AnnotationType.MISSED_OPPORTUNITY]: 0,
      [AnnotationType.NOTE]: 0,
    },
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
  annotation: Annotation;
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
        {ANNOTATION_DISPLAY_NAMES[annotation.type]}
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
  annotations: Annotation[];
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
  annotation: Annotation;
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
      title={`${ANNOTATION_DISPLAY_NAMES[annotation.type]}: ${annotation.content.slice(0, 60)}...`}
      className={cn(
        "absolute size-2 rounded-full -translate-x-1/2 -translate-y-1/2 transition-transform hover:scale-150 cursor-pointer",
        ANNOTATION_BG[annotation.type],
      )}
      style={{ top: `${pct}%`, left: "50%" }}
    />
  );
}

interface AnnotationRailProps {
  annotations: Annotation[];
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
        const msgIndex = seqToIndex.get(ann.messageSeq) ?? 0;
        return (
          <RailDot
            key={i}
            annotation={ann}
            totalMessages={messages.length}
            msgIndex={msgIndex}
            onClick={() => onDotClick(ann.messageSeq)}
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
  return <TranscriptInner />;
}

function TranscriptInner() {
  const { dataSource, sessionId } = useSessionDetail();

  const authSession = useSession(sessionId, { enabled: dataSource === "api" });
  const authTranscript = useTranscript(sessionId, dataSource === "api");
  const authEval = useEvaluation(sessionId, dataSource === "api");
  const sampleSession = useSampleSession({ enabled: dataSource === "sample" });
  const sampleEval = useSampleEvaluation({ enabled: dataSource === "sample" });

  const session =
    dataSource === "api" ? authSession.data?.session : sampleSession.data?.session;
  const messages = dataSource === "api" ? authTranscript.data?.messages : sampleSession.data?.messages;
  const evaluation = dataSource === "api" ? authEval.data?.evaluation : sampleEval.data?.evaluation;
  const evaluationLoading = dataSource === "api" ? authEval.isLoading : sampleEval.isLoading;

  const [activeFilter, setActiveFilter] = useState<FilterType>("all");

  // Replay state
  const [replayMode, setReplayMode] = useState(false);

  const replay = useReplayEngine(
    replayMode && messages && session?.startTime
      ? {
          messages,
          sessionStartedAt: session.startTime,
          sessionEndedAt: session.endTime,
          annotationSeqs: (evaluation?.annotations ?? []).map(
            (a) => a.messageSeq,
          ),
        }
      : null,
  );

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
    return (
      <div className="space-y-4 max-w-3xl mx-auto">
        <Skeleton className="h-8 w-48" />
        <Skeleton className="h-16 w-3/4" />
        <Skeleton className="h-16 w-1/2 ml-auto" />
        <Skeleton className="h-16 w-3/4" />
        <Skeleton className="h-16 w-1/2 ml-auto" />
      </div>
    );
  }

  const sorted = useMemo(
    () => [...messages].sort((a, b) => a.seq - b.seq),
    [messages],
  );
  const annotations = evaluation?.annotations ?? [];

  // Group annotations by messageSeq for efficient lookup
  const annotationsBySeq = useMemo(
    () =>
      annotations.reduce<Map<number, Annotation[]>>(
        (acc, ann) => {
          const existing = acc.get(ann.messageSeq) ?? [];
          acc.set(ann.messageSeq, [...existing, ann]);
          return acc;
        },
        new Map(),
      ),
    [annotations],
  );

  const handleJumpToFirst = (type: AnnotationType) => {
    const first = annotations.find((a) => a.type === type);
    if (first) {
      scrollToSeq(first.messageSeq);
    }
  };

  // Filter messages/annotations during replay
  const displayMessages = replayMode
    ? sorted.slice(0, replay.state.visibleMessages)
    : sorted;

  const displayAnnotations = replayMode
    ? (() => {
        const activeSet = new Set(replay.state.activeAnnotationSeqs);
        return annotations.filter((a) => activeSet.has(a.messageSeq));
      })()
    : annotations;

  // Build display-time annotation lookup (changes during replay)
  const displayAnnotationsBySeq = replayMode
    ? displayAnnotations.reduce<Map<number, Annotation[]>>(
        (acc, ann) => {
          const existing = acc.get(ann.messageSeq) ?? [];
          acc.set(ann.messageSeq, [...existing, ann]);
          return acc;
        },
        new Map(),
      )
    : annotationsBySeq;

  return (
    <div className="flex gap-0 max-w-3xl mx-auto">
      {/* Main content column */}
      <div className="flex-1 min-w-0">
        {/* Replay controls */}
        <div className="flex items-center gap-2 py-2 px-1">
          <button
            className={cn(
              "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
              replayMode
                ? "bg-primary text-primary-foreground"
                : "bg-muted text-muted-foreground hover:text-foreground",
            )}
            onClick={() => setReplayMode((v) => !v)}
            aria-pressed={replayMode}
          >
            {replayMode ? "Exit Replay" : "Replay"}
          </button>
        </div>

        {replayMode && (
          <div className="px-1 pb-3">
            <ReplayControls
              state={replay.state}
              onPlay={replay.play}
              onPause={replay.pause}
              onSeek={replay.seek}
              onSetSpeed={replay.setSpeed}
            />
          </div>
        )}

        {/* Summary bar */}
        {!replayMode && annotations.length > 0 ? (
          <SummaryBar
            annotations={annotations}
            activeFilter={activeFilter}
            onFilterChange={setActiveFilter}
            onJumpToFirst={handleJumpToFirst}
          />
        ) : !replayMode && !evaluationLoading ? (
          <NoAnnotationsView />
        ) : null}

        {/* Message list */}
        <div className="space-y-5 pt-2 pb-10">
          {displayMessages.map((msg) => {
            const msgAnnotations = displayAnnotationsBySeq.get(msg.seq) ?? [];
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
      {!replayMode && annotations.length > 0 && (
        <AnnotationRail
          annotations={annotations}
          messages={sorted}
          onDotClick={scrollToSeq}
        />
      )}
    </div>
  );
}
