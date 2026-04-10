import { useState, useMemo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Link } from "react-router-dom";
import { toast } from "sonner";
import { useQuery, useMutation, createConnectQueryKey } from "@connectrpc/connect-query";
import { useQueryClient } from "@tanstack/react-query";
import { listQuestions } from "@/pb/drill/v1/question-QuestionService_connectquery";
import { listSessions } from "@/pb/drill/v1/session-SessionService_connectquery";
import { getCoachAnalysis, requestCoachAnalysis } from "@/pb/drill/v1/coach-CoachService_connectquery";
import { Difficulty, QuestionSource } from "@/pb/drill/v1/question_pb";
import { SessionStatus } from "@/pb/drill/v1/session_pb";
import type { Question as ProtoQuestion } from "@/pb/drill/v1/question_pb";
import { getMe } from "@/pb/drill/v1/user-UserService_connectquery";
import { UserPlan } from "@/pb/drill/v1/user_pb";
import type { SessionSummary } from "@/pb/drill/v1/session_pb";
import type { CoachAnalysis } from "@/pb/drill/v1/coach_pb";
import {
  useCreateQuestion,
} from "@/api/queries";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogFooter, DialogTrigger,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Tooltip, TooltipTrigger, TooltipContent, TooltipProvider,
} from "@/components/ui/tooltip";
import { scoreColor } from "@/lib/score-utils";

// DB constraint enforces difficulty IN ('medium', 'hard'), so UNSPECIFIED
// should never appear in ListQuestions responses. Defaults are defensive.
function difficultyVariant(d: Difficulty): "secondary" | "destructive" {
  return d === Difficulty.HARD ? "destructive" : "secondary";
}

function difficultyLabel(d: Difficulty): string {
  switch (d) {
    case Difficulty.HARD: return "hard";
    case Difficulty.MEDIUM: return "medium";
    default: return "unknown";
  }
}

// Derived data helpers
function reviewedSessions(sessions: SessionSummary[]): SessionSummary[] {
  return sessions.filter((s) => s.status === SessionStatus.REVIEWED && !s.archiveTime);
}

function activeSessions(sessions: SessionSummary[]): SessionSummary[] {
  return sessions.filter((s) => s.status === SessionStatus.ACTIVE);
}

function SessionBarChart({ sessions }: { sessions: SessionSummary[] }) {
  const reviewed = reviewedSessions(sessions);
  if (reviewed.length === 0) return null;

  const sorted = [...reviewed]
    .filter((s) => s.scoreOverall != null)
    .sort((a, b) => {
      const ta = a.createTime ? Number(a.createTime.seconds) : 0;
      const tb = b.createTime ? Number(b.createTime.seconds) : 0;
      return ta - tb;
    });
  if (sorted.length === 0) return null;

  const latest = sorted[sorted.length - 1]!;
  const previous = sorted.length >= 2 ? sorted[sorted.length - 2]! : undefined;
  const latestScore = latest.scoreOverall ?? 0;
  const trendArrow =
    previous?.scoreOverall != null && latest.scoreOverall != null
      ? latest.scoreOverall > previous.scoreOverall
        ? " \u2191"
        : latest.scoreOverall < previous.scoreOverall
          ? " \u2193"
          : ""
      : "";

  return (
    <div>
      <TooltipProvider>
        <div className="flex items-end gap-1 h-12">
          {sorted.map((s, i) => {
            const score = s.scoreOverall ?? 0;
            const heightPct = Math.max((score / 5) * 100, 4);
            const isLatest = i === sorted.length - 1;
            return (
              <Tooltip key={s.id}>
                <TooltipTrigger
                  render={
                    <Link
                      to={`/sessions/${s.id}/overview`}
                      className="flex-1 rounded-t transition-opacity hover:opacity-75"
                      style={{
                        height: `${heightPct}%`,
                        backgroundColor: scoreColor(score),
                        outline: isLatest ? "2px solid rgba(0,0,0,0.15)" : undefined,
                        outlineOffset: isLatest ? "1px" : undefined,
                      }}
                    />
                  }
                />
                <TooltipContent>
                  {s.questionTitle || "Session"} &middot; {score}/5
                </TooltipContent>
              </Tooltip>
            );
          })}
        </div>
      </TooltipProvider>
      <div className="h-px bg-border mt-0.5 mb-1.5" />
      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <span>{sorted.length} sessions</span>
        <span style={{ color: scoreColor(latestScore) }} className="font-semibold">
          Latest {latestScore}/5{trendArrow}
        </span>
      </div>
    </div>
  );
}

// CoachCard
function CoachCard({ coach, isActive }: {
  coach: CoachAnalysis | undefined;
  isActive: boolean;
}) {
  const [modalOpen, setModalOpen] = useState(false);
  const qc = useQueryClient();
  const coachAnalysisKey = createConnectQueryKey({ schema: getCoachAnalysis, input: {}, cardinality: "finite" });
  const requestCoach = useMutation(requestCoachAnalysis, {
    onSuccess: () => {
      toast.success("Coach analysis requested");
      qc.invalidateQueries({ queryKey: coachAnalysisKey });
    },
  });

  if (!isActive) return null;

  if (requestCoach.isPending) {
    return (
      <div className="rounded-xl border border-border bg-card px-5 py-4">
        <p className="text-sm text-muted-foreground animate-pulse">
          Analyzing your progress...
        </p>
      </div>
    );
  }

  if (!coach) {
    return (
      <div className="rounded-xl border border-border bg-card px-5 py-4 flex items-center justify-between">
        <p className="text-sm text-muted-foreground">
          Get strategic coaching based on your sessions.
        </p>
        <Button
          variant="outline"
          size="sm"
          onClick={() => requestCoach.mutate({})}
        >
          Get strategic coaching
        </Button>
      </div>
    );
  }

  return (
    <>
      <div className="rounded-xl border border-amber-300/40 bg-amber-50/30 dark:border-amber-500/20 dark:bg-amber-950/20 px-5 py-4 space-y-3 cursor-pointer" onClick={() => setModalOpen(true)}>
        <span className="text-[10px] font-bold tracking-wider uppercase text-muted-foreground">
          Coach
        </span>
        {coach.summary && (
          <p className="text-sm italic text-foreground/80 leading-relaxed border-l-[3px] border-amber-400/60 pl-3">
            &ldquo;{coach.summary}&rdquo;
          </p>
        )}
        <div className="flex items-center gap-2 flex-wrap">
          {coach.weakestDimension && (
            <span className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-red-100 text-red-700 dark:bg-red-900/30 dark:text-red-400">
              &darr; {coach.weakestDimension}
            </span>
          )}
          {coach.improvingDimensions.map((dim) => (
            <span
              key={dim}
              className="text-xs font-semibold px-2.5 py-0.5 rounded-full bg-green-100 text-green-700 dark:bg-green-900/30 dark:text-green-400"
            >
              &uarr; {dim}
            </span>
          ))}
          <span className="ml-auto text-xs text-muted-foreground hover:text-foreground transition-colors cursor-pointer">
            Read full analysis &rarr;
          </span>
        </div>
      </div>

      <CoachAnalysisModal
        open={modalOpen}
        onOpenChange={setModalOpen}
        narrative={coach.narrative}
      />
    </>
  );
}

// TODO: Upgrade modal to dedicated /coach page.
// Add session history, dimension trend charts over time.
function CoachAnalysisModal({
  open,
  onOpenChange,
  narrative,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  narrative: string;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl max-h-[80vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Coach Analysis</DialogTitle>
        </DialogHeader>
        <div className="text-sm text-foreground leading-relaxed">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              h2: ({ children, ...props }) => <h2 {...props} className="text-sm font-semibold tracking-wide uppercase text-muted-foreground mt-6 mb-2 first:mt-0">{children}</h2>,
              h3: ({ children, ...props }) => <h3 {...props} className="text-sm font-semibold text-muted-foreground mt-6 mb-2 first:mt-0">{children}</h3>,
              h4: ({ children, ...props }) => <h4 {...props} className="text-sm font-semibold text-muted-foreground mt-6 mb-2 first:mt-0">{children}</h4>,
              p: ({ children, ...props }) => <p {...props} className="mb-3 last:mb-0 text-sm leading-relaxed text-foreground/90">{children}</p>,
              ul: ({ children, ...props }) => <ul {...props} className="list-disc pl-5 mb-3 space-y-1 text-sm text-foreground/90">{children}</ul>,
              ol: ({ children, ...props }) => <ol {...props} className="list-decimal pl-5 mb-3 space-y-1 text-sm text-foreground/90">{children}</ol>,
              li: ({ children, ...props }) => <li {...props} className="leading-relaxed">{children}</li>,
              strong: ({ children, ...props }) => <strong {...props} className="font-semibold">{children}</strong>,
              hr: ({ ...props }) => <hr {...props} className="my-4 border-border" />,
            }}
          >
            {narrative}
          </ReactMarkdown>
        </div>
      </DialogContent>
    </Dialog>
  );
}

// ActiveSessionBanner
function ActiveSessionBanner({
  sessions,
  atConcurrentLimit,
}: {
  sessions: SessionSummary[];
  atConcurrentLimit: boolean;
}) {
  const active = activeSessions(sessions);
  if (active.length === 0) return null;

  const session = active[0]!;

  return (
    <div
      id="active-session-banner"
      className="rounded-lg bg-blue-600 px-5 py-4 space-y-2"
    >
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-3 min-w-0">
          <span className="flex-shrink-0 h-2.5 w-2.5 rounded-full bg-white animate-pulse" />
          <p className="text-sm text-white">
            Session in progress
            {session.questionTitle ? (
              <> &mdash; <span className="font-semibold">{session.questionTitle}</span></>
            ) : null}
          </p>
        </div>
        <div className="flex items-center gap-2 flex-shrink-0">
          <Link
            to={`/sessions/${session.id}/interview`}
            className="bg-white text-blue-600 font-semibold px-4 py-2 rounded text-sm leading-none hover:bg-blue-50 transition-colors"
          >
            Resume
          </Link>
          <Link
            to={`/sessions/${session.id}/interview`}
            className="text-sm text-blue-100 hover:text-white underline underline-offset-2"
          >
            Go to session
          </Link>
        </div>
      </div>
      {atConcurrentLimit && (
        <p className="text-xs text-blue-100">
          Resume or end your active session to start a new one.
        </p>
      )}
    </div>
  );
}

// HeroQuestionCard — full-width recommended question
function HeroQuestionCard({
  question,
  startDisabled,
  labelOverride,
}: {
  question: ProtoQuestion;
  startDisabled: boolean;
  labelOverride?: string;
}) {
  const href = startDisabled ? undefined : `/sessions/new?question=${question.id}`;

  const [imgError, setImgError] = useState(false);
  const image = question.imageUrl && !imgError ? (
    <img
      src={question.imageUrl}
      alt={question.title}
      className="w-[55%] aspect-[4/3] object-cover rounded-l-[14px] flex-shrink-0"
      onError={() => setImgError(true)}
    />
  ) : (
    <div
      className="w-[55%] aspect-[4/3] rounded-l-[14px] flex-shrink-0"
      style={{ background: "linear-gradient(135deg, hsl(32 40% 85%), hsl(24 30% 75%))" }}
    />
  );

  const content = (
    <div className="flex rounded-[14px] border border-border bg-card overflow-hidden transition-shadow hover:shadow-lg">
      {image}
      <div className="flex flex-col justify-center px-7 py-6 flex-1">
        <span className="text-xs font-semibold uppercase tracking-wide text-amber-600 dark:text-amber-400 mb-2">
          {labelOverride ?? "Recommended by Coach"}
        </span>
        <span className="text-xl font-bold mb-2">{question.title}</span>
        <div className="flex items-center gap-1.5 flex-wrap">
          <Badge variant={difficultyVariant(question.difficulty)}>
            {difficultyLabel(question.difficulty)}
          </Badge>
          {question.tags.map((tag) => (
            <span key={tag} className="text-xs text-muted-foreground">{tag}</span>
          ))}
        </div>
      </div>
    </div>
  );

  if (startDisabled) {
    return <div className="opacity-60 cursor-not-allowed col-span-full">{content}</div>;
  }

  return (
    <Link to={href!} className="no-underline text-inherit col-span-full">
      {content}
    </Link>
  );
}

// QuestionCard — borderless image grid card
function QuestionCard({
  question,
  startDisabled,
}: {
  question: ProtoQuestion;
  startDisabled: boolean;
}) {
  const href = startDisabled ? undefined : `/sessions/new?question=${question.id}`;

  const [imgError, setImgError] = useState(false);
  const image = question.imageUrl && !imgError ? (
    <img
      src={question.imageUrl}
      alt={question.title}
      className="w-full aspect-[4/3] object-cover transition-transform duration-250 ease-out group-hover:scale-[1.02]"
      onError={() => setImgError(true)}
    />
  ) : (
    <div
      className="w-full aspect-[4/3]"
      style={{ background: "linear-gradient(135deg, hsl(32 40% 85%), hsl(24 30% 75%))" }}
    />
  );

  const content = (
    <>
      <div className="overflow-hidden rounded-[14px]">
        {image}
      </div>
      <div className="pt-3 px-0.5">
        <p className="text-base font-semibold leading-snug">{question.title}</p>
        <div className="flex items-center gap-1.5 flex-wrap mt-1.5">
          <Badge variant={difficultyVariant(question.difficulty)}>
            {difficultyLabel(question.difficulty)}
          </Badge>
          {question.source === QuestionSource.CUSTOM && (
            <Badge variant="outline">Custom</Badge>
          )}
          {question.source === QuestionSource.COACH_GENERATED && (
            <Badge variant="outline" className="text-amber-600 dark:text-amber-400 border-amber-400/50">
              Coach
            </Badge>
          )}
          {question.tags.map((tag) => (
            <span key={tag} className="text-xs text-muted-foreground">{tag}</span>
          ))}
        </div>
      </div>
    </>
  );

  if (startDisabled) {
    return (
      <div className="group opacity-60 cursor-not-allowed">
        {content}
      </div>
    );
  }

  return (
    <Link to={href!} className="group no-underline text-inherit">
      {content}
    </Link>
  );
}

// CreateQuestionDialog
function CreateQuestionDialog() {
  const [open, setOpen] = useState(false);
  const [title, setTitle] = useState("");
  const [prompt, setPrompt] = useState("");
  const [difficulty, setDifficulty] = useState("medium");
  const [tagsInput, setTagsInput] = useState("");
  const createQuestion = useCreateQuestion();

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    const tags = tagsInput
      .split(",")
      .map((t) => t.trim())
      .filter(Boolean);
    createQuestion.mutate(
      { title, prompt, difficulty, tags },
      {
        onSuccess: () => {
          setOpen(false);
          setTitle("");
          setPrompt("");
          setDifficulty("medium");
          setTagsInput("");
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger
        render={
          <div className="rounded-[14px] border-[1.5px] border-dashed border-border flex flex-col items-center justify-center gap-2 cursor-pointer text-muted-foreground hover:text-foreground hover:border-foreground/30 transition-colors aspect-[4/3]">
            <span className="text-2xl font-light leading-none">+</span>
            <span className="text-xs">New question</span>
          </div>
        }
      />
      <DialogContent>
        <form onSubmit={handleSubmit}>
          <DialogHeader>
            <DialogTitle>Create a question</DialogTitle>
          </DialogHeader>
          <div className="mt-4 space-y-3">
            <div className="space-y-1.5">
              <Label htmlFor="cq-title">Title</Label>
              <Input
                id="cq-title"
                value={title}
                onChange={(e) => setTitle(e.target.value)}
                placeholder="Design a rate limiter"
                required
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="cq-prompt">Prompt</Label>
              <textarea
                id="cq-prompt"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder="Describe the full problem..."
                required
                className="flex min-h-[120px] w-full rounded-lg border border-input bg-transparent px-3 py-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/50 resize-y"
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="cq-difficulty">Difficulty</Label>
              <select
                id="cq-difficulty"
                value={difficulty}
                onChange={(e) => setDifficulty(e.target.value)}
                className="h-8 w-full rounded-lg border border-input bg-transparent px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/50"
              >
                <option value="medium">Medium</option>
                <option value="hard">Hard</option>
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="cq-tags">Tags (comma-separated)</Label>
              <Input
                id="cq-tags"
                value={tagsInput}
                onChange={(e) => setTagsInput(e.target.value)}
                placeholder="caching, scaling"
              />
            </div>
          </div>
          <DialogFooter className="mt-4">
            <Button type="submit" disabled={createQuestion.isPending}>
              {createQuestion.isPending ? "Creating..." : "Create"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// NewUserHero — hero-promoted question for brand-new users
function NewUserHero({
  questions,
  startDisabled,
}: {
  questions: ProtoQuestion[];
  startDisabled: boolean;
}) {
  const [randomSeed] = useState(() => Math.random());
  const suggested = useMemo(() => {
    const medium = questions.filter((q) => q.difficulty === Difficulty.MEDIUM);
    const pool = medium.length > 0 ? medium : questions;
    if (pool.length === 0) return null;
    return pool[Math.floor(randomSeed * pool.length)];
  }, [questions, randomSeed]);

  if (!suggested) return null;

  return (
    <HeroQuestionCard
      question={suggested}
      startDisabled={startDisabled}
      labelOverride="Get started"
    />
  );
}

// Home
export default function Home() {
  const { data: meData } = useQuery(getMe, {});
  const user = meData?.user;
  const { data: questionsResp } = useQuery(listQuestions, {});
  const questions = useMemo(
    () => questionsResp?.questions ?? [],
    [questionsResp],
  );
  const { data: sessionsResp } = useQuery(listSessions, {});
  const sessions = sessionsResp?.sessions ?? [];
  const { data: coachResp } = useQuery(getCoachAnalysis, {});
  const coach = coachResp?.analysis;

  const reviewed = reviewedSessions(sessions);
  const active = activeSessions(sessions);
  const reviewedCount = reviewed.length;
  // TODO: concurrent limit should come from the backend (plan capabilities endpoint).
  // Hardcoded until the backend exposes per-plan limits via the /api/me or /api/usage response.
  const concurrentLimit = user?.plan === UserPlan.PRO ? 2 : 1;
  const atConcurrentLimit = active.length >= concurrentLimit;
  // Tier
  const visibleSessions = sessions.filter(s => !s.archiveTime);
  const isNew = visibleSessions.length === 0;
  const isActive = reviewedCount >= 3;

  const suggestedId = coach?.suggestedQuestionId ?? null;
  const heroQuestion = suggestedId ? questions.find((q) => q.id === suggestedId) : undefined;

  if (!user) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-5 w-64" />
        <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-8">
          <Skeleton className="aspect-[4/3] rounded-[14px]" />
          <Skeleton className="aspect-[4/3] rounded-[14px]" />
          <Skeleton className="aspect-[4/3] rounded-[14px]" />
        </div>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      {/* Welcome / Summary — new user hero promotion */}
      {isNew && !heroQuestion && (
        <NewUserHero questions={questions} startDisabled={atConcurrentLimit} />
      )}

      <SessionBarChart sessions={sessions} />

      {/* Coach card — only for active users */}
      {isActive && <CoachCard coach={coach} isActive={isActive} />}

      {/* Active session banner */}
      <ActiveSessionBanner
        sessions={sessions}
        atConcurrentLimit={atConcurrentLimit}
      />

      {/* Question list header */}
      <h2 className="text-base font-medium">Questions</h2>

      {/* Question grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-8">
        {heroQuestion && (
          <HeroQuestionCard
            question={heroQuestion}
            startDisabled={atConcurrentLimit}
          />
        )}
        {questions
          .filter((q) => q.id !== suggestedId)
          .map((q) => (
            <QuestionCard
              key={q.id}
              question={q}
              startDisabled={atConcurrentLimit}
            />
          ))}
        <CreateQuestionDialog />
      </div>
    </div>
  );
}
