import { useState, useRef, useCallback, useEffect } from "react";
import type { Message } from "@/api/types";

export interface ReplayState {
  isPlaying: boolean;
  currentTime: number;
  duration: number;
  speed: number;
  visibleMessages: number;
  activeAnnotationSeqs: number[];
}

interface ReplayOptions {
  messages: Message[];
  sessionStartedAt: string;
  sessionEndedAt: string | null;
  annotationSeqs: number[];
}

export function useReplayEngine(options: ReplayOptions | null) {
  const [state, setState] = useState<ReplayState>({
    isPlaying: false,
    currentTime: 0,
    duration: 0,
    speed: 1,
    visibleMessages: 0,
    activeAnnotationSeqs: [],
  });

  const animRef = useRef<number>(0);
  const lastTickRef = useRef<number>(0);

  const optionsRef = useRef(options);
  optionsRef.current = options;

  const messageOffsets = useRef<number[]>([]);

  useEffect(() => {
    if (!options) return;
    const start = new Date(options.sessionStartedAt).getTime();
    const end = options.sessionEndedAt
      ? new Date(options.sessionEndedAt).getTime()
      : start + 30 * 60 * 1000;

    messageOffsets.current = options.messages.map(
      (m) => (new Date(m.created_at).getTime() - start) / 1000,
    );

    setState((s) => ({ ...s, duration: (end - start) / 1000 }));
  }, [options]);

  const tick = useCallback(() => {
    const now = performance.now();
    const dt = (now - lastTickRef.current) / 1000;
    lastTickRef.current = now;

    setState((prev) => {
      if (!prev.isPlaying) return prev;

      const newTime = prev.currentTime + dt * prev.speed;
      if (newTime >= prev.duration) {
        return { ...prev, isPlaying: false, currentTime: prev.duration };
      }

      const visible = messageOffsets.current.filter((t) => t <= newTime).length;

      const opts = optionsRef.current;
      const activeAnns = opts?.annotationSeqs.filter((seq) => {
        const idx = opts.messages.findIndex((m) => m.seq === seq);
        return idx >= 0 && idx < visible;
      }) ?? [];

      return {
        ...prev,
        currentTime: newTime,
        visibleMessages: visible,
        activeAnnotationSeqs: activeAnns,
      };
    });

    animRef.current = requestAnimationFrame(tick);
  }, []);

  const play = useCallback(() => {
    lastTickRef.current = performance.now();
    setState((s) => ({ ...s, isPlaying: true }));
    animRef.current = requestAnimationFrame(tick);
  }, [tick]);

  const pause = useCallback(() => {
    cancelAnimationFrame(animRef.current);
    setState((s) => ({ ...s, isPlaying: false }));
  }, []);

  const seek = useCallback((time: number) => {
    setState((prev) => {
      const clamped = Math.max(0, Math.min(time, prev.duration));
      const visible = messageOffsets.current.filter((t) => t <= clamped).length;
      const opts = optionsRef.current;
      const activeAnns = opts?.annotationSeqs.filter((seq) => {
        const idx = opts.messages.findIndex((m) => m.seq === seq);
        return idx >= 0 && idx < visible;
      }) ?? [];

      return {
        ...prev,
        currentTime: clamped,
        visibleMessages: visible,
        activeAnnotationSeqs: activeAnns,
      };
    });
  }, []);

  const setSpeed = useCallback((speed: number) => {
    setState((s) => ({ ...s, speed }));
  }, []);

  useEffect(() => {
    return () => cancelAnimationFrame(animRef.current);
  }, []);

  return { state, play, pause, seek, setSpeed };
}
