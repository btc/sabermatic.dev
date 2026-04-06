import { useRef, useCallback, useState, useEffect } from "react";
import { AudioRecorder } from "./recorder";
import { AudioPlayer } from "./player";

export function useAudioRecorder() {
  const recorderRef = useRef<AudioRecorder | null>(null);
  const [isRecording, setIsRecording] = useState(false);
  const [segmentCount, setSegmentCount] = useState(0);
  const [pendingDuration, setPendingDuration] = useState(0);
  const [analyserNode, setAnalyserNode] = useState<AnalyserNode | null>(null);

  useEffect(() => {
    recorderRef.current = new AudioRecorder();
    return () => recorderRef.current?.destroy();
  }, []);

  const start = useCallback(async () => {
    await recorderRef.current?.start();
    setIsRecording(true);
    if (!analyserNode && recorderRef.current?.analyserNode) {
      setAnalyserNode(recorderRef.current.analyserNode);
    }
  }, [analyserNode]);

  const stop = useCallback(async () => {
    await recorderRef.current?.stop();
    setIsRecording(false);
    setSegmentCount(recorderRef.current?.segmentCount ?? 0);
    setPendingDuration(recorderRef.current?.totalDuration ?? 0);
  }, []);

  const submit = useCallback(async () => {
    const data = await recorderRef.current?.submit();
    setSegmentCount(0);
    setPendingDuration(0);
    return data ?? "";
  }, []);

  const discard = useCallback(() => {
    recorderRef.current?.discard();
    setSegmentCount(0);
    setPendingDuration(0);
  }, []);

  return { isRecording, segmentCount, pendingDuration, analyserNode, start, stop, submit, discard };
}

export function useAudioPlayer() {
  const playerRef = useRef<AudioPlayer | null>(null);
  const [isPlaying, setIsPlaying] = useState(false);

  useEffect(() => {
    const player = new AudioPlayer();
    player.onComplete = () => setIsPlaying(false);
    playerRef.current = player;
    return () => {
      player.onComplete = null;
      player.destroy();
    };
  }, []);

  const initContext = useCallback(async () => {
    await playerRef.current?.initContext();
  }, []);

  const enqueue = useCallback((data: string, seq: number) => {
    setIsPlaying(true);
    playerRef.current?.enqueue(data, seq);
  }, []);

  const cancel = useCallback(() => {
    playerRef.current?.cancel();
    setIsPlaying(false);
  }, []);

  const done = useCallback(() => {
    playerRef.current?.done();
  }, []);

  return { isPlaying, initContext, enqueue, cancel, done };
}
