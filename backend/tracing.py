import json
import os
from datetime import datetime, timezone
from typing import Sequence

from opentelemetry import trace
from opentelemetry.sdk.trace import TracerProvider, ReadableSpan
from opentelemetry.sdk.trace.export import SpanExporter, SpanExportResult, BatchSpanProcessor
from opentelemetry.instrumentation.fastapi import FastAPIInstrumentor
from opentelemetry.instrumentation.httpx import HTTPXClientInstrumentor


class FileSpanExporter(SpanExporter):
    """Export spans as JSON to data/traces/YYYY-MM-DD/"""

    def __init__(self, base_dir: str = "./data/traces") -> None:
        self.base_dir = base_dir

    def export(self, spans: Sequence[ReadableSpan]) -> SpanExportResult:
        for span in spans:
            try:
                ctx = span.get_span_context()
                if ctx is None:
                    continue
                trace_id_hex = format(ctx.trace_id, "032x")
                trace_id_short = trace_id_hex[:6]
                span_name_safe = span.name.replace("/", "_").replace(" ", "_")
                filename = f"{trace_id_short}_{span_name_safe}.jsonl"

                start_ns = span.start_time or 0
                dt = datetime.fromtimestamp(start_ns / 1e9, tz=timezone.utc)
                date_str = dt.strftime("%Y-%m-%d")
                day_dir = os.path.join(self.base_dir, date_str)
                os.makedirs(day_dir, exist_ok=True)

                record = {
                    "trace_id": trace_id_hex,
                    "trace_id_short": trace_id_short,
                    "span_id": format(ctx.span_id, "016x"),
                    "name": span.name,
                    "start_time": dt.isoformat(),
                    "duration_ms": round((span.end_time - span.start_time) / 1e6, 2)
                    if span.end_time and span.start_time
                    else None,
                    "status": span.status.status_code.name if span.status else None,
                    "attributes": dict(span.attributes) if span.attributes else {},
                }

                filepath = os.path.join(day_dir, filename)
                with open(filepath, "a") as f:
                    f.write(json.dumps(record) + "\n")
            except Exception:
                pass

        return SpanExportResult.SUCCESS

    def shutdown(self) -> None:
        pass


# User-friendly action labels for the trace widget
ACTION_LABELS: dict[str, str] = {
    "interview.turn": "Stopped talking",
    "speech.transcribe": "Transcribing speech",
    "llm.interviewer": "Interviewer responding",
    "speech.tts": "Generating audio",
    "interview.evaluate": "Evaluating session",
    "coach.analyze": "Coach analyzing",
    "db.save_message": "Saving message",
}


def setup_tracing(data_dir: str = "./data") -> None:
    """Initialize OTEL with file export."""
    provider = TracerProvider()
    exporter = FileSpanExporter(base_dir=os.path.join(data_dir, "traces"))
    provider.add_span_processor(BatchSpanProcessor(exporter))
    trace.set_tracer_provider(provider)
    HTTPXClientInstrumentor().instrument()


def instrument_app(app: object) -> None:
    FastAPIInstrumentor.instrument_app(app)  # type: ignore[arg-type]


def get_tracer(name: str) -> trace.Tracer:
    return trace.get_tracer(name)
