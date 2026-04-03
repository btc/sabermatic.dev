import { useState, useEffect, useRef, useCallback } from "react";
import { ConnectionManager, ConnectionState } from "./connection";
import type { ServerMessage, TraceContext } from "./protocol";

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
  | "ended";

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

  const cmRef = useRef<ConnectionManager | null>(null);
  const lastSeqRef = useRef<number | null>(null);
  // Use ref for streaming text to avoid stale closures in handleMessage
  const streamingTextRef = useRef("");
  const rawMessageHandlerRef = useRef<((msg: ServerMessage) => void) | null>(null);

  const handleMessage = useCallback((msg: ServerMessage) => {
    switch (msg.type) {
      case "session_loaded":
        setSessionInfo({
          duration: msg.duration,
          tts_enabled: msg.tts_enabled,
          question: msg.question,
        });
        setState("streaming"); // opening message incoming
        break;

      case "reconnect_state": {
        const msgs = msg.messages;
        if (msgs.length > 0) {
          lastSeqRef.current = msgs[msgs.length - 1]!.seq;
        }
        setMessages(
          msgs.map((m) => ({
            id: m.id,
            seq: m.seq,
            role: m.role,
            content: m.content,
          })),
        );
        setState("waiting");
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
        break;
      }

      case "transcription_result":
        // Transcription text is informational (no edit window per spec)
        break;

      case "session_ended":
        setState("ended");
        break;

      case "error":
        console.error(`WS error: ${msg.code} — ${msg.message}`);
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
    cm.connect(lastSeqRef.current);
    return () => cm.destroy();
  }, [sessionId, handleMessage]);

  const sendText = useCallback((content: string, traceContext?: TraceContext) => {
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

  const sendAudio = useCallback((audioBase64: string, traceContext?: TraceContext) => {
    setState("transcribing");
    cmRef.current?.send({ type: "end_turn", audio: audioBase64, input_method: "voice", trace_context: traceContext });
  }, []);

  const endSession = useCallback(() => {
    cmRef.current?.send({ type: "end_session" });
  }, []);

  const cancelTts = useCallback(() => {
    cmRef.current?.send({ type: "cancel_tts" });
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
    sendText,
    sendAudio,
    endSession,
    cancelTts,
    setRawMessageHandler,
  };
}
