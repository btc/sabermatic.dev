import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { registerInstrumentations } from "@opentelemetry/instrumentation";
import { FetchInstrumentation } from "@opentelemetry/instrumentation-fetch";
import { resourceFromAttributes } from "@opentelemetry/resources";
import { SimpleSpanProcessor } from "@opentelemetry/sdk-trace-base";
import { WebTracerProvider } from "@opentelemetry/sdk-trace-web";

export function initTelemetry() {
  // TODO(telemetry): no backend route serves POST /api/traces. The SPA
  // fallback answers it with 200 and index.html, so production browser spans
  // are discarded, one request per span. Either add an OTLP/HTTP forwarding
  // route in internal/handler or drop the exporter and keep only the
  // FetchInstrumentation traceparent propagation.
  const spanProcessors = import.meta.env.PROD
    ? [new SimpleSpanProcessor(new OTLPTraceExporter({ url: "/api/traces" }))]
    : [];

  const provider = new WebTracerProvider({
    resource: resourceFromAttributes({ "service.name": "drill-web" }),
    spanProcessors,
  });

  provider.register();

  registerInstrumentations({
    instrumentations: [
      new FetchInstrumentation({
        propagateTraceHeaderCorsUrls: [/.*/],
      }),
    ],
  });
}
