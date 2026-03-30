import type { WSClientMessage, WSServerMessage } from "../types";

type MessageHandler = (msg: WSServerMessage) => void;

export class InterviewSocket {
  private ws: WebSocket | null = null;
  private listeners: Set<MessageHandler> = new Set();

  /**
   * Open a WebSocket connection to /ws/interview/{sessionId}.
   * Resolves once the socket is open; rejects on error or timeout.
   */
  connect(sessionId: number): Promise<void> {
    return new Promise((resolve, reject) => {
      const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
      const url = `${proto}//${window.location.host}/ws/interview/${sessionId}`;
      const ws = new WebSocket(url);

      const timeout = setTimeout(() => {
        ws.close();
        reject(new Error("WebSocket connection timed out"));
      }, 10_000);

      ws.onopen = () => {
        clearTimeout(timeout);
        this.ws = ws;
        resolve();
      };

      ws.onerror = () => {
        clearTimeout(timeout);
        reject(new Error("WebSocket connection failed"));
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data) as WSServerMessage;
          for (const fn of this.listeners) {
            fn(msg);
          }
        } catch {
          // ignore malformed messages
        }
      };

      ws.onclose = () => {
        this.ws = null;
      };
    });
  }

  /** Send a typed client message as JSON. */
  send(msg: WSClientMessage): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error("WebSocket is not connected");
    }
    this.ws.send(JSON.stringify(msg));
  }

  /** Subscribe to incoming messages. Returns an unsubscribe function. */
  onMessage(fn: MessageHandler): () => void {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  }

  /** Close the WebSocket connection. */
  disconnect(): void {
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.listeners.clear();
  }

  get connected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }
}
