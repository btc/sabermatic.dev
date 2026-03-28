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
  const [analyserData, setAnalyserData] = useState<Uint8Array | null>(null);

  const mediaRecorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);
  const analyserRef = useRef<AnalyserNode | null>(null);
  const animFrameRef = useRef<number>(0);

  // Playback queue: sequential audio playback via standard Audio elements
  const audioQueueRef = useRef<string[]>([]);
  const isPlayingRef = useRef(false);
  const currentAudioRef = useRef<HTMLAudioElement | null>(null);

  // ---- Recording ----

  const startRecording = useCallback(async () => {
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

  // ---- Playback (queued sequential) ----

  const playNext = useCallback(() => {
    if (audioQueueRef.current.length === 0) {
      isPlayingRef.current = false;
      return;
    }
    isPlayingRef.current = true;
    const b64 = audioQueueRef.current.shift()!;
    const audio = new Audio(`data:audio/mp3;base64,${b64}`);
    currentAudioRef.current = audio;
    audio.onended = () => {
      currentAudioRef.current = null;
      playNext();
    };
    audio.onerror = () => {
      currentAudioRef.current = null;
      playNext();
    };
    audio.play().catch(() => {
      currentAudioRef.current = null;
      playNext();
    });
  }, []);

  const playAudioChunk = useCallback(
    (base64: string) => {
      audioQueueRef.current.push(base64);
      if (!isPlayingRef.current) {
        playNext();
      }
    },
    [playNext]
  );

  const stopPlayback = useCallback(() => {
    audioQueueRef.current = [];
    if (currentAudioRef.current) {
      currentAudioRef.current.pause();
      currentAudioRef.current = null;
    }
    isPlayingRef.current = false;
  }, []);

  return {
    isRecording,
    analyserData,
    startRecording,
    stopRecording,
    playAudioChunk,
    stopPlayback,
  };
}
