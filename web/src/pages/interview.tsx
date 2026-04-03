import { useState, useEffect, useRef, useCallback } from "react";
import { useParams, useNavigate } from "react-router-dom";
import {
  ArrowLeft,
  Mic,
  Send,
  Volume2,
  X,
} from "lucide-react";
import { useInterview } from "@/ws/hooks";
import { useTimer } from "@/hooks/use-timer";
import { useAudioRecorder } from "@/audio/hooks";
import { useAudioPlayer } from "@/audio/hooks";
import { useSession } from "@/api/queries";
import { ConnectionState } from "@/ws/connection";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogDescription,
  DialogFooter,
  DialogClose,
} from "@/components/ui/dialog";
import { cn } from "@/lib/utils";
import { WAITING_MESSAGES } from "@/lib/constants";

// ---------------------------------------------------------------------------
// Sub-components
// ---------------------------------------------------------------------------

function ReconnectingBanner() {
  return (
    <div className="bg-amber-100 dark:bg-amber-900/40 text-amber-800 dark:text-amber-200 text-center text-sm py-1.5 px-4">
      Reconnecting...
    </div>
  );
}

function TtsIndicator() {
  return (
    <span className="inline-flex items-center gap-1.5 text-xs text-muted-foreground">
      <Volume2 className="size-3.5" />
      <span className="size-1.5 rounded-full bg-primary animate-pulse" />
    </span>
  );
}

function InterviewerMessage({ content }: { content: string }) {
  return (
    <div className="flex justify-start">
      <div className="max-w-[80%] rounded-xl rounded-tl-sm bg-card border border-border px-4 py-3 text-sm leading-relaxed whitespace-pre-wrap">
        {content}
      </div>
    </div>
  );
}

function CandidateMessage({ content }: { content: string }) {
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] rounded-xl rounded-tr-sm bg-primary/10 dark:bg-primary/20 px-4 py-3 text-sm leading-relaxed whitespace-pre-wrap">
        {content}
      </div>
    </div>
  );
}

function StreamingMessage({ text }: { text: string }) {
  if (!text) return null;
  return (
    <div className="flex justify-start">
      <div className="max-w-[80%] rounded-xl rounded-tl-sm bg-card border border-border px-4 py-3 text-sm leading-relaxed whitespace-pre-wrap">
        {text}
        <span className="inline-block w-1 h-4 ml-0.5 bg-foreground/60 animate-pulse align-text-bottom" />
      </div>
    </div>
  );
}

function ProcessingIndicator() {
  return (
    <div className="flex justify-end">
      <div className="max-w-[80%] rounded-xl rounded-tr-sm bg-primary/10 dark:bg-primary/20 px-4 py-3">
        <div className="flex items-center gap-2">
          <div className="h-1.5 w-16 rounded-full bg-muted animate-pulse" />
          <span className="text-xs text-muted-foreground">Processing...</span>
        </div>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// WaitingView — shown after session ends while evaluation is processing
// ---------------------------------------------------------------------------

function formatElapsed(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  return `${m}:${s.toString().padStart(2, "0")}`;
}

interface WaitingViewProps {
  sessionId: string;
  questionTitle: string | undefined;
  messageCount: number;
  elapsed: number;
}

function WaitingView({ sessionId, questionTitle, messageCount, elapsed }: WaitingViewProps) {
  const navigate = useNavigate();
  const [messageIndex, setMessageIndex] = useState(0);

  // Cycle through status messages every 4 seconds
  useEffect(() => {
    const interval = setInterval(() => {
      setMessageIndex((i) => (i + 1) % WAITING_MESSAGES.length);
    }, 4000);
    return () => clearInterval(interval);
  }, []);

  // Poll for evaluation completion
  const { data: session } = useSession(sessionId, {
    refetchInterval: 3000,
  });

  // Navigate when evaluation is done
  useEffect(() => {
    if (session?.status === "reviewed" || session?.status === "evaluation_failed") {
      navigate(`/sessions/${sessionId}/overview`);
    }
  }, [session?.status, sessionId, navigate]);

  const candidateTurns = Math.ceil(messageCount / 2);

  return (
    <div className="flex flex-col items-center justify-center h-full gap-8 px-4">
      {/* Breathing pulse indicator */}
      <div className="size-3 rounded-full bg-primary animate-pulse" />

      {/* Cycling status message */}
      <p className="text-sm text-muted-foreground text-center max-w-xs">
        {WAITING_MESSAGES[messageIndex]}
      </p>

      {/* Session stats */}
      <div className="flex flex-col items-center gap-1.5 text-center">
        {questionTitle && (
          <p className="text-base font-medium">{questionTitle}</p>
        )}
        <p className="text-sm text-muted-foreground tabular-nums">
          {formatElapsed(elapsed)} &middot; {candidateTurns} {candidateTurns === 1 ? "response" : "responses"}
        </p>
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Main component
// ---------------------------------------------------------------------------

export default function Interview() {
  const { id: sessionId } = useParams<{ id: string }>();
  const navigate = useNavigate();

  // Hooks
  const {
    messages,
    streamingText,
    state,
    connectionState,
    sessionInfo,
    sendText,
    sendAudio,
    endSession,
    cancelTts,
    setRawMessageHandler,
  } = useInterview(sessionId!);

  const { data: session } = useSession(sessionId!);
  const audioRecorder = useAudioRecorder();
  const audioPlayer = useAudioPlayer();

  const { display: timerDisplay, phase: timerPhase, elapsed } = useTimer(
    session?.started_at ?? null,
    sessionInfo?.duration ?? 0,
  );

  // Local state
  const [textInput, setTextInput] = useState("");
  const [inputFocused, setInputFocused] = useState(false);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [endDialogOpen, setEndDialogOpen] = useState(false);
  const [audioContextInitialized, setAudioContextInitialized] = useState(false);

  // Refs
  const chatEndRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);

  // ------ Audio player wiring ------
  useEffect(() => {
    setRawMessageHandler((msg) => {
      if (msg.type === "tts_chunk") {
        audioPlayer.enqueue(msg.data, msg.seq);
      } else if (msg.type === "tts_done") {
        audioPlayer.done();
      }
    });
  }, [setRawMessageHandler, audioPlayer, audioPlayer.enqueue, audioPlayer.done]);

  // ------ AudioContext init on first interaction ------
  const ensureAudioContext = useCallback(() => {
    if (!audioContextInitialized) {
      audioPlayer.initContext();
      setAudioContextInitialized(true);
    }
  }, [audioContextInitialized, audioPlayer]);

  // ------ Auto-scroll ------
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, streamingText, state]);

  // ------ Cancel TTS when user starts responding ------
  const stopTts = useCallback(() => {
    cancelTts();
    audioPlayer.cancel();
  }, [cancelTts, audioPlayer]);

  // ------ Spacebar push-to-talk ------
  const { isRecording: recIsRecording, start: recStart, stop: recStop } = audioRecorder;
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.code === "Space" && !inputFocused && !recIsRecording) {
        e.preventDefault();
        stopTts();
        ensureAudioContext();
        recStart();
      }
    };
    const handleKeyUp = (e: KeyboardEvent) => {
      if (e.code === "Space" && !inputFocused && recIsRecording) {
        e.preventDefault();
        recStop();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    document.addEventListener("keyup", handleKeyUp);
    return () => {
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("keyup", handleKeyUp);
    };
  }, [inputFocused, recIsRecording, recStart, recStop, ensureAudioContext, stopTts]);

  // ------ Escape to blur input ------
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.code === "Escape" && inputFocused) {
        inputRef.current?.blur();
      }
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [inputFocused]);

  // ------ Send handlers ------
  const handleSendText = useCallback(() => {
    const trimmed = textInput.trim();
    if (!trimmed) return;
    stopTts();
    ensureAudioContext();
    sendText(trimmed);
    setTextInput("");
  }, [textInput, sendText, ensureAudioContext, stopTts]);

  const handleSendAudio = useCallback(async () => {
    ensureAudioContext();
    const audioBase64 = await audioRecorder.submit();
    if (audioBase64) {
      sendAudio(audioBase64);
    }
  }, [audioRecorder, sendAudio, ensureAudioContext]);

  const handleSend = useCallback(() => {
    if (textInput.trim()) {
      handleSendText();
    } else if (audioRecorder.segmentCount > 0) {
      handleSendAudio();
    }
  }, [textInput, audioRecorder.segmentCount, handleSendText, handleSendAudio]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  // ------ Derived state ------
  const isStreaming = state === "streaming";
  const isProcessing = state === "transcribing" || state === "processing";
  const isEnded = state === "ended";
  const inputDisabled = isStreaming || isEnded;
  const canSend = (textInput.trim().length > 0 || audioRecorder.segmentCount > 0) && !inputDisabled;

  // ------ Timer color ------
  const timerColor =
    timerPhase === "overtime"
      ? "text-destructive"
      : timerPhase === "warning"
        ? "text-amber-500"
        : "text-foreground";

  // ------ Post-interview waiting state ------
  if (isEnded) {
    return (
      <WaitingView
        sessionId={sessionId!}
        questionTitle={sessionInfo?.question.title}
        messageCount={messages.length}
        elapsed={elapsed}
      />
    );
  }

  return (
    <div className="flex flex-col h-full">
      {/* Reconnecting banner */}
      {connectionState === ConnectionState.Reconnecting && <ReconnectingBanner />}

      {/* ---- Header strip ---- */}
      <header className="flex items-center justify-between px-4 h-12 border-b border-border shrink-0">
        {/* Cancel */}
        <Button
          variant="ghost"
          size="sm"
          onClick={() => setCancelDialogOpen(true)}
        >
          <ArrowLeft className="size-4 mr-1" />
          Cancel
        </Button>

        {/* Timer + TTS indicator */}
        <div className="flex items-center gap-2">
          {audioPlayer.isPlaying && <TtsIndicator />}
          <span className={cn("text-sm font-mono tabular-nums", timerColor)}>
            {timerDisplay}
          </span>
        </div>

        {/* End Session */}
        <Button
          variant="default"
          size="sm"
          disabled={isEnded}
          onClick={() => setEndDialogOpen(true)}
        >
          End Session
        </Button>
      </header>

      {/* ---- Chat area ---- */}
      <div
        className="flex-1 overflow-y-auto"
        onClick={ensureAudioContext}
      >
        <div className="flex flex-col justify-end min-h-full px-4 py-6 max-w-2xl mx-auto">
          <div className="space-y-4">
            {messages.map((msg) =>
              msg.role === "interviewer" ? (
                <InterviewerMessage key={msg.id} content={msg.content} />
              ) : (
                <CandidateMessage key={msg.id} content={msg.content} />
              ),
            )}

            {/* Streaming text */}
            <StreamingMessage text={streamingText} />

            {/* Processing shimmer */}
            {isProcessing && <ProcessingIndicator />}
          </div>
          <div ref={chatEndRef} />
        </div>
      </div>

      {/* ---- Input area ---- */}
      <div className="border-t border-border p-4 shrink-0">
        <div className="max-w-2xl mx-auto flex items-center gap-2">
          {/* Microphone */}
          <Button
            variant="ghost"
            size="icon"
            disabled={inputDisabled}
            className={cn(
              audioRecorder.isRecording && "ring-2 ring-red-500 animate-pulse",
            )}
            onClick={() => {
              ensureAudioContext();
              if (audioRecorder.isRecording) {
                audioRecorder.stop();
              } else {
                stopTts();
                audioRecorder.start();
              }
            }}
          >
            <Mic className={cn("size-4", audioRecorder.isRecording && "text-red-500")} />
          </Button>

          {/* Segment indicator + discard */}
          {audioRecorder.segmentCount > 0 && !audioRecorder.isRecording && (
            <div className="flex items-center gap-1.5">
              <span className="text-xs text-muted-foreground tabular-nums">
                {audioRecorder.segmentCount} segment{audioRecorder.segmentCount !== 1 ? "s" : ""}
              </span>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={audioRecorder.discard}
              >
                <X className="size-3" />
              </Button>
            </div>
          )}

          {/* Text input */}
          <Input
            ref={inputRef}
            value={textInput}
            onChange={(e) => setTextInput(e.target.value)}
            onKeyDown={handleKeyDown}
            onFocus={() => setInputFocused(true)}
            onBlur={() => setInputFocused(false)}
            placeholder="Type a response..."
            disabled={inputDisabled}
            className="flex-1"
          />

          {/* Send */}
          <Button
            variant="default"
            size="icon"
            disabled={!canSend}
            onClick={handleSend}
          >
            <Send className="size-4" />
          </Button>
        </div>
      </div>

      {/* ---- Cancel dialog ---- */}
      <Dialog open={cancelDialogOpen} onOpenChange={setCancelDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Leave this session?</DialogTitle>
            <DialogDescription>
              You can return to it later.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose render={<Button variant="outline" />}>
              Stay
            </DialogClose>
            <Button
              variant="ghost"
              onClick={() => {
                setCancelDialogOpen(false);
                navigate("/");
              }}
            >
              Leave
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      {/* ---- End session dialog ---- */}
      <Dialog open={endDialogOpen} onOpenChange={setEndDialogOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>End this session?</DialogTitle>
            <DialogDescription>
              Your responses will be evaluated.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <DialogClose render={<Button variant="outline" />}>
              Continue
            </DialogClose>
            <Button
              onClick={() => {
                setEndDialogOpen(false);
                endSession();
              }}
            >
              End Session
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
