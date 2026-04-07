import { useState, useEffect, useRef, useCallback } from "react";
import type { Span } from "@opentelemetry/api";
import { createClient, ConnectError } from "@connectrpc/connect";
import { InterviewService } from "@/pb/drill/v1/interview_pb";
import type {
  InterviewSessionInfo,
  TurnEvent,
} from "@/pb/drill/v1/interview_pb";
import { SessionStatus } from "@/pb/drill/v1/session_pb";
import { transport } from "@/api/transport";
import { createTurnSpan, closeTurnSpan } from "@/telemetry/trace";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface InterviewMessage {
  id: string;
  seq: number;
  role: "interviewer" | "candidate";
  content: string;
}

export type InterviewState =
  | "connecting"
  | "waiting"
  | "transcribing"
  | "processing"
  | "streaming"
  | "ended"
  | "cancelled";

type TtsChunkCallback = (data: Uint8Array, seq: number) => void;
type TtsDoneCallback = () => void;

// ---------------------------------------------------------------------------
// Client singleton (stateless — safe to share across hook instances)
// ---------------------------------------------------------------------------

const client = createClient(InterviewService, transport);

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

export function useInterview(sessionId: string) {
  // -- State --
  const [messages, setMessages] = useState<InterviewMessage[]>([]);
  const [streamingText, setStreamingText] = useState("");
  const [state, setState] = useState<InterviewState>("connecting");
  const [sessionInfo, setSessionInfo] = useState<InterviewSessionInfo | null>(null);
  const [lastError, setLastError] = useState<string | null>(null);

  // -- Refs --
  const streamingTextRef = useRef("");
  const turnSpanRef = useRef<Span | null>(null);
  const ttsChunkRef = useRef<TtsChunkCallback | null>(null);
  const ttsDoneRef = useRef<TtsDoneCallback | null>(null);
  // AbortController for the active SubmitTurn stream so we can cancel on unmount.
  const streamAbortRef = useRef<AbortController | null>(null);

  // -- TTS callback setters --
  const setOnTtsChunk = useCallback((cb: TtsChunkCallback) => {
    ttsChunkRef.current = cb;
  }, []);

  const setOnTtsDone = useCallback((cb: TtsDoneCallback) => {
    ttsDoneRef.current = cb;
  }, []);

  // -- Handle a single TurnEvent from the SubmitTurn stream --
  const handleTurnEvent = useCallback((event: TurnEvent) => {
    const e = event.event;
    switch (e.case) {
      case "transcriptionResult": {
        setState("processing");
        const text = e.value.text;
        setMessages((prev) => {
          const displaySeq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
          return [
            ...prev,
            { id: `local-${displaySeq}`, seq: displaySeq, role: "candidate", content: text },
          ];
        });
        break;
      }

      case "interviewerToken":
        streamingTextRef.current += e.value.token;
        setStreamingText(streamingTextRef.current);
        setState("streaming");
        break;

      case "interviewerDone": {
        const finalText = streamingTextRef.current;
        streamingTextRef.current = "";
        setStreamingText("");
        const messageId = e.value.messageId;
        setMessages((prev) => {
          const seq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
          return [
            ...prev,
            { id: messageId, seq, role: "interviewer", content: finalText },
          ];
        });
        setState("waiting");
        if (turnSpanRef.current) {
          closeTurnSpan(turnSpanRef.current);
          turnSpanRef.current = null;
        }
        break;
      }

      case "ttsChunk":
        ttsChunkRef.current?.(e.value.data, e.value.seq);
        break;

      case "ttsDone":
        ttsDoneRef.current?.();
        break;

      case "error": {
        const err = e.value;
        console.error(`Turn error: ${err.code} -- ${err.message}`);
        setLastError(err.message);
        setState("waiting");
        if (turnSpanRef.current) {
          closeTurnSpan(turnSpanRef.current, false);
          turnSpanRef.current = null;
        }
        break;
      }

      default:
        break;
    }
  }, []);

  // -- Consume a SubmitTurn server stream --
  const consumeStream = useCallback(
    async (
      stream: AsyncIterable<TurnEvent>,
    ) => {
      for await (const event of stream) {
        handleTurnEvent(event);
      }
    },
    [handleTurnEvent],
  );

  // -- Fetch session state with exponential backoff while GENERATING --
  // Returns true if the session is in an active (non-terminal) state with
  // no existing messages, meaning the opening turn should be fired.
  const fetchSessionState = useCallback(
    async (signal: AbortSignal, attempt = 0): Promise<boolean> => {
      const resp = await client.getSessionState({
        sessionId,
        knownMessageCount: 0,
      });

      // If the server is still generating the interviewer response, poll with backoff.
      if (resp.status === SessionStatus.GENERATING) {
        const delay = Math.min(300 * Math.pow(2, attempt), 2000);
        await new Promise((r) => setTimeout(r, delay));
        if (signal.aborted) return false;
        return fetchSessionState(signal, attempt + 1);
      }

      // Populate session info.
      if (resp.sessionInfo) {
        setSessionInfo(resp.sessionInfo);
      }

      // Populate messages from server state.
      const hasMessages = resp.messages.length > 0;
      if (hasMessages) {
        setMessages(
          resp.messages.map((m) => ({
            id: m.id,
            seq: m.seq,
            role: m.role as "interviewer" | "candidate",
            content: m.content,
          })),
        );
      }

      // Map server status to InterviewState.
      if (
        resp.status === SessionStatus.COMPLETED ||
        resp.status === SessionStatus.FAILED ||
        resp.status === SessionStatus.EVALUATING ||
        resp.status === SessionStatus.REVIEWED ||
        resp.status === SessionStatus.EVALUATION_FAILED
      ) {
        setState("ended");
        return false;
      }
      if (resp.status === SessionStatus.CANCELLED) {
        setState("cancelled");
        return false;
      }

      setState("waiting");
      // Only fire the opening turn for a fresh session with no messages.
      return !hasMessages;
    },
    [sessionId],
  );

  // -- Initialize: GetSessionState + opening SubmitTurn("") --
  useEffect(() => {
    const abort = new AbortController();
    streamAbortRef.current = abort;

    (async () => {
      let needsOpeningTurn = false;
      try {
        needsOpeningTurn = await fetchSessionState(abort.signal);
      } catch (err) {
        if (abort.signal.aborted) return;
        const msg = err instanceof ConnectError ? err.message : String(err);
        console.error("Failed to load session state:", msg);
        setLastError(msg);
        return;
      }

      if (!needsOpeningTurn || abort.signal.aborted) return;

      try {
        const { span, traceContext } = createTurnSpan("text");
        turnSpanRef.current = span;
        const stream = client.submitTurn(
          {
            sessionId,
            traceparent: traceContext.traceparent,
            input: { case: "textInput", value: { content: "" } },
          },
          { signal: abort.signal },
        );
        await consumeStream(stream);
      } catch (err) {
        if (abort.signal.aborted) return;
        const msg = err instanceof ConnectError ? err.message : String(err);
        // An error from the opening turn is not necessarily fatal — the session
        // may already be in progress. Log but don't block.
        console.error("Opening turn error:", msg);
      }
    })();

    return () => {
      abort.abort();
    };
  }, [sessionId, fetchSessionState, consumeStream]);

  // -- sendText --
  const sendText = useCallback(
    (content: string) => {
      const { span, traceContext } = createTurnSpan("text");
      turnSpanRef.current = span;

      // Add candidate message to chat immediately.
      setMessages((prev) => {
        const displaySeq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
        return [
          ...prev,
          { id: `local-${displaySeq}`, seq: displaySeq, role: "candidate", content },
        ];
      });

      setState("processing");

      const abort = new AbortController();
      streamAbortRef.current = abort;

      (async () => {
        try {
          const stream = client.submitTurn(
            {
              sessionId,
              traceparent: traceContext.traceparent,
              input: { case: "textInput", value: { content } },
            },
            { signal: abort.signal },
          );
          await consumeStream(stream);
        } catch (err) {
          if (abort.signal.aborted) return;
          const msg = err instanceof ConnectError ? err.message : String(err);
          console.error("SubmitTurn error:", msg);
          setLastError(msg);
          setState("waiting");
          if (turnSpanRef.current) {
            closeTurnSpan(turnSpanRef.current, false);
            turnSpanRef.current = null;
          }
        }
      })();
    },
    [sessionId, consumeStream],
  );

  // -- sendAudio --
  const sendAudio = useCallback(
    (audio: Uint8Array, mimeType: string) => {
      const { span, traceContext } = createTurnSpan("voice");
      turnSpanRef.current = span;
      setState("transcribing");

      const abort = new AbortController();
      streamAbortRef.current = abort;

      (async () => {
        try {
          const stream = client.submitTurn(
            {
              sessionId,
              traceparent: traceContext.traceparent,
              input: {
                case: "voiceInput",
                value: { audio, audioMimeType: mimeType },
              },
            },
            { signal: abort.signal },
          );
          await consumeStream(stream);
        } catch (err) {
          if (abort.signal.aborted) return;
          const msg = err instanceof ConnectError ? err.message : String(err);
          console.error("SubmitTurn (voice) error:", msg);
          setLastError(msg);
          setState("waiting");
          if (turnSpanRef.current) {
            closeTurnSpan(turnSpanRef.current, false);
            turnSpanRef.current = null;
          }
        }
      })();
    },
    [sessionId, consumeStream],
  );

  // -- endSession --
  const doEndSession = useCallback(async () => {
    setState("ended"); // Optimistic — show transition immediately.
    try {
      await client.endSession({ sessionId });
      if (turnSpanRef.current) {
        closeTurnSpan(turnSpanRef.current, false);
        turnSpanRef.current = null;
      }
    } catch (err) {
      const msg = err instanceof ConnectError ? err.message : String(err);
      console.error("EndSession error:", msg);
      setLastError(msg);
    }
  }, [sessionId]);

  // -- cancelSession --
  const doCancelSession = useCallback(async () => {
    try {
      await client.cancelSession({ sessionId });
      setState("cancelled");
      if (turnSpanRef.current) {
        closeTurnSpan(turnSpanRef.current, false);
        turnSpanRef.current = null;
      }
    } catch (err) {
      const msg = err instanceof ConnectError ? err.message : String(err);
      console.error("CancelSession error:", msg);
      setLastError(msg);
    }
  }, [sessionId]);

  return {
    messages,
    streamingText,
    state,
    sessionInfo,
    lastError,
    sendText,
    sendAudio,
    endSession: doEndSession,
    cancelSession: doCancelSession,
    setOnTtsChunk,
    setOnTtsDone,
  };
}
