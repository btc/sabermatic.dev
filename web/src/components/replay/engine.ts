import { useState, useRef, useCallback, useEffect } from "react";
import type { Message } from "@/pb/drill/v1/session_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { timestampDate } from "@bufbuild/protobuf/wkt";

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
  const playingRef = useRef(false);

  const optionsRef = useRef(options);
  optionsRef.current = options;

  const messageOffsets = useRef<number[]>([]);
  // seq → index map for O(1) lookups in tick/seek (avoids O(A*M) per frame)
  const seqToIndex = useRef<Map<number, number>>(new Map());

  useEffect(() => {
    if (!options) return;
    const start = timestampDate(options.sessionStartedAt).getTime();
    // Use ended_at if available; otherwise fall back to last message + 30s buffer
    const lastMsg = options.messages.length > 0
      ? timestampDate(options.messages[options.messages.length - 1].createTime!).getTime()
      : start;
    const end = options.sessionEndedAt
      ? timestampDate(options.sessionEndedAt).getTime()
      : lastMsg + 30 * 1000;

    messageOffsets.current = options.messages.map(
      (m) => (timestampDate(m.createTime!).getTime() - start) / 1000,
    );

    const map = new Map<number, number>();
    options.messages.forEach((m, i) => map.set(m.seq, i));
    seqToIndex.current = map;

    setState((s) => ({ ...s, duration: (end - start) / 1000 }));
  }, [options]);

  function computeActiveAnnotations(visible: number): number[] {
    const opts = optionsRef.current;
    if (!opts) return [];
    const map = seqToIndex.current;
    return opts.annotationSeqs.filter((seq) => {
      const idx = map.get(seq);
      return idx !== undefined && idx < visible;
    });
  }

  const tick = useCallback(() => {
    if (!playingRef.current) return;

    const now = performance.now();
    const dt = (now - lastTickRef.current) / 1000;
    lastTickRef.current = now;

    setState((prev) => {
      const newTime = prev.currentTime + dt * prev.speed;
      if (newTime >= prev.duration) {
        playingRef.current = false;
        return { ...prev, isPlaying: false, currentTime: prev.duration };
      }

      const visible = messageOffsets.current.filter((t) => t <= newTime).length;
      const activeAnns = computeActiveAnnotations(visible);

      return {
        ...prev,
        currentTime: newTime,
        visibleMessages: visible,
        activeAnnotationSeqs: activeAnns,
      };
    });

    if (playingRef.current) {
      animRef.current = requestAnimationFrame(tick);
    }
  }, []);

  const play = useCallback(() => {
    // Cancel any existing loop to prevent stacking
    cancelAnimationFrame(animRef.current);
    playingRef.current = true;
    lastTickRef.current = performance.now();
    setState((s) => ({ ...s, isPlaying: true }));
    animRef.current = requestAnimationFrame(tick);
  }, [tick]);

  const pause = useCallback(() => {
    playingRef.current = false;
    cancelAnimationFrame(animRef.current);
    setState((s) => ({ ...s, isPlaying: false }));
  }, []);

  const seek = useCallback((time: number) => {
    setState((prev) => {
      const clamped = Math.max(0, Math.min(time, prev.duration));
      const visible = messageOffsets.current.filter((t) => t <= clamped).length;
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

  return { state, play, pause, seek, setSpeed };
}
