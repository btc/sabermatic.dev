import { Mic, Send, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Kbd } from "@/components/ui/kbd";
import { Waveform } from "@/components/waveform";
import { cn } from "@/lib/utils";

interface RecordingInputProps {
  isRecording: boolean;
  segmentCount: number;
  pendingDuration: number;
  analyserNode: AnalyserNode | null;

  textInput: string;
  onTextChange: (value: string) => void;
  inputFocused: boolean;
  onFocusChange: (focused: boolean) => void;

  onSend: () => void;
  onStartRecording: () => void;
  onStopRecording: () => void;
  onDiscard: () => void;

  disabled: boolean;
  dialogOpen: boolean;
}

function formatRecordingTime(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

function formatPendingDuration(seconds: number): string {
  return `${seconds.toFixed(1)}s`;
}

export function RecordingInput({
  isRecording,
  segmentCount,
  pendingDuration,
  analyserNode,
  textInput,
  onTextChange,
  inputFocused,
  onFocusChange,
  onSend,
  onStartRecording,
  onStopRecording,
  onDiscard,
  disabled,
  dialogOpen,
}: RecordingInputProps) {
  const inputRef = useRef<HTMLInputElement>(null);
  const spaceHeldRef = useRef(false);
  const startedViaSpaceRef = useRef(false);
  const autoStoppedRef = useRef(false);

  // Auto-stop recording if Space was released during async start (e.g., mic permission prompt).
  // Only applies to Space-initiated recording — mic button clicks are toggle-based.
  useEffect(() => {
    if (isRecording && startedViaSpaceRef.current && !spaceHeldRef.current) {
      autoStoppedRef.current = true;
      onStopRecording();
    }
  }, [isRecording, onStopRecording]);

  // -- Waveform animation state --
  const [waveformData, setWaveformData] = useState<Uint8Array>(new Uint8Array(24).fill(128));
  const [frozenWaveform, setFrozenWaveform] = useState<Uint8Array>(new Uint8Array(24).fill(128));
  const [elapsedTime, setElapsedTime] = useState(0);
  const animFrameRef = useRef(0);
  const latestWaveformRef = useRef<Uint8Array>(new Uint8Array(24).fill(128));

  // Drive waveform + elapsed timer via requestAnimationFrame during recording
  useEffect(() => {
    if (!isRecording || !analyserNode) {
      cancelAnimationFrame(animFrameRef.current);
      return;
    }
    const startTime = performance.now();
    const baseOffset = pendingDuration; // include prior segments in elapsed
    const dataArray = new Uint8Array(analyserNode.frequencyBinCount);
    const tick = () => {
      analyserNode.getByteTimeDomainData(dataArray);
      const snapshot = new Uint8Array(dataArray);
      latestWaveformRef.current = snapshot;
      setWaveformData(snapshot);
      setElapsedTime(baseOffset + (performance.now() - startTime) / 1000);
      animFrameRef.current = requestAnimationFrame(tick);
    };
    tick();
    return () => cancelAnimationFrame(animFrameRef.current);
  }, [isRecording, analyserNode, pendingDuration]);

  // Freeze waveform snapshot when recording stops (use ref to avoid stale data).
  // If auto-stopped (permission dialog case), discard the phantom segment.
  const prevRecording = useRef(false);
  useEffect(() => {
    if (prevRecording.current && !isRecording) {
      if (autoStoppedRef.current) {
        autoStoppedRef.current = false;
        onDiscard();
      } else {
        setFrozenWaveform(new Uint8Array(latestWaveformRef.current));
      }
      startedViaSpaceRef.current = false;
    }
    prevRecording.current = isRecording;
  }, [isRecording, onDiscard]);

  // -- Consolidated keyboard handlers --
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (inputFocused) {
        if (e.code === "Escape") inputRef.current?.blur();
        return;
      }
      if (disabled) return;
      if (e.code === "Space" && !e.repeat && !isRecording && !dialogOpen) {
        e.preventDefault();
        spaceHeldRef.current = true;
        startedViaSpaceRef.current = true;
        onStartRecording();
      } else if (e.code === "Enter" && !dialogOpen) {
        e.preventDefault();
        onSend();
      } else if ((e.code === "Delete" || e.code === "Backspace") && !isRecording && segmentCount > 0) {
        e.preventDefault();
        onDiscard();
      }
    };
    const handleKeyUp = (e: KeyboardEvent) => {
      if (e.code === "Space") {
        spaceHeldRef.current = false;
        if (!inputFocused && isRecording) {
          e.preventDefault();
          onStopRecording();
        }
      }
    };
    // Reset spaceHeld on window blur — permission dialogs steal focus,
    // swallowing the keyup event and leaving spaceHeld stuck true.
    const handleBlur = () => { spaceHeldRef.current = false; };
    document.addEventListener("keydown", handleKeyDown);
    document.addEventListener("keyup", handleKeyUp);
    window.addEventListener("blur", handleBlur);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("keyup", handleKeyUp);
      window.removeEventListener("blur", handleBlur);
    };
  }, [inputFocused, disabled, isRecording, segmentCount, dialogOpen,
      onStartRecording, onStopRecording, onSend, onDiscard]);

  // -- Input keydown (Enter to submit text) --
  const handleInputKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      onSend();
    }
  };

  // -- Derived --
  const canSend = (textInput.trim().length > 0 || segmentCount > 0) && !disabled;
  const hasPendingAudio = segmentCount > 0 && !isRecording;

  // -- Hint text --
  let hint: React.ReactNode = null;
  if (disabled) {
    hint = null;
  } else if (isRecording) {
    hint = (
      <>Recording... release <Kbd>Space</Kbd> to stop</>
    );
  } else if (hasPendingAudio) {
    hint = (
      <>
        <Kbd>Enter</Kbd> submit · <Kbd>Space</Kbd> add more · <Kbd>Delete</Kbd> discard
      </>
    );
  } else if (!inputFocused) {
    hint = (
      <>Hold <Kbd>Space</Kbd> to talk</>
    );
  }

  return (
    <div className="border-t border-border p-4 shrink-0">
      <div className="max-w-2xl mx-auto flex items-center gap-2">
        {/* Mic button */}
        <Button
          variant="ghost"
          size="icon"
          aria-label={isRecording ? "Stop recording" : "Start recording"}
          disabled={disabled}
          className={cn(isRecording && "ring-2 ring-red-500")}
          onClick={() => {
            if (isRecording) {
              onStopRecording();
            } else {
              onStartRecording();
            }
          }}
        >
          <Mic className={cn("size-4", isRecording && "text-red-500")} />
        </Button>

        {/* Delete button — only when audio is buffered */}
        {hasPendingAudio && (
          <Button
            variant="ghost"
            size="icon"
            aria-label="Discard recording"
            onClick={onDiscard}
          >
            <X className="size-4" />
          </Button>
        )}

        {/* Main input area — switches between text input, active waveform, and passive waveform */}
        {isRecording ? (
          <div className="flex-1 h-9 rounded-lg bg-red-500/5 border border-red-500/30 flex items-center gap-2 px-3 overflow-hidden">
            <div className="size-2 rounded-full bg-red-500 shrink-0 animate-pulse" />
            <Waveform data={waveformData} variant="active" className="flex-1" />
            <span className="text-red-500 text-xs font-mono tabular-nums shrink-0">
              {formatRecordingTime(elapsedTime)}
            </span>
          </div>
        ) : hasPendingAudio ? (
          <div className="flex-1 h-9 rounded-lg bg-muted/30 border border-border flex items-center gap-2 px-3 overflow-hidden">
            <Waveform data={frozenWaveform} variant="passive" className="flex-1" />
            <span className="text-muted-foreground text-xs font-mono tabular-nums shrink-0">
              {formatPendingDuration(pendingDuration)}
            </span>
          </div>
        ) : (
          <Input
            ref={inputRef}
            value={textInput}
            onChange={(e) => onTextChange(e.target.value)}
            onKeyDown={handleInputKeyDown}
            onFocus={() => onFocusChange(true)}
            onBlur={() => onFocusChange(false)}
            placeholder="Type a response..."
            disabled={disabled}
            className="flex-1"
          />
        )}

        {/* Send button */}
        <Button
          variant="default"
          size="icon"
          aria-label="Send message"
          disabled={!canSend}
          onClick={onSend}
        >
          <Send className="size-4" />
        </Button>
      </div>

      {/* Contextual keyboard hint — always rendered to avoid layout shift */}
      <p className={cn("text-center text-[11px] text-muted-foreground mt-2", !hint && "invisible")}>
        {hint || "\u00A0"}
      </p>
    </div>
  );
}
