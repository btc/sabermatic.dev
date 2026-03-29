import { useCallback, useRef, useState } from "react";

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
  const [isPreparing, setIsPreparing] = useState(false);
  const [isPlaying, setIsPlaying] = useState(false);
  const [analyserData, setAnalyserData] = useState<Uint8Array | null>(null);

  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const analyserRef = useRef<AnalyserNode | null>(null);
  const animFrameRef = useRef<number>(0);

  // Playback: accumulate base64 chunks, play complete audio on flush
  const audioChunksRef = useRef<string[]>([]);
  const currentAudioRef = useRef<HTMLAudioElement | null>(null);

  // ---- Recording ----

  const startRecording = useCallback(async () => {
    setIsPreparing(true);
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true });

    // Set up analyser for waveform visualisation
    const audioCtx = new AudioContext();
    const source = audioCtx.createMediaStreamSource(stream);
    const analyser = audioCtx.createAnalyser();
    analyser.fftSize = 256;
    source.connect(analyser);
    analyserRef.current = analyser;

    const dataArray = new Uint8Array(analyser.frequencyBinCount);
    const tick = () => {
      analyser.getByteTimeDomainData(dataArray);
      setAnalyserData(new Uint8Array(dataArray));
      animFrameRef.current = requestAnimationFrame(tick);
    };
    tick();

    // MediaRecorder
    const recorder = new MediaRecorder(stream, { mimeType: "audio/webm" });
    chunksRef.current = [];
    recorder.ondataavailable = (e) => {
      if (e.data.size > 0) chunksRef.current.push(e.data);
    };
    mediaRecorderRef.current = recorder;
    recorder.start();
    setIsPreparing(false);
    setIsRecording(true);
  }, []);

  const stopRecording = useCallback(async (): Promise<string> => {
    return new Promise((resolve) => {
      const recorder = mediaRecorderRef.current;
      if (!recorder || recorder.state === "inactive") {
        resolve("");
        return;
      }

      recorder.onstop = async () => {
        // Stop analyser animation
        cancelAnimationFrame(animFrameRef.current);
        setAnalyserData(null);

        // Stop all tracks to release mic
        recorder.stream.getTracks().forEach((t) => t.stop());

        const blob = new Blob(chunksRef.current, { type: "audio/webm" });
        const b64 = await blobToBase64(blob);
        setIsRecording(false);
        resolve(b64);
      };

      recorder.stop();
    });
  }, []);

  // ---- Playback ----
  // TTS sends many small MP3 fragments. Individual fragments are NOT valid
  // standalone MP3 files. We accumulate all chunks, then play the complete
  // audio when flushPlayback() is called (on "interviewer_done").

  const playAudioChunk = useCallback((base64: string) => {
    audioChunksRef.current.push(base64);
  }, []);

  const flushPlayback = useCallback(() => {
    const chunks = audioChunksRef.current;
    audioChunksRef.current = [];
    if (chunks.length === 0) return;

    // Decode all base64 chunks and concatenate into a single binary blob
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

    const blob = new Blob([combined], { type: "audio/mp3" });
    const url = URL.createObjectURL(blob);
    const audio = new Audio(url);
    currentAudioRef.current = audio;
    setIsPlaying(true);
    audio.onended = () => {
      URL.revokeObjectURL(url);
      currentAudioRef.current = null;
      setIsPlaying(false);
    };
    audio.onerror = (e) => {
      console.error("[drill] Audio playback error:", e);
      URL.revokeObjectURL(url);
      currentAudioRef.current = null;
      setIsPlaying(false);
    };
    audio.play().catch((e) => {
      console.error("[drill] Audio play() rejected:", e);
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
    isPreparing,
    isPlaying,
    analyserData,
    startRecording,
    stopRecording,
    playAudioChunk,
    flushPlayback,
    stopPlayback,
  };
}
