import type { WSClientMessage, WSServerMessage } from "../types";

type MessageHandler = (msg: WSServerMessage) => void;
type StatusHandler = (status: "connected" | "disconnected" | "reconnecting") => void;

export class InterviewSocket {
  private ws: WebSocket | null = null;
  private listeners: Set<MessageHandler> = new Set();
  private statusListeners: Set<StatusHandler> = new Set();
  private sessionId: number | null = null;
  private reconnectAttempts = 0;
  private maxReconnectAttempts = 5;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private intentionalClose = false;

  connect(sessionId: number): Promise<void> {
    this.sessionId = sessionId;
    this.intentionalClose = false;
    return this._connect(sessionId);
  }

  private _connect(sessionId: number): Promise<void> {
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
        this.reconnectAttempts = 0;
        this._notifyStatus("connected");
        resolve();
      };

      ws.onerror = () => {
        clearTimeout(timeout);
        reject(new Error("WebSocket connection failed"));
      };

      ws.onmessage = (event) => {
        try {
          const msg = JSON.parse(event.data) as WSServerMessage;
          for (const fn of this.listeners) fn(msg);
        } catch {
          // ignore malformed messages from server
        }
      };

      ws.onclose = () => {
        this.ws = null;
        if (!this.intentionalClose && this.sessionId !== null) {
          this._scheduleReconnect();
        } else {
          this._notifyStatus("disconnected");
        }
      };
    });
  }

  private _scheduleReconnect(): void {
    if (this.reconnectAttempts >= this.maxReconnectAttempts) {
      this._notifyStatus("disconnected");
      return;
    }
    this._notifyStatus("reconnecting");
    const delay = Math.min(1000 * Math.pow(2, this.reconnectAttempts), 16000);
    this.reconnectAttempts++;
    this.reconnectTimer = setTimeout(async () => {
      if (this.sessionId === null || this.intentionalClose) return;
      try {
        await this._connect(this.sessionId);
      } catch {
        // onclose will fire again and trigger next retry
      }
    }, delay);
  }

  private _notifyStatus(status: "connected" | "disconnected" | "reconnecting"): void {
    for (const fn of this.statusListeners) fn(status);
  }

  send(msg: WSClientMessage): void {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) {
      throw new Error("WebSocket is not connected");
    }
    this.ws.send(JSON.stringify(msg));
  }

  onMessage(fn: MessageHandler): () => void {
    this.listeners.add(fn);
    return () => { this.listeners.delete(fn); };
  }

  onStatus(fn: StatusHandler): () => void {
    this.statusListeners.add(fn);
    return () => { this.statusListeners.delete(fn); };
  }

  disconnect(): void {
    this.intentionalClose = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.ws) {
      this.ws.close();
      this.ws = null;
    }
    this.listeners.clear();
    this.statusListeners.clear();
  }

  get connected(): boolean {
    return this.ws !== null && this.ws.readyState === WebSocket.OPEN;
  }
}
