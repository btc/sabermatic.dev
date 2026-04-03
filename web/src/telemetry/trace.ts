import { trace, SpanStatusCode } from "@opentelemetry/api";
import type { Span } from "@opentelemetry/api";
import type { TraceContext } from "@/ws/protocol";

const tracer = trace.getTracer("drill-web");

export function createTurnSpan(inputMethod: "text" | "voice"): { span: Span; traceContext: TraceContext } {
  const span = tracer.startSpan("turn.submit", {
    attributes: { "turn.input_method": inputMethod },
  });
  const spanContext = span.spanContext();
  return {
    span,
    traceContext: {
      trace_id: spanContext.traceId,
      span_id: spanContext.spanId,
    },
  };
}

export function closeTurnSpan(span: Span) {
  span.setStatus({ code: SpanStatusCode.OK });
  span.end();
}
