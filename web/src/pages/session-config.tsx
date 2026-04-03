import { useState } from "react";
import { useSearchParams, useNavigate, Link, Navigate } from "react-router-dom";
import {
  useQuestions,
  useCoachLatest,
  useCreateSession,
  useMe,
  useUsage,
} from "@/api/queries";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

const DURATION_PRESETS = [15, 30, 45, 60] as const;
const FREE_PLAN_MAX = 30;
const PRO_PLAN_MAX = 180;

type MicState = "idle" | "granted" | "denied";

function Toggle({
  checked,
  onCheckedChange,
  id,
}: {
  checked: boolean;
  onCheckedChange: (v: boolean) => void;
  id: string;
}) {
  return (
    <button
      id={id}
      type="button"
      role="switch"
      aria-checked={checked}
      onClick={() => onCheckedChange(!checked)}
      className={cn(
        "relative inline-flex h-5 w-9 shrink-0 cursor-pointer rounded-full border-2 border-transparent transition-colors",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2",
        checked
          ? "bg-amber-500"
          : "bg-muted"
      )}
    >
      <span
        className={cn(
          "pointer-events-none inline-block h-4 w-4 rounded-full bg-white shadow-lg transition-transform",
          checked ? "translate-x-4" : "translate-x-0"
        )}
      />
    </button>
  );
}

export default function SessionConfig() {
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();

  const questionId = searchParams.get("question");

  const { data: questions = [] } = useQuestions();
  const { data: coach } = useCoachLatest();
  const { data: me } = useMe();
  const { data: usage } = useUsage();
  const createSession = useCreateSession();

  const planMax = me?.plan === "pro" ? PRO_PLAN_MAX : FREE_PLAN_MAX;

  const [durationMode, setDurationMode] = useState<"preset" | "custom">("preset");
  const [durationPreset, setDurationPreset] = useState<number>(30);
  const [customDuration, setCustomDuration] = useState<string>("30");
  const [ttsEnabled, setTtsEnabled] = useState(true);
  const [coachBriefing, setCoachBriefing] = useState(true);
  const [micState, setMicState] = useState<MicState>("idle");

  // Redirect if no question param.
  // NOTE: the hooks above fire before this guard runs (React rules of hooks).
  // The in-flight requests will be cancelled by React Query cleanup on unmount,
  // so this is a minor inefficiency rather than a correctness issue.
  if (!questionId) {
    return <Navigate to="/" replace />;
  }

  const question = questions.find((q) => q.id === questionId);

  const effectiveDuration = durationMode === "preset"
    ? durationPreset
    : Math.min(Math.max(1, parseInt(customDuration, 10) || 1), planMax);

  const entitlementExceeded = usage != null && usage.total_balance < effectiveDuration;

  const hasCoach = coach != null && coach !== undefined;

  async function handleEnableMic() {
    try {
      await navigator.mediaDevices.getUserMedia({ audio: true });
      setMicState("granted");
    } catch {
      setMicState("denied");
    }
  }

  function handleCustomDurationChange(val: string) {
    // Only allow digits
    const digits = val.replace(/\D/g, "");
    setCustomDuration(digits);
  }

  function handleCustomDurationBlur() {
    const n = parseInt(customDuration, 10);
    if (isNaN(n) || n < 1) {
      setCustomDuration("1");
    } else if (n > planMax) {
      setCustomDuration(String(planMax));
    }
  }

  function handleBegin() {
    if (!questionId || entitlementExceeded || createSession.isPending) return;

    createSession.mutate(
      {
        question_id: questionId,
        duration_minutes: effectiveDuration,
        tts_enabled: ttsEnabled,
        ...(hasCoach ? { coach_briefing: coachBriefing } : {}),
      },
      {
        onSuccess: (session) => {
          navigate(`/sessions/${session.id}/interview`);
        },
      }
    );
  }

  return (
    <div className="mx-auto max-w-xl space-y-6">
      {/* Question */}
      <Card>
        <CardHeader>
          <CardTitle>
            {question ? question.title : <span className="text-muted-foreground">Loading question...</span>}
          </CardTitle>
        </CardHeader>
        {question && (
          <CardContent>
            <p className="text-sm text-foreground leading-relaxed whitespace-pre-wrap">
              {question.prompt}
            </p>
          </CardContent>
        )}
      </Card>

      {/* Configuration */}
      <Card>
        <CardHeader>
          <CardTitle>Configuration</CardTitle>
        </CardHeader>
        <CardContent className="space-y-5">
          {/* Duration */}
          <div className="space-y-2">
            <Label>Duration</Label>
            <div className="flex items-center gap-2 flex-wrap">
              {DURATION_PRESETS.map((preset) => (
                <Button
                  key={preset}
                  type="button"
                  variant={durationMode === "preset" && durationPreset === preset ? "default" : "outline"}
                  size="sm"
                  disabled={preset > planMax}
                  onClick={() => {
                    setDurationMode("preset");
                    setDurationPreset(preset);
                  }}
                >
                  {preset} min
                </Button>
              ))}
              <Button
                type="button"
                variant={durationMode === "custom" ? "default" : "outline"}
                size="sm"
                onClick={() => setDurationMode("custom")}
              >
                Custom
              </Button>
            </div>
            {durationMode === "custom" && (
              <div className="flex items-center gap-2 mt-2">
                <Input
                  type="text"
                  inputMode="numeric"
                  value={customDuration}
                  onChange={(e) => handleCustomDurationChange(e.target.value)}
                  onBlur={handleCustomDurationBlur}
                  className="w-20"
                  aria-label="Custom duration in minutes"
                />
                <span className="text-sm text-muted-foreground">
                  minutes (max {planMax})
                </span>
              </div>
            )}
            {me?.plan === "free" && (
              <p className="text-xs text-muted-foreground">
                Free plan: sessions capped at {FREE_PLAN_MAX} minutes.
              </p>
            )}
          </div>

          {/* TTS toggle */}
          <div className="flex items-center justify-between">
            <div>
              <Label htmlFor="tts-toggle">Interviewer voice responses</Label>
              <p className="text-xs text-muted-foreground mt-0.5">
                Hear the interviewer speak rather than read text.
              </p>
            </div>
            <Toggle
              id="tts-toggle"
              checked={ttsEnabled}
              onCheckedChange={setTtsEnabled}
            />
          </div>

          {/* Coach briefing toggle — only if coach data exists */}
          {hasCoach && (
            <div className="flex items-center justify-between">
              <div>
                <Label htmlFor="coach-briefing-toggle">Brief interviewer on your weak areas</Label>
                <p className="text-xs text-muted-foreground mt-0.5">
                  The interviewer will focus on dimensions where you need practice.
                </p>
              </div>
              <Toggle
                id="coach-briefing-toggle"
                checked={coachBriefing}
                onCheckedChange={setCoachBriefing}
              />
            </div>
          )}
        </CardContent>
      </Card>

      {/* Preparation tips */}
      <Card>
        <CardHeader>
          <CardTitle>Before you begin</CardTitle>
        </CardHeader>
        <CardContent>
          <ul className="space-y-2">
            {[
              "Think out loud — the interviewer evaluates your reasoning process",
              "Start with requirements, not solutions",
              "You can switch between voice and text at any time",
              "Spacebar to record when not typing",
            ].map((tip) => (
              <li key={tip} className="flex items-start gap-2 text-sm text-muted-foreground">
                <span className="mt-0.5 h-1.5 w-1.5 shrink-0 rounded-full bg-muted-foreground/50" />
                {tip}
              </li>
            ))}
          </ul>
        </CardContent>
      </Card>

      {/* Microphone */}
      <div className="rounded-xl border border-border bg-card px-4 py-4 space-y-3">
        <p className="text-sm text-foreground">
          For the best experience, use your voice. It is faster and more natural.
        </p>
        {micState === "idle" && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={handleEnableMic}
          >
            Enable microphone
          </Button>
        )}
        {micState === "granted" && (
          <div className="flex items-center gap-2 text-sm text-green-600 dark:text-green-400">
            <svg
              xmlns="http://www.w3.org/2000/svg"
              className="h-4 w-4"
              viewBox="0 0 20 20"
              fill="currentColor"
              aria-hidden="true"
            >
              <path
                fillRule="evenodd"
                d="M16.707 5.293a1 1 0 010 1.414l-8 8a1 1 0 01-1.414 0l-4-4a1 1 0 011.414-1.414L8 12.586l7.293-7.293a1 1 0 011.414 0z"
                clipRule="evenodd"
              />
            </svg>
            Microphone enabled
          </div>
        )}
        {micState === "denied" && (
          <p className="text-sm text-muted-foreground">
            You can still use text input.
          </p>
        )}
      </div>

      {/* Entitlement warning */}
      {entitlementExceeded && (
        <div className="rounded-xl border border-border bg-card px-4 py-3 text-sm text-muted-foreground">
          Not enough minutes for a {effectiveDuration}-minute session.{" "}
          <Link to="/settings/billing" className="text-foreground underline underline-offset-2">
            Buy more minutes
          </Link>{" "}
          to continue.
        </div>
      )}

      {/* Begin */}
      <Button
        type="button"
        className="w-full h-10 bg-amber-500 text-white hover:bg-amber-600 dark:bg-amber-500 dark:hover:bg-amber-600 font-medium"
        disabled={entitlementExceeded || createSession.isPending || !question}
        onClick={handleBegin}
      >
        {createSession.isPending ? "Starting..." : "Begin session"}
      </Button>
    </div>
  );
}
