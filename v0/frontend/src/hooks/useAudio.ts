import { useCallback, useRef, useState } from "react";
import { recordEvent, startSpan } from "../tracer";

// ---- helpers ----

async function blobToBase64(blob: Blob): Promise<string> {
  return new Promise((resolve) => {
    const reader = new FileReader();
    reader.onloadend = () => resolve((reader.result as string).split(",")[1]);
    reader.readAsDataURL(blob);
  });
}

// ---- hook ----

export function useAudio() {
  const [isRecording, setIsRecording] = useState(false);
  const [isPlaying, setIsPlaying] = useState(false);
  const [micReady, setMicReady] = useState(false);
  const [analyserData, setAnalyserData] = useState<Uint8Array | null>(null);

  // Persistent mic stream — acquired once, reused for every recording
  const streamRef = useRef<MediaStream | null>(null);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const analyserRef = useRef<AnalyserNode | null>(null);
  const animFrameRef = useRef<number>(0);

  // Per-recording state
  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);

  // Multi-segment state
  const segmentsRef = useRef<Blob[]>([]);
  const [pendingSegments, setPendingSegments] = useState(0);
  const [pendingDuration, setPendingDuration] = useState(0);
  const recordingStartRef = useRef<number>(0);

  // Playback via AudioContext (survives autoplay policy)
  const playbackCtxRef = useRef<AudioContext | null>(null);
  const audioChunksRef = useRef<string[]>([]);
  const currentSourceRef = useRef<AudioBufferSourceNode | null>(null);

  // ---- Mic initialization (call once, e.g. on "Begin Interview") ----

  const initMic = useCallback(async () => {
    if (streamRef.current) return; // already initialized

    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
    streamRef.current = stream;

    const audioCtx = new AudioContext();
    const source = audioCtx.createMediaStreamSource(stream);
    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 256;
    source.connect(analyser);
    audioCtxRef.current = audioCtx;
    analyserRef.current = analyser;

    // Create a separate AudioContext for TTS playback.
    // Created during user gesture (click), so it's unlocked for autoplay.
    playbackCtxRef.current = new AudioContext();

    setMicReady(true);
  }, []);

  const releaseMic = useCallback(() => {
    if (streamRef.current) {
      streamRef.current.getTracks().forEach((t) => t.stop());
      streamRef.current = null;
    }
    if (audioCtxRef.current) {
      audioCtxRef.current.close();
      audioCtxRef.current = null;
    }
    if (playbackCtxRef.current) {
      playbackCtxRef.current.close();
      playbackCtxRef.current = null;
    }
    analyserRef.current = null;
    setMicReady(false);
  }, []);

  // ---- Recording (synchronous start — no getUserMedia call) ----

  const startRecording = useCallback(() => {
    const stream = streamRef.current;
    if (!stream) {
      console.error("[drill] startRecording called but mic not initialized");
      return;
    }

    // Start waveform animation
    const analyser = analyserRef.current;
    if (analyser) {
      const dataArray = new Uint8Array(analyser.frequencyBinCount);
      const tick = () => {
        analyser.getByteTimeDomainData(dataArray);
        setAnalyserData(new Uint8Array(dataArray));
        animFrameRef.current = requestAnimationFrame(tick);
      };
      tick();
    }

    // Create recorder on the persistent stream — this is synchronous
    const recorder = new MediaRecorder(stream, { mimeType: "audio/webm" });
    chunksRef.current = [];
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunksRef.current.push(e.data);
    };
    mediaRecorderRef.current = recorder;
    recordingStartRef.current = performance.now();
    recorder.start();
    setIsRecording(true);
  }, []);

  const stopRecording = useCallback(async (): Promise<void> => {
    // Stop waveform animation
    cancelAnimationFrame(animFrameRef.current);
    setAnalyserData(null);

    return new Promise((resolve) => {
      const recorder = mediaRecorderRef.current;
      if (!recorder || recorder.state === "inactive") {
        setIsRecording(false);
        resolve();
        return;
      }

      const elapsedSec = (performance.now() - recordingStartRef.current) / 1000;

      recorder.onstop = () => {
        // Do NOT stop stream tracks — the stream persists between recordings
        const blob = new Blob(chunksRef.current, { type: "audio/webm" });
        if (blob.size > 0) {
          segmentsRef.current = [...segmentsRef.current, blob];
          setPendingSegments(segmentsRef.current.length);
          setPendingDuration((prev) => prev + elapsedSec);
        }
        setIsRecording(false);
        resolve();
      };

      recorder.stop();
    });
  }, []);

  const submitRecording = useCallback(async (): Promise<string> => {
    if (segmentsRef.current.length === 0) return "";
    const combined = new Blob(segmentsRef.current, { type: "audio/webm" });
    segmentsRef.current = [];
    setPendingSegments(0);
    setPendingDuration(0);
    return blobToBase64(combined);
  }, []);

  const discardRecording = useCallback(() => {
    segmentsRef.current = [];
    setPendingSegments(0);
    setPendingDuration(0);
  }, []);

  // ---- Playback ----

  const playAudioChunk = useCallback((base64: string) => {
    audioChunksRef.current.push(base64);
    if (audioChunksRef.current.length === 1) {
      recordEvent("audio.first_chunk_received", { chunk_b64_length: base64.length });
    }
  }, []);

  const flushPlayback = useCallback(async () => {
    const chunks = audioChunksRef.current;
    audioChunksRef.current = [];
    if (chunks.length === 0) {
      recordEvent("audio.flush_empty", { reason: "no_chunks_accumulated" });
      return;
    }

    const binaryChunks = chunks.map((b64) => {
      const binary = atob(b64);
      const bytes = new Uint8Array(binary.length);
      for (let i = 0; i < binary.length; i++) {
        bytes[i] = binary.charCodeAt(i);
      }
      return bytes;
    });

    const totalLength = binaryChunks.reduce((sum, c) => sum + c.length, 0);
    const combined = new Uint8Array(totalLength);
    let offset = 0;
    for (const chunk of binaryChunks) {
      combined.set(chunk, offset);
      offset += chunk.length;
    }

    const playSpan = startSpan("audio.playback_attempt", {
      chunk_count: chunks.length,
      total_bytes: totalLength,
    });

    const ctx = playbackCtxRef.current;
    if (!ctx) {
      playSpan.end({ result: "error", error: "no_playback_context" });
      return;
    }

    // Resume context if suspended (browsers suspend after inactivity)
    if (ctx.state === "suspended") {
      await ctx.resume();
    }

    try {
      // decodeAudioData needs a complete audio file — our concatenated MP3 is one
      const audioBuffer = await ctx.decodeAudioData(combined.buffer.slice(0));
      const source = ctx.createBufferSource();
      source.buffer = audioBuffer;
      source.connect(ctx.destination);

      currentSourceRef.current = source;
      setIsPlaying(true);

      source.onended = () => {
        playSpan.end({ result: "completed", duration_sec: audioBuffer.duration });
        currentSourceRef.current = null;
        setIsPlaying(false);
      };

      source.start(0);
      recordEvent("audio.play_started", { total_bytes: totalLength, duration_sec: audioBuffer.duration });
    } catch (e) {
      playSpan.end({ result: "error", error: String(e) });
      setIsPlaying(false);
    }
  }, []);

  const stopPlayback = useCallback(() => {
    audioChunksRef.current = [];
    if (currentSourceRef.current) {
      try { currentSourceRef.current.stop(); } catch { /* already stopped */ }
      currentSourceRef.current = null;
    }
    setIsPlaying(false);
  }, []);

  return {
    isRecording,
    isPlaying,
    micReady,
    analyserData,
    pendingSegments,
    pendingDuration,
    initMic,
    releaseMic,
    startRecording,
    stopRecording,
    submitRecording,
    discardRecording,
    playAudioChunk,
    flushPlayback,
    stopPlayback,
  };
}
