import { useState, useEffect, useRef, useCallback } from "react";
import { useParams, useNavigate, Navigate } from "react-router-dom";
import {
  ArrowLeft,
  Volume2,
} from "lucide-react";
import { useInterview } from "@/ws/hooks";
import { useTimer } from "@/hooks/use-timer";
import { useAudioRecorder } from "@/audio/hooks";
import { useAudioPlayer } from "@/audio/hooks";
import { useQuery } from "@connectrpc/connect-query";
import { getSession } from "@/pb/drill/v1/session-SessionService_connectquery";
import { SessionStatus } from "@/pb/drill/v1/session_pb";
import { ConnectionState } from "@/ws/connection";
import { Button } from "@/components/ui/button";
import { RecordingInput } from "@/components/recording-input";
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
import { toast } from "sonner";

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
  const { data: sessionResp } = useQuery(getSession, { id: sessionId }, {
    refetchInterval: 3000,
  });

  // Navigate when evaluation is done
  useEffect(() => {
    const status = sessionResp?.session?.status;
    if (status === SessionStatus.REVIEWED || status === SessionStatus.EVALUATION_FAILED) {
      navigate(`/sessions/${sessionId}/overview`);
    }
  }, [sessionResp?.session?.status, sessionId, navigate]);

  const candidateTurns = Math.floor(messageCount / 2);

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
  if (!sessionId) return <Navigate to="/" replace />;
  return <InterviewInner sessionId={sessionId} />;
}

function InterviewInner({ sessionId }: { sessionId: string }) {
  // Hooks
  const {
    messages,
    streamingText,
    state,
    connectionState,
    sessionInfo,
    lastError,
    sendText,
    sendAudio,
    endSession,
    cancelSession,
    cancelTts,
    setRawMessageHandler,
  } = useInterview(sessionId);

  const navigate = useNavigate();
  const { data: sessionResp } = useQuery(getSession, { id: sessionId });
  const session = sessionResp?.session;
  const audioRecorder = useAudioRecorder();
  const audioPlayer = useAudioPlayer();

  const { display: timerDisplay, phase: timerPhase, elapsed } = useTimer(
    session?.startTime ? new Date(Number(session.startTime.seconds) * 1000).toISOString() : null,
    sessionInfo?.duration ?? 0,
  );

  // Navigate home when session is cancelled
  useEffect(() => {
    if (state === "cancelled") {
      navigate("/");
    }
  }, [state, navigate]);

  // Show server errors as toasts
  useEffect(() => {
    if (lastError) {
      toast.error(lastError);
    }
  }, [lastError]);

  // Local state
  const [textInput, setTextInput] = useState("");
  const [inputFocused, setInputFocused] = useState(false);
  const [cancelDialogOpen, setCancelDialogOpen] = useState(false);
  const [endDialogOpen, setEndDialogOpen] = useState(false);
  // Refs
  const chatEndRef = useRef<HTMLDivElement>(null);

  // ------ Audio player wiring ------
  useEffect(() => {
    setRawMessageHandler((msg) => {
      if (msg.type === "tts_chunk") {
        audioPlayer.enqueue(msg.data, msg.seq);
      } else if (msg.type === "tts_done") {
        audioPlayer.done();
      }
    });
  }, [setRawMessageHandler, audioPlayer]);

  // ------ Init TTS AudioContext on user gesture (idempotent) ------
  const initTtsContext = useCallback(() => {
    audioPlayer.initContext();
  }, [audioPlayer]);

  // ------ Derived state (hoisted above effects that reference it) ------
  const isStreaming = state === "streaming";
  const isProcessing = state === "transcribing" || state === "processing";
  const isEnded = state === "ended" || state === "cancelled";
  const inputDisabled = isStreaming || isEnded || connectionState === ConnectionState.Reconnecting;

  // ------ Auto-scroll ------
  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages, streamingText, state]);

  // ------ Cancel TTS when user starts responding ------
  const stopTts = useCallback(() => {
    cancelTts();
    audioPlayer.cancel();
  }, [cancelTts, audioPlayer]);

  // ------ Stable refs from audioRecorder ------
  const { start: recStart, stop: recStop } = audioRecorder;

  // ------ Send handlers ------
  const handleSendText = useCallback(() => {
    const trimmed = textInput.trim();
    if (!trimmed) return;
    stopTts();
    initTtsContext();
    sendText(trimmed);
    setTextInput("");
  }, [textInput, sendText, initTtsContext, stopTts]);

  const handleSendAudio = useCallback(async () => {
    if (audioRecorder.segmentCount === 0) return;
    stopTts();
    initTtsContext();
    const audioBase64 = await audioRecorder.submit();
    if (audioBase64) {
      sendAudio(audioBase64);
    }
  }, [audioRecorder, sendAudio, initTtsContext, stopTts]);

  const handleSend = useCallback(() => {
    if (textInput.trim()) {
      handleSendText();
    } else if (audioRecorder.segmentCount > 0) {
      handleSendAudio();
    }
  }, [textInput, audioRecorder.segmentCount, handleSendText, handleSendAudio]);

  // ------ Recording callbacks ------
  const handleStartRecording = useCallback(async () => {
    stopTts();
    initTtsContext();
    await recStart();
  }, [stopTts, initTtsContext, recStart]);

  const handleStopRecording = useCallback(async () => {
    await recStop();
  }, [recStop]);

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
        sessionId={sessionId}
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

        {/* Question title */}
        <span className="text-sm font-medium truncate max-w-[40%] text-center">
          {sessionInfo?.question.title}
        </span>

        {/* Timer + End Session */}
        <div className="flex items-center gap-2">
          {audioPlayer.isPlaying && <TtsIndicator />}
          <span className={cn("text-sm font-mono tabular-nums", timerColor)}>
            {timerDisplay}
          </span>
          <Button
            variant="default"
            size="sm"
            disabled={isEnded}
            onClick={() => setEndDialogOpen(true)}
          >
            End Session
          </Button>
        </div>
      </header>

      {/* ---- Chat area ---- */}
      <div
        className="flex-1 overflow-y-auto"
        onClick={initTtsContext}
      >
        <div className="flex flex-col justify-center min-h-full px-4 py-6 max-w-2xl mx-auto">
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
      <RecordingInput
        isRecording={audioRecorder.isRecording}
        segmentCount={audioRecorder.segmentCount}
        pendingDuration={audioRecorder.pendingDuration}
        analyserNode={audioRecorder.analyserNode}
        textInput={textInput}
        onTextChange={setTextInput}
        inputFocused={inputFocused}
        onFocusChange={setInputFocused}
        onSend={handleSend}
        onStartRecording={handleStartRecording}
        onStopRecording={handleStopRecording}
        onDiscard={audioRecorder.discard}
        disabled={inputDisabled}
        dialogOpen={cancelDialogOpen || endDialogOpen}
      />

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
                cancelSession();
              }}
            >
              Cancel Session
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
