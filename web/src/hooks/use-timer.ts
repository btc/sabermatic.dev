import { useState, useEffect, useRef } from "react";

export type TimerPhase = "normal" | "warning" | "overtime";

export interface TimerState {
  elapsed: number;
  phase: TimerPhase;
  display: string;
}

export function useTimer(startedAt: string | null, durationMinutes: number) {
  const [elapsed, setElapsed] = useState(0);
  const intervalRef = useRef<ReturnType<typeof setInterval>>();
  const warningThresholdRef = useRef(0);

  useEffect(() => {
    if (!startedAt) return;

    warningThresholdRef.current = Math.max(2, Math.min(5, Math.round(durationMinutes / 9)));

    const start = new Date(startedAt).getTime();
    const tick = () => setElapsed(Math.floor((Date.now() - start) / 1000));
    tick();
    intervalRef.current = setInterval(tick, 1000);
    return () => clearInterval(intervalRef.current);
  }, [startedAt, durationMinutes]);

  const totalSeconds = durationMinutes * 60;
  const remaining = totalSeconds - elapsed;
  const warningAt = warningThresholdRef.current * 60;

  let phase: TimerPhase = "normal";
  if (remaining <= 0) phase = "overtime";
  else if (remaining <= warningAt) phase = "warning";

  const absRemaining = Math.abs(remaining);
  const m = Math.floor(absRemaining / 60);
  const s = absRemaining % 60;
  const prefix = remaining < 0 ? "+" : "";
  const display = `${prefix}${m}:${s.toString().padStart(2, "0")}`;

  return { elapsed, phase, display };
}
