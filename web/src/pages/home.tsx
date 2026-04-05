import { useState, useMemo } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { toast } from "sonner";
import { useQuery } from "@connectrpc/connect-query";
import { listQuestions } from "@/pb/drill/v1/question-QuestionService_connectquery";
import { Difficulty, QuestionSource } from "@/pb/drill/v1/question_pb";
import type { Question as ProtoQuestion } from "@/pb/drill/v1/question_pb";
import {
  useMe, useSessions, useCoachLatest,
  useRequestCoachAnalysis, useCreateQuestion,
} from "@/api/queries";
import type { Session, CoachAnalysis } from "@/api/types";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
  DialogFooter, DialogTrigger,
} from "@/components/ui/dialog";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { Skeleton } from "@/components/ui/skeleton";

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
function reviewedSessions(sessions: Session[]): Session[] {
  return sessions.filter((s) => s.status === "reviewed");
}

function activeSessions(sessions: Session[]): Session[] {
  return sessions.filter((s) => s.status === "active");
}

function allTags(questions: ProtoQuestion[]): string[] {
  const set = new Set<string>();
  for (const q of questions) {
    for (const t of q.tags) set.add(t);
  }
  return Array.from(set).sort();
}

// SummaryStrip
function SummaryStrip({ sessions }: { sessions: Session[] }) {
  const reviewed = reviewedSessions(sessions);
  if (reviewed.length === 0) return null;

  return (
    <div className="flex items-center gap-6 text-sm">
      <div>
        <span className="text-muted-foreground">Sessions completed</span>{" "}
        <span className="font-medium">{reviewed.length}</span>
      </div>
    </div>
  );
}

// TODO: ScoreSparkline — render once real score_overall data is available on the Session type.
// Removed fake/placeholder data that was previously rendered here.

// CoachCard
function CoachCard({ coach, isActive }: {
  coach: CoachAnalysis | null | undefined;
  isActive: boolean;
}) {
  const requestCoach = useRequestCoachAnalysis();

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
            onClick={() => requestCoach.mutate(undefined, {
              onSuccess: () => toast.success("Coach analysis updated"),
            })}
          >
            Get strategic coaching
          </Button>
        </CardContent>
      </Card>
    );
  }

  const narrativeExcerpt =
    coach.narrative.length > 300
      ? coach.narrative.slice(0, 300) + "…"
      : coach.narrative;

  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between">
          <CardTitle>Coach</CardTitle>
          <Button
            variant="ghost"
            size="sm"
            onClick={() => requestCoach.mutate(undefined, {
              onSuccess: () => toast.success("Coach analysis updated"),
            })}
            disabled={requestCoach.isPending}
          >
            Refresh
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        <p className="text-sm text-foreground leading-relaxed">
          {narrativeExcerpt}
        </p>
        {coach.weakest_dimension && (
          <div className="flex items-center gap-2">
            <span className="text-xs text-muted-foreground">Focus area:</span>
            <Badge variant="outline">{coach.weakest_dimension}</Badge>
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
  sessions: Session[];
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
            {session.question_title ? (
              <> &mdash; <span className="font-semibold">{session.question_title}</span></>
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

// QuestionCard
function QuestionCard({
  question,
  suggestedId,
  startDisabled,
}: {
  question: ProtoQuestion;
  suggestedId: string | null;
  startDisabled: boolean;
}) {
  const isRecommended = suggestedId === question.id;

  const cardContent = (
    <div
      className={`relative flex items-start justify-between gap-4 rounded-lg border px-4 py-3 ${
        isRecommended ? "border-amber-400/50 bg-amber-50/40 dark:bg-amber-900/10" : "border-border bg-card"
      } ${startDisabled ? "opacity-60 cursor-pointer" : ""}`}
      onClick={
        startDisabled
          ? () => {
              document
                .getElementById("active-session-banner")
                ?.scrollIntoView({ behavior: "smooth" });
            }
          : undefined
      }
      {...(startDisabled
        ? {
            role: "button",
            tabIndex: 0,
            onKeyDown: (e: React.KeyboardEvent) => {
              if (e.key === "Enter" || e.key === " ") {
                document.getElementById("active-session-banner")?.scrollIntoView({ behavior: "smooth" });
              }
            },
          }
        : {})}
    >
      {startDisabled && (
        <div className="absolute inset-0 rounded-lg flex items-center justify-center z-10 bg-card/70 backdrop-blur-sm cursor-pointer">
          <p className="text-xs text-muted-foreground text-center px-4">
            Resume or end your active session to start a new one
          </p>
        </div>
      )}
      <div className="min-w-0 flex-1 space-y-1.5">
        <div className="flex items-center gap-2 flex-wrap">
          <span className="text-sm font-medium">{question.title}</span>
          <Badge variant={difficultyVariant(question.difficulty)}>
            {difficultyLabel(question.difficulty)}
          </Badge>
          {question.source === QuestionSource.CUSTOM && (
            <Badge variant="outline">Custom</Badge>
          )}
          {question.source === QuestionSource.COACH_GENERATED && (
            <TooltipProvider>
              <Tooltip>
                <TooltipTrigger render={<span />}>
                  <Badge variant="outline" className="text-amber-600 dark:text-amber-400 border-amber-400/50">
                    Coach
                  </Badge>
                </TooltipTrigger>
                <TooltipContent>
                  Generated by coach analysis
                </TooltipContent>
              </Tooltip>
            </TooltipProvider>
          )}
          {isRecommended && (
            <span className="text-xs font-medium text-amber-600 dark:text-amber-400">
              Recommended by coach
            </span>
          )}
        </div>
        <div className="flex items-center gap-3 flex-wrap">
          {question.tags.map((tag) => (
            <span key={tag} className="text-xs text-muted-foreground">
              {tag}
            </span>
          ))}
        </div>
      </div>
      {startDisabled ? (
        <Button variant="outline" size="sm" disabled>
          Start
        </Button>
      ) : (
        <Link
          to={`/sessions/new?question=${question.id}`}
          className={buttonVariants({ variant: "outline", size: "sm" })}
        >
          Start
        </Link>
      )}
    </div>
  );

  if (startDisabled) {
    return (
      <TooltipProvider>
        <Tooltip>
          <TooltipTrigger render={<div />} className="w-full text-left">
            {cardContent}
          </TooltipTrigger>
          <TooltipContent>Active session in progress</TooltipContent>
        </Tooltip>
      </TooltipProvider>
    );
  }

  return cardContent;
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
  const { data: user } = useMe();
  const { data: questionsResp } = useQuery(listQuestions, {});
  const questions = useMemo(
    () => questionsResp?.questions ?? [],
    [questionsResp],
  );
  const { data: sessions = [] } = useSessions();
  const { data: coach } = useCoachLatest();

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
  const concurrentLimit = user?.plan === "pro" ? 2 : 1;
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

  // Sort: recommended first if coach suggests one
  const suggestedId = coach?.suggested_question_id ?? null;
  const sorted = useMemo(() => {
    if (!suggestedId) return filtered;
    return [...filtered].sort((a, b) => {
      if (a.id === suggestedId) return -1;
      if (b.id === suggestedId) return 1;
      return 0;
    });
  }, [filtered, suggestedId]);

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
        <div className="space-y-2">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-16 w-full" />
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

      {/* Question list */}
      <div className="space-y-2">
        {sorted.map((q) => (
          <QuestionCard
            key={q.id}
            question={q}
            suggestedId={suggestedId}
            startDisabled={atConcurrentLimit}
          />
        ))}
        {sorted.length === 0 && (
          <p className="text-sm text-muted-foreground py-8 text-center">
            No questions match your filters.
          </p>
        )}
      </div>
    </div>
  );
}
