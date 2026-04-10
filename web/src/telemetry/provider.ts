import { OTLPTraceExporter } from "@opentelemetry/exporter-trace-otlp-http";
import { registerInstrumentations } from "@opentelemetry/instrumentation";
import { FetchInstrumentation } from "@opentelemetry/instrumentation-fetch";
import { resourceFromAttributes } from "@opentelemetry/resources";
import { SimpleSpanProcessor } from "@opentelemetry/sdk-trace-base";
import { WebTracerProvider } from "@opentelemetry/sdk-trace-web";

export function initTelemetry() {
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
