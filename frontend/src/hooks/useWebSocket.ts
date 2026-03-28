import { useCallback, useRef } from "react";
import { InterviewSocket } from "../api/ws";
import type { WSClientMessage, WSServerMessage } from "../types";

export function useWebSocket(onMessage: (msg: WSServerMessage) => void) {
  // Stable ref so the callback inside the socket listener always reads the
  // latest onMessage without causing reconnects when it changes.
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  const socketRef = useRef<InterviewSocket | null>(null);

  const connect = useCallback(async () => {
    // Avoid double-connecting
    if (socketRef.current?.connected) return;

    const socket = new InterviewSocket();
    await socket.connect();
    socket.onMessage((msg) => onMessageRef.current(msg));
    socketRef.current = socket;
  }, []);

  const send = useCallback((msg: WSClientMessage) => {
    if (!socketRef.current?.connected) {
      console.error("[drill] WS send failed: not connected, msg type:", msg.type);
      return;
    }
    console.log("[drill] WS send:", msg.type);
    socketRef.current.send(msg);
  }, []);

  const disconnect = useCallback(() => {
    socketRef.current?.disconnect();
    socketRef.current = null;
  }, []);

  return { connect, send, disconnect, socketRef };
}
