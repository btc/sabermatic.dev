import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampDate } from "@bufbuild/protobuf/wkt";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { Message } from "@/pb/drill/v1/session_pb";

export interface ReplayState {
  isPlaying: boolean;
  currentTime: number;
  duration: number;
  speed: number;
  visibleMessages: number;
  activeAnnotationSeqs: number[];
}

export interface ReplayOptions {
  messages: Message[];
  sessionStartedAt: Timestamp;
  sessionEndedAt: Timestamp | undefined;
  annotationSeqs: number[];
}

interface DerivedTimings {
  duration: number;
  offsets: number[];
  seqToIndex: Map<number, number>;
}

function deriveTimings(options: ReplayOptions): DerivedTimings {
  const start = timestampDate(options.sessionStartedAt).getTime();
  const lastMessage = options.messages.at(-1);
  const lastMsg = lastMessage?.createTime
    ? timestampDate(lastMessage.createTime).getTime()
    : start;
  const end = options.sessionEndedAt
    ? timestampDate(options.sessionEndedAt).getTime()
    : lastMsg + 30 * 1000;

  const offsets = options.messages.map(
    (m) => m.createTime ? (timestampDate(m.createTime).getTime() - start) / 1000 : 0,
  );

  const map = new Map<number, number>();
  options.messages.forEach((m, i) => map.set(m.seq, i));

  return { duration: (end - start) / 1000, offsets, seqToIndex: map };
}

const EMPTY_TIMINGS: DerivedTimings = {
  duration: 0,
  offsets: [],
  seqToIndex: new Map(),
};

export function useReplayEngine(options: ReplayOptions | null) {
  const timings = useMemo(
    () => (options ? deriveTimings(options) : EMPTY_TIMINGS),
    [options],
  );

  const [state, setState] = useState<ReplayState>({
    isPlaying: false,
    currentTime: 0,
    duration: 0,
    speed: 1,
    visibleMessages: 0,
    activeAnnotationSeqs: [],
  });

  // Keep duration in sync with timings (derived, not from effect setState)
  const combinedState: ReplayState = useMemo(
    () => ({ ...state, duration: timings.duration }),
    [state, timings.duration],
  );

  const animRef = useRef<number>(0);
  const lastTickRef = useRef<number>(0);
  const playingRef = useRef(false);

  // Latest-ref pattern: runs after every render to keep refs in sync.
  // Do not add a dependency array — the tick callback must always
  // read current options/timings without stale closures.
  const optionsRef = useRef(options);
  useEffect(() => {
    optionsRef.current = options;
  });

  const timingsRef = useRef(timings);
  useEffect(() => {
    timingsRef.current = timings;
  });

  function computeActiveAnnotations(visible: number): number[] {
    const opts = optionsRef.current;
    if (!opts) return [];
    const map = timingsRef.current.seqToIndex;
    return opts.annotationSeqs.filter((seq) => {
      const idx = map.get(seq);
      return idx !== undefined && idx < visible;
    });
  }

  const tickRef = useRef<() => void>(() => {});

  useEffect(() => {
    tickRef.current = () => {
      if (!playingRef.current) return;

      const now = performance.now();
      const dt = (now - lastTickRef.current) / 1000;
      lastTickRef.current = now;
      const { duration, offsets } = timingsRef.current;

      setState((prev) => {
        const newTime = prev.currentTime + dt * prev.speed;
        if (newTime >= duration) {
          playingRef.current = false;
          return { ...prev, isPlaying: false, currentTime: duration };
        }

        const visible = offsets.filter((t) => t <= newTime).length;
        const activeAnns = computeActiveAnnotations(visible);

        return {
          ...prev,
          currentTime: newTime,
          visibleMessages: visible,
          activeAnnotationSeqs: activeAnns,
        };
      });

      if (playingRef.current) {
        animRef.current = requestAnimationFrame(() => tickRef.current());
      }
    };
  });

  const play = useCallback(() => {
    // Cancel any existing loop to prevent stacking
    cancelAnimationFrame(animRef.current);
    playingRef.current = true;
    lastTickRef.current = performance.now();
    setState((s) => ({ ...s, isPlaying: true }));
    animRef.current = requestAnimationFrame(() => tickRef.current());
  }, []);

  const pause = useCallback(() => {
    playingRef.current = false;
    cancelAnimationFrame(animRef.current);
    setState((s) => ({ ...s, isPlaying: false }));
  }, []);

  const seek = useCallback((time: number) => {
    const { duration, offsets } = timingsRef.current;
    setState((prev) => {
      const clamped = Math.max(0, Math.min(time, duration));
      const visible = offsets.filter((t) => t <= clamped).length;
      const activeAnns = computeActiveAnnotations(visible);

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
    return () => {
      playingRef.current = false;
      cancelAnimationFrame(animRef.current);
    };
  }, []);

  return { state: combinedState, play, pause, seek, setSpeed };
}
