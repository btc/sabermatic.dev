import type { ClientMessage, ServerMessage } from "./protocol";

export enum ConnectionState {
  Disconnected = "disconnected",
  Connecting = "connecting",
  Connected = "connected",
  Reconnecting = "reconnecting",
}

export class ConnectionManager {
  ws: WebSocket | null = null;
  private getLastSeq: () => number | null = () => null;
  private retryCount = 0;
  private static readonly MAX_RETRIES = 20;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private healthCheckTimer: ReturnType<typeof setTimeout> | null = null;
  private destroyed = false;
  private immediateReconnect = false;

  constructor(
    private sessionId: string,
    private onMessage: (msg: ServerMessage) => void,
    private onStateChange: (state: ConnectionState) => void,
  ) {}

  connect(getLastSeq: () => number | null) {
    this.getLastSeq = getLastSeq;
    this.open();
  }

  send(msg: ClientMessage) {
    if (this.ws?.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    }
  }

  setMessageHandler(handler: (msg: ServerMessage) => void) {
    this.onMessage = handler;
  }

  destroy() {
    this.destroyed = true;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    if (this.healthCheckTimer) clearTimeout(this.healthCheckTimer);
    this.ws?.close(1000);
    this.ws = null;
  }

  healthCheck() {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return;
    let gotPong = false;
    const prevHandler = this.onMessage;
    const pongListener = (msg: ServerMessage) => {
      prevHandler(msg);
      if (msg.type === "pong") {
        gotPong = true;
      }
    };
    this.onMessage = pongListener;
    this.send({ type: "ping" });
    this.healthCheckTimer = setTimeout(() => {
      this.onMessage = prevHandler;
      if (!gotPong && this.ws?.readyState === WebSocket.OPEN) {
        this.ws.close(4000, "health check timeout");
      }
    }, 3000);
  }

  retry() {
    this.retryCount = 0;
    this.open();
  }

  private open() {
    if (this.destroyed) return;
    this.onStateChange(ConnectionState.Connecting);

    const protocol = location.protocol === "https:" ? "wss:" : "ws:";
    this.ws = new WebSocket(`${protocol}//${location.host}/api/sessions/${this.sessionId}/ws`);

    this.ws.onopen = () => {
      this.retryCount = 0;
      this.onStateChange(ConnectionState.Connected);
      this.send({ type: "session_init", last_seq: this.getLastSeq() });
    };

    this.ws.onmessage = (event) => {
      let msg: ServerMessage;
      try {
        msg = JSON.parse(event.data as string) as ServerMessage;
      } catch {
        console.error("Malformed WS message:", event.data);
        return;
      }
      // NOTE: no runtime schema validation (e.g. zod) — the server is trusted.
      // Add validation here if third-party or untrusted WS sources are introduced.
      if (msg.type === "reconnect_please") {
        this.immediateReconnect = true;
        this.ws?.close(1000);
        return;
      }
      this.onMessage(msg);
    };

    this.ws.onclose = (event) => {
      if (this.destroyed) return;
      if (this.immediateReconnect) {
        this.immediateReconnect = false;
        this.open();
        return;
      }
      if (event.code === 1000) {
        this.onStateChange(ConnectionState.Disconnected);
        return;
      }
      // Unexpected close — reconnect with backoff
      if (this.retryCount >= ConnectionManager.MAX_RETRIES) {
        this.onStateChange(ConnectionState.Disconnected);
        return;
      }
      this.onStateChange(ConnectionState.Reconnecting);
      const delay = Math.min(1000 * Math.pow(2, this.retryCount), 30000);
      const jitter = delay * (0.5 + Math.random() * 0.5);
      this.retryCount++;
      this.retryTimer = setTimeout(() => this.open(), jitter);
    };

    this.ws.onerror = () => {
      // onerror is always followed by onclose
    };
  }
}
