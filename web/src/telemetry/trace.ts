import { context, propagation, trace, SpanStatusCode } from "@opentelemetry/api";
import type { Span } from "@opentelemetry/api";
import type { TraceContext } from "@/ws/protocol";

const tracer = trace.getTracer("drill-web");

export function createTurnSpan(inputMethod: "text" | "voice"): { span: Span; traceContext: TraceContext } {
  const span = tracer.startSpan("turn.submit", {
    attributes: { "turn.input_method": inputMethod },
  });
  const carrier: Record<string, string> = {};
  propagation.inject(trace.setSpan(context.active(), span), carrier);
  return {
    span,
    traceContext: {
      traceparent: carrier["traceparent"] ?? "",
    },
  };
}

export function closeTurnSpan(span: Span, ok = true) {
  span.setStatus({ code: ok ? SpanStatusCode.OK : SpanStatusCode.ERROR });
  span.end();
}
