import { useState, useEffect, useRef, useCallback } from "react";
import type { Span } from "@opentelemetry/api";
import { toast } from "sonner";
import { ConnectionManager, ConnectionState } from "./connection";
import type { ServerMessage } from "./protocol";
import { createTurnSpan, closeTurnSpan } from "@/telemetry/trace";

interface InterviewMessage {
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

// TODO: Add integration tests for useInterview — requires WebSocket mock infrastructure
export function useInterview(sessionId: string) {
  const [messages, setMessages] = useState<InterviewMessage[]>([]);
  const [streamingText, setStreamingText] = useState("");
  const [state, setState] = useState<InterviewState>("connecting");
  const [connectionState, setConnectionState] = useState(ConnectionState.Disconnected);
  const [sessionInfo, setSessionInfo] = useState<{
    duration: number;
    tts_enabled: boolean;
    question: { title: string; prompt: string };
  } | null>(null);
  const [lastError, setLastError] = useState<string | null>(null);

  const cmRef = useRef<ConnectionManager | null>(null);
  const lastSeqRef = useRef<number | null>(null);
  // Use ref for streaming text to avoid stale closures in handleMessage
  const streamingTextRef = useRef("");
  const rawMessageHandlerRef = useRef<((msg: ServerMessage) => void) | null>(null);
  // Active turn span — open from send to interviewer_done.
  const turnSpanRef = useRef<Span | null>(null);
  const pendingActionRef = useRef<"cancel" | "end" | null>(null);
  const [wasReconnected, setWasReconnected] = useState(false);

  const handleMessage = useCallback((msg: ServerMessage) => {
    switch (msg.type) {
      case "session_loaded":
        setSessionInfo({
          duration: msg.duration,
          tts_enabled: msg.tts_enabled,
          question: msg.question,
        });
        // State will be set by the next message: either interviewer_token
        // (new session) or reconnect_state (page refresh of existing session).
        break;

      case "reconnect_state": {
        // Do not update lastSeqRef here. The CM getter always reads the
        // current ref value, and keeping it null ensures every reconnect
        // takes the page-refresh path (server sends all messages). This
        // avoids message loss when setMessages replaces the array with
        // only a delta on second+ reconnects.
        const msgs = msg.messages;
        setMessages(
          msgs.map((m) => ({
            id: m.id,
            seq: m.seq,
            role: m.role,
            content: m.content,
          })),
        );
        setState("waiting");
        setWasReconnected(true);
        break;
      }

      case "state_change":
        // Map backend conductor states to UI states
        if (msg.state === "transcribing") setState("transcribing");
        else if (msg.state === "processing_input") setState("processing");
        else if (msg.state === "interviewer_speaking") setState("streaming");
        else if (msg.state === "waiting_for_input") setState("waiting");
        else if (msg.state === "ending" || msg.state === "ended") setState("ended");
        break;

      case "interviewer_token":
        streamingTextRef.current += msg.token;
        setStreamingText(streamingTextRef.current);
        break;

      case "interviewer_done": {
        const finalText = streamingTextRef.current;
        streamingTextRef.current = "";
        setStreamingText("");
        setMessages((prev) => {
          // Do NOT update lastSeqRef here — the reconnect_state handler sets it
          // from authoritative server data. Updating it here would cause
          // session_init.last_seq to diverge from server-acknowledged seqs.
          const seq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
          return [...prev, { id: msg.message_id, seq, role: "interviewer", content: finalText }];
        });
        // Close the turn span — full round-trip from send to response complete.
        if (turnSpanRef.current) {
          closeTurnSpan(turnSpanRef.current);
          turnSpanRef.current = null;
        }
        break;
      }

      case "transcription_result": {
        // Add the candidate's voice message to the chat using transcribed text.
        const text = msg.text;
        setMessages((prev) => {
          const displaySeq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
          return [...prev, { id: `local-${displaySeq}`, seq: displaySeq, role: "candidate", content: text }];
        });
        break;
      }

      case "ack":
        if (pendingActionRef.current === "cancel") {
          setState("cancelled");
        } else if (pendingActionRef.current === "end") {
          setState("ended");
        }
        pendingActionRef.current = null;
        // Close any outstanding turn span on ack.
        if (turnSpanRef.current) {
          closeTurnSpan(turnSpanRef.current, false);
          turnSpanRef.current = null;
        }
        break;

      case "tts_error":
        toast.info("Audio temporarily unavailable");
        break;

      case "audio_upload_failed":
        toast.info("Audio recording could not be saved. Your response was captured as text.");
        break;

      case "error":
        console.error(`WS error: ${msg.code} — ${msg.message}`);
        setLastError(msg.message);
        break;

      // timer_warning, timer_overtime: handled by useTimer
      // tts_chunk, tts_done: handled by audio player via onRawMessage
      default:
        break;
    }
  }, []); // No dependencies — uses refs for mutable state

  useEffect(() => {
    const cm = new ConnectionManager(sessionId, handleMessage, setConnectionState);
    cmRef.current = cm;
    cm.connect(() => lastSeqRef.current);
    return () => cm.destroy();
  }, [sessionId, handleMessage]);

  const sendText = useCallback((content: string) => {
    const { span, traceContext } = createTurnSpan("text");
    turnSpanRef.current = span;
    // Use a local display seq derived from current messages — do NOT modify
    // lastSeqRef, which is reserved for server-acknowledged seqs used in
    // session_init.last_seq on reconnect.
    setMessages((prev) => {
      const displaySeq = prev.length > 0 ? prev[prev.length - 1]!.seq + 1 : 1;
      return [...prev, { id: `local-${displaySeq}`, seq: displaySeq, role: "candidate", content }];
    });
    setState("processing");
    cmRef.current?.send({ type: "end_turn", content, input_method: "text", trace_context: traceContext });
  }, []);

  const sendAudio = useCallback((audioBase64: string) => {
    const { span, traceContext } = createTurnSpan("voice");
    turnSpanRef.current = span;
    setState("transcribing");
    cmRef.current?.send({ type: "end_turn", audio: audioBase64, input_method: "voice", trace_context: traceContext });
  }, []);

  const endSession = useCallback(() => {
    pendingActionRef.current = "end";
    cmRef.current?.send({ type: "end_session" });
  }, []);

  const cancelSession = useCallback(() => {
    pendingActionRef.current = "cancel";
    cmRef.current?.send({ type: "cancel_session" });
  }, []);

  // Expose raw WS messages for audio player integration (tts_chunk, tts_done)
  const setRawMessageHandler = useCallback((handler: (msg: ServerMessage) => void) => {
    rawMessageHandlerRef.current = handler;
  }, []);

  // Update the connection manager's message handler to also forward to the raw handler.
  useEffect(() => {
    if (!cmRef.current) return;
    const original = handleMessage;
    cmRef.current.setMessageHandler((msg: ServerMessage) => {
      original(msg);
      rawMessageHandlerRef.current?.(msg);
    });
  }, [handleMessage]);

  return {
    messages,
    streamingText,
    state,
    connectionState,
    sessionInfo,
    lastError,
    wasReconnected,
    sendText,
    sendAudio,
    endSession,
    cancelSession,
    setRawMessageHandler,
    cmRef,
  };
}
