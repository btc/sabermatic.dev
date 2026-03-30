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

  // Playback
  const audioChunksRef = useRef<string[]>([]);
  const currentAudioRef = useRef<HTMLAudioElement | null>(null);

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
    recorder.start();
    setIsRecording(true);
  }, []);

  const stopRecording = useCallback(async (): Promise<string> => {
    // Stop waveform animation
    cancelAnimationFrame(animFrameRef.current);
    setAnalyserData(null);

    return new Promise((resolve) => {
      const recorder = mediaRecorderRef.current;
      if (!recorder || recorder.state === "inactive") {
        setIsRecording(false);
        resolve("");
        return;
      }

      recorder.onstop = async () => {
        // Do NOT stop stream tracks — the stream persists between recordings
        const blob = new Blob(chunksRef.current, { type: "audio/webm" });
        const b64 = await blobToBase64(blob);
        setIsRecording(false);
        resolve(b64);
      };

      recorder.stop();
    });
  }, []);

  // ---- Playback ----

  const playAudioChunk = useCallback((base64: string) => {
    audioChunksRef.current.push(base64);
    if (audioChunksRef.current.length === 1) {
      recordEvent("audio.first_chunk_received", { chunk_b64_length: base64.length });
    }
  }, []);

  const flushPlayback = useCallback(() => {
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

    const blob = new Blob([combined], { type: "audio/mp3" });
    const url = URL.createObjectURL(blob);
    const audio = new Audio(url);
    currentAudioRef.current = audio;
    setIsPlaying(true);
    audio.onended = () => {
      playSpan.end({ result: "completed" });
      URL.revokeObjectURL(url);
      currentAudioRef.current = null;
      setIsPlaying(false);
    };
    audio.onerror = (e) => {
      const err = e instanceof ErrorEvent ? e.message : String(e);
      playSpan.end({ result: "error", error: err });
      URL.revokeObjectURL(url);
      currentAudioRef.current = null;
      setIsPlaying(false);
    };
    audio.play().then(() => {
      recordEvent("audio.play_started", { total_bytes: totalLength });
    }).catch((e) => {
      playSpan.end({ result: "rejected", error: String(e) });
      URL.revokeObjectURL(url);
      currentAudioRef.current = null;
      setIsPlaying(false);
    });
  }, []);

  const stopPlayback = useCallback(() => {
    audioChunksRef.current = [];
    if (currentAudioRef.current) {
      currentAudioRef.current.pause();
      currentAudioRef.current = null;
    }
    setIsPlaying(false);
  }, []);

  return {
    isRecording,
    isPlaying,
    micReady,
    analyserData,
    initMic,
    releaseMic,
    startRecording,
    stopRecording,
    playAudioChunk,
    flushPlayback,
    stopPlayback,
  };
}
