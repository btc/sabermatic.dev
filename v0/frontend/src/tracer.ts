/**
 * Lightweight client-side tracer.
 *
 * Records user-action spans (spacebar, recording, WS send/receive) and
 * POSTs them to /api/traces/client. The backend stores them alongside
 * server-side OTEL spans so they show up in the trace widget and Jaeger.
 *
 * No heavy OTEL browser SDK — just timestamps and fetch.
 */

let sessionTraceId: string | null = null;

function randomHex(bytes: number): string {
  const arr = new Uint8Array(bytes);
  crypto.getRandomValues(arr);
  return Array.from(arr, (b) => b.toString(16).padStart(2, "0")).join("");
}

/** Set the trace ID for this interview session (call on session start). */
export function setSessionTraceId(id?: string) {
  sessionTraceId = id ?? randomHex(16);
}

export function getSessionTraceId(): string | null {
  return sessionTraceId;
}

export interface ClientSpan {
  trace_id: string;
  span_id: string;
  parent_span_id: string | null;
  name: string;
  start_time: string;
  end_time: string | null;
  duration_ms: number | null;
  attributes: Record<string, unknown>;
  source: "client";
}

const pendingSpans: ClientSpan[] = [];
let flushTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleFlush() {
  if (flushTimer) return;
  flushTimer = setTimeout(flush, 500);
}

async function flush() {
  flushTimer = null;
  if (pendingSpans.length === 0) return;
  const batch = pendingSpans.splice(0);
  try {
    await fetch("/api/traces/client", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(batch),
    });
  } catch {
    // Best-effort — don't break the app if tracing fails
  }
}

/** Start a span. Returns a handle to end it. */
export function startSpan(
  name: string,
  attributes: Record<string, unknown> = {},
  parentSpanId: string | null = null
): { spanId: string; end: (extraAttrs?: Record<string, unknown>) => void } {
  const spanId = randomHex(8);
  const startTime = new Date().toISOString();
  const startMs = performance.now();

  return {
    spanId,
    end(extraAttrs?: Record<string, unknown>) {
      const endTime = new Date().toISOString();
      const durationMs = Math.round(performance.now() - startMs);
      const span: ClientSpan = {
        trace_id: sessionTraceId ?? randomHex(16),
        span_id: spanId,
        parent_span_id: parentSpanId,
        name,
        start_time: startTime,
        end_time: endTime,
        duration_ms: durationMs,
        attributes: { ...attributes, ...extraAttrs },
        source: "client",
      };
      pendingSpans.push(span);
      scheduleFlush();
    },
  };
}

/** Record an instant event (zero-duration span). */
export function recordEvent(
  name: string,
  attributes: Record<string, unknown> = {}
) {
  const now = new Date().toISOString();
  const span: ClientSpan = {
    trace_id: sessionTraceId ?? randomHex(16),
    span_id: randomHex(8),
    parent_span_id: null,
    name,
    start_time: now,
    end_time: now,
    duration_ms: 0,
    attributes,
    source: "client",
  };
  pendingSpans.push(span);
  scheduleFlush();
}
