import { useState, useMemo } from "react";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import {
  LineChart,
  Line,
  ResponsiveContainer,
  Tooltip as RechartsTooltip,
} from "recharts";
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
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogFooter, DialogTrigger,
} from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
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
  return sessions.filter((s) => s.status === SessionStatus.REVIEWED);
}

function activeSessions(sessions: SessionSummary[]): SessionSummary[] {
  return sessions.filter((s) => s.status === SessionStatus.ACTIVE);
}

function allTags(questions: ProtoQuestion[]): string[] {
  const set = new Set<string>();
  for (const q of questions) {
    for (const t of q.tags) set.add(t);
  }
  return Array.from(set).sort();
}


function ScoreSparkline({ sessions }: { sessions: SessionSummary[] }) {
  const points = useMemo(() => {
    return sessions
      .filter((s) => s.scoreOverall != null)
      .sort((a, b) => {
        const ta = a.createTime ? Number(a.createTime.seconds) : 0;
        const tb = b.createTime ? Number(b.createTime.seconds) : 0;
        return ta - tb;
      })
      .map((s) => ({
        score: s.scoreOverall!,
        label: s.questionTitle || "Session",
      }));
  }, [sessions]);

  if (points.length < 2) return null;

  const lastScore = points[points.length - 1]!.score;

  return (
    <div className="flex items-center gap-3">
      <div className="h-8 w-24">
        <ResponsiveContainer width="100%" height="100%">
          <LineChart data={points}>
            <Line
              type="monotone"
              dataKey="score"
              stroke="hsl(32 95% 44%)"
              strokeWidth={1.5}
              dot={{ r: 2, fill: "hsl(32 95% 44%)", strokeWidth: 0 }}
              isAnimationActive={false}
            />
            <RechartsTooltip
              content={({ active, payload }) => {
                if (!active || !payload?.length) return null;
                const p = payload[0]!.payload as { score: number; label: string };
                return (
                  <div className="rounded border border-border bg-popover px-2 py-1 text-xs shadow">
                    <p className="font-medium">{p.label}</p>
                    <p style={{ color: scoreColor(p.score) }}>{p.score}/5</p>
                  </div>
                );
              }}
            />
          </LineChart>
        </ResponsiveContainer>
      </div>
      <span className="text-sm font-medium" style={{ color: scoreColor(lastScore) }}>
        {lastScore}/5
      </span>
    </div>
  );
}

// SummaryStrip
function SummaryStrip({ sessions }: { sessions: SessionSummary[] }) {
  const reviewed = reviewedSessions(sessions);
  if (reviewed.length === 0) return null;

  return (
    <div className="flex items-center gap-6 text-sm">
      <div>
        <span className="text-muted-foreground">Sessions completed</span>{" "}
        <span className="font-medium">{reviewed.length}</span>
      </div>
      <ScoreSparkline sessions={reviewed} />
    </div>
  );
}

// CoachCard
function CoachCard({ coach, isActive }: {
  coach: CoachAnalysis | undefined;
  isActive: boolean;
}) {
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
      <Card>
        <CardContent className="py-6">
          <p className="text-sm text-muted-foreground animate-pulse">
            Analyzing your progress...
          </p>
        </CardContent>
      </Card>
    );
  }

  if (!coach) {
    return (
      <Card>
        <CardContent className="py-6 flex items-center justify-between">
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
        </CardContent>
      </Card>
    );
  }

  return (
    <Card className="border-amber-300/40 bg-amber-50/30 dark:border-amber-500/20 dark:bg-amber-950/20">
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle>Coach</CardTitle>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => requestCoach.mutate({})}
            disabled={requestCoach.isPending}
          >
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <div className="text-sm text-foreground leading-relaxed">
          <ReactMarkdown
            remarkPlugins={[remarkGfm]}
            components={{
              p: ({ children, ...props }) => <p {...props} className="mb-2 last:mb-0">{children}</p>,
              ul: ({ children, ...props }) => <ul {...props} className="list-disc pl-5 mb-2 space-y-1">{children}</ul>,
              ol: ({ children, ...props }) => <ol {...props} className="list-decimal pl-5 mb-2 space-y-1">{children}</ol>,
              strong: ({ children, ...props }) => <strong {...props} className="font-semibold">{children}</strong>,
            }}
          >
            {coach.narrative}
          </ReactMarkdown>
        </div>
        {coach.weakestDimension && (
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">Focus area:</span>
            <Badge variant="outline">{coach.weakestDimension}</Badge>
          </div>
        )}
      </CardContent>
    </Card>
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

// QuestionFilters
function QuestionFilters({
  allTagList,
  difficulty,
  selectedTags,
  onDifficultyChange,
  onTagToggle,
}: {
  allTagList: string[];
  difficulty: string;
  selectedTags: Set<string>;
  onDifficultyChange: (v: string) => void;
  onTagToggle: (tag: string) => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-3">
      <select
        value={difficulty}
        onChange={(e) => onDifficultyChange(e.target.value)}
        className="h-8 rounded-lg border border-input bg-transparent px-2 text-sm text-foreground outline-none focus:border-ring focus:ring-2 focus:ring-ring/50"
      >
        <option value="">All difficulties</option>
        <option value="medium">Medium</option>
        <option value="hard">Hard</option>
      </select>
      {allTagList.map((tag) => (
        <button
          key={tag}
          type="button"
          onClick={() => onTagToggle(tag)}
          className={`inline-flex h-6 items-center rounded-full border px-2.5 text-xs font-medium transition-colors cursor-pointer ${
            selectedTags.has(tag)
              ? "border-primary bg-primary/10 text-primary"
              : "border-border text-muted-foreground hover:text-foreground"
          }`}
        >
          {tag}
        </button>
      ))}
    </div>
  );
}

// HeroQuestionCard — full-width recommended question
function HeroQuestionCard({
  question,
  startDisabled,
}: {
  question: ProtoQuestion;
  startDisabled: boolean;
}) {
  const href = startDisabled ? undefined : `/sessions/new?question=${question.id}`;

  const image = question.imageUrl ? (
    <img
      src={question.imageUrl}
      alt={question.title}
      className="w-[55%] aspect-[4/3] object-cover rounded-l-[14px] flex-shrink-0"
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
          Recommended by Coach
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

  const image = question.imageUrl ? (
    <img
      src={question.imageUrl}
      alt={question.title}
      className="w-full aspect-[4/3] object-cover transition-transform duration-250 ease-out group-hover:scale-[1.02]"
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
      <div className="pt-2.5 px-0.5 space-y-1">
        <span className="text-sm font-semibold">{question.title}</span>
        <div className="flex items-center gap-1.5 flex-wrap">
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
      <DialogTrigger render={<Button variant="outline" size="sm" />}>
        Create question
      </DialogTrigger>
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
              <Input
                id="cq-prompt"
                value={prompt}
                onChange={(e) => setPrompt(e.target.value)}
                placeholder="Describe the full problem..."
                required
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

  const [searchParams, setSearchParams] = useSearchParams();

  const difficulty = searchParams.get("difficulty") ?? "";
  const tagsParam = searchParams.get("tags") ?? "";
  const selectedTags = useMemo(
    () => new Set(tagsParam ? tagsParam.split(",") : []),
    [tagsParam],
  );

  const reviewed = reviewedSessions(sessions);
  const active = activeSessions(sessions);
  const reviewedCount = reviewed.length;
  // TODO: concurrent limit should come from the backend (plan capabilities endpoint).
  // Hardcoded until the backend exposes per-plan limits via the /api/me or /api/usage response.
  const concurrentLimit = user?.plan === UserPlan.PRO ? 2 : 1;
  const atConcurrentLimit = active.length >= concurrentLimit;
  const tagList = useMemo(() => allTags(questions), [questions]);

  // Tier
  const isNew = reviewedCount === 0;
  const isReturning = reviewedCount >= 1 && reviewedCount <= 2;
  const isActive = reviewedCount >= 3;

  // Map URL param string to proto enum for filtering
  const difficultyEnum = useMemo(() => {
    if (difficulty === "medium") return Difficulty.MEDIUM;
    if (difficulty === "hard") return Difficulty.HARD;
    return undefined;
  }, [difficulty]);

  // Filter questions
  const filtered = useMemo(() => {
    let list = questions;
    if (difficultyEnum !== undefined) {
      list = list.filter((q) => q.difficulty === difficultyEnum);
    }
    if (selectedTags.size > 0) {
      list = list.filter((q) => q.tags.some((t) => selectedTags.has(t)));
    }
    return list;
  }, [questions, difficultyEnum, selectedTags]);

  const suggestedId = coach?.suggestedQuestionId ?? null;
  const heroQuestion = suggestedId ? filtered.find((q) => q.id === suggestedId) : undefined;

  function setDifficulty(v: string) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      if (v) next.set("difficulty", v);
      else next.delete("difficulty");
      return next;
    });
  }

  function toggleTag(tag: string) {
    setSearchParams((prev) => {
      const next = new URLSearchParams(prev);
      const current = new Set(
        (next.get("tags") ?? "").split(",").filter(Boolean),
      );
      if (current.has(tag)) current.delete(tag);
      else current.add(tag);
      if (current.size > 0) next.set("tags", Array.from(current).join(","));
      else next.delete("tags");
      return next;
    });
  }

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
      {/* Welcome / Summary */}
      {isNew && (
        <p className="text-sm text-muted-foreground">
          Pick a question to start practicing. 30 minutes is a good first session.
        </p>
      )}

      {(isReturning || isActive) && (
        <div className="flex items-center gap-6">
          <SummaryStrip sessions={sessions} />
        </div>
      )}

      {/* Coach card — only for active users */}
      {isActive && <CoachCard coach={coach} isActive={isActive} />}

      {/* Active session banner */}
      <ActiveSessionBanner
        sessions={sessions}
        atConcurrentLimit={atConcurrentLimit}
      />

      {/* Question list header */}
      <div className="flex items-center justify-between">
        <h2 className="text-base font-medium">Questions</h2>
        <CreateQuestionDialog />
      </div>

      {/* Filters */}
      {tagList.length > 0 && (
        <QuestionFilters
          allTagList={tagList}
          difficulty={difficulty}
          selectedTags={selectedTags}
          onDifficultyChange={setDifficulty}
          onTagToggle={toggleTag}
        />
      )}

      {/* Question grid */}
      <div className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-x-6 gap-y-8">
        {heroQuestion && (
          <HeroQuestionCard
            question={heroQuestion}
            startDisabled={atConcurrentLimit}
          />
        )}
        {filtered
          .filter((q) => q.id !== suggestedId)
          .map((q) => (
            <QuestionCard
              key={q.id}
              question={q}
              startDisabled={atConcurrentLimit}
            />
          ))}
        {filtered.length === 0 && (
          <p className="text-sm text-muted-foreground py-8 text-center col-span-full">
            No questions match your filters.
          </p>
        )}
      </div>
    </div>
  );
}
