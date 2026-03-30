import { useCallback, useRef, useState } from "react";
import { InterviewSocket } from "../api/ws";
import type { WSClientMessage, WSServerMessage } from "../types";

export type ConnectionStatus = "connected" | "disconnected" | "reconnecting";

export function useWebSocket(onMessage: (msg: WSServerMessage) => void) {
  const onMessageRef = useRef(onMessage);
  onMessageRef.current = onMessage;

  const socketRef = useRef<InterviewSocket | null>(null);
  const [connectionStatus, setConnectionStatus] = useState<ConnectionStatus>("disconnected");

  const connect = useCallback(async (sessionId: number) => {
    if (socketRef.current?.connected) return;

    const socket = new InterviewSocket();
    socket.onMessage((msg) => onMessageRef.current(msg));
    socket.onStatus((status) => setConnectionStatus(status));
    await socket.connect(sessionId);
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
    setConnectionStatus("disconnected");
  }, []);

  return { connect, send, disconnect, socketRef, connectionStatus };
}
