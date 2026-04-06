import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { ConnectionManager, ConnectionState } from "../connection";

// Mock WebSocket that simulates onopen after a microtask
class MockWebSocket {
  static OPEN = 1;
  readyState = 1;
  onopen: (() => void) | null = null;
  onclose: ((e: { code: number }) => void) | null = null;
  onmessage: ((e: { data: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  send = vi.fn();
  close = vi.fn();
  constructor(public url: string) {
    setTimeout(() => this.onopen?.(), 0);
  }
}

describe("ConnectionManager", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.stubGlobal("WebSocket", MockWebSocket);
    vi.stubGlobal("location", { protocol: "https:", host: "drill.dev" });
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.restoreAllMocks();
  });

  it("connects and sends session_init", async () => {
    const onMessage = vi.fn();
    const onStateChange = vi.fn();
    const cm = new ConnectionManager("session-123", onMessage, onStateChange);
    cm.connect(() => null);

    await vi.advanceTimersByTimeAsync(0);
    expect(onStateChange).toHaveBeenCalledWith(ConnectionState.Connected);
    const mockSend = cm.ws!.send as ReturnType<typeof vi.fn>;
    const sent = JSON.parse(mockSend.mock.calls[0]![0] as string);
    expect(sent).toEqual({ type: "session_init", last_seq: null });
  });

  it("reconnects with exponential backoff on unexpected close", async () => {
    vi.spyOn(Math, "random").mockReturnValue(0.5); // deterministic jitter
    const onMessage = vi.fn();
    const onStateChange = vi.fn();
    const cm = new ConnectionManager("session-123", onMessage, onStateChange);
    cm.connect(() => null);
    await vi.advanceTimersByTimeAsync(0);

    // Simulate unexpected close (code 1006)
    cm.ws!.onclose?.({ code: 1006 } as CloseEvent);
    expect(onStateChange).toHaveBeenCalledWith(ConnectionState.Reconnecting);

    // First retry fires at 750ms (jitter: 1000 * (0.5 + 0.5*0.5) = 750ms)
    // Advance to just past 750ms but before the MockWebSocket's onopen (setTimeout 0)
    await vi.advanceTimersByTimeAsync(750);
    expect(onStateChange).toHaveBeenLastCalledWith(ConnectionState.Connecting);
  });

  it("reconnects immediately on reconnect_please", async () => {
    const onMessage = vi.fn();
    const onStateChange = vi.fn();
    const cm = new ConnectionManager("session-123", onMessage, onStateChange);
    cm.connect(() => 5);
    await vi.advanceTimersByTimeAsync(0);

    // Receive reconnect_please
    cm.ws!.onmessage?.({ data: JSON.stringify({ type: "reconnect_please" }) } as MessageEvent);

    // reconnect_please calls ws.close(1000), which triggers onclose
    cm.ws!.onclose?.({ code: 1000 } as CloseEvent);

    // Should reconnect immediately
    await vi.advanceTimersByTimeAsync(0);
    // Two send calls: first init + reconnect init
    expect(cm.ws!.send).toHaveBeenCalledTimes(1); // new WS, fresh send mock
  });

  it("does not reconnect on normal close (1000)", async () => {
    const onMessage = vi.fn();
    const onStateChange = vi.fn();
    const cm = new ConnectionManager("session-123", onMessage, onStateChange);
    cm.connect(() => null);
    await vi.advanceTimersByTimeAsync(0);

    cm.ws!.onclose?.({ code: 1000 } as CloseEvent);
    expect(onStateChange).toHaveBeenCalledWith(ConnectionState.Disconnected);

    // No reconnect attempt
    await vi.advanceTimersByTimeAsync(30000);
    expect(onStateChange).not.toHaveBeenCalledWith(ConnectionState.Reconnecting);
  });

  it("destroy prevents reconnection", async () => {
    const onMessage = vi.fn();
    const onStateChange = vi.fn();
    const cm = new ConnectionManager("session-123", onMessage, onStateChange);
    cm.connect(() => null);
    await vi.advanceTimersByTimeAsync(0);

    cm.destroy();
    // Should not try to reconnect
    expect(cm.ws).toBeNull();
  });
});
