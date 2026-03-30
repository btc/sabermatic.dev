import json
import os
from datetime import datetime, timezone

from typing import Any

from fastapi import APIRouter, Query, Request
from pydantic import BaseModel

from backend.tracing import ACTION_LABELS

router = APIRouter(prefix="/traces", tags=["traces"])


@router.get("/recent")
async def get_recent_traces(
    request: Request,
    limit: int = Query(default=50, le=200),
) -> list[dict[str, Any]]:
    """Return recent trace entries for the widget."""
    settings = request.app.state.settings
    traces_dir = os.path.join(str(settings.data_dir), "traces")

    if not os.path.isdir(traces_dir):
        return []

    # Collect all .jsonl files, sorted by modification time descending
    entries: list[dict[str, Any]] = []
    day_dirs = sorted(
        [d for d in os.listdir(traces_dir) if os.path.isdir(os.path.join(traces_dir, d))],
        reverse=True,
    )

    for day in day_dirs:
        day_path = os.path.join(traces_dir, day)
        try:
            filenames = sorted(
                os.listdir(day_path),
                key=lambda f: os.path.getmtime(os.path.join(day_path, f)),
                reverse=True,
            )
        except OSError:
            continue

        for filename in filenames:
            if not filename.endswith(".jsonl"):
                continue
            filepath = os.path.join(day_path, filename)
            try:
                with open(filepath) as f:
                    for line in reversed(f.readlines()):
                        line = line.strip()
                        if not line:
                            continue
                        record = json.loads(line)
                        name = record.get("name", "")
                        record.setdefault("action_label",
                            ACTION_LABELS.get(name, CLIENT_ACTION_LABELS.get(name, name))
                        )
                        entries.append(record)
                        if len(entries) >= limit:
                            return entries
            except (OSError, json.JSONDecodeError):
                continue

        if len(entries) >= limit:
            break

    return entries


# ---- Client-side trace ingestion ----

CLIENT_ACTION_LABELS: dict[str, str] = {
    "user.begin_interview": "Clicked Begin",
    "user.spacebar_press": "Started speaking",
    "user.spacebar_release": "Stopped speaking",
    "audio.encode": "Encoding audio",
    "audio.first_chunk_received": "First TTS chunk",
    "audio.flush_empty": "Flush (no audio)",
    "audio.playback_attempt": "Playing audio",
    "audio.play_started": "Audio started",
    "ws.send": "Sent message",
    "ws.receive": "Received message",
    "user.text_submit": "Typed response",
    "audio.playback_start": "Audio playing",
    "audio.playback_stop": "Audio stopped",
    "user.end_session": "Ended session",
}


class ClientSpan(BaseModel):
    trace_id: str
    span_id: str
    parent_span_id: str | None = None
    name: str
    start_time: str
    end_time: str | None = None
    duration_ms: float | None = None
    attributes: dict[str, Any] = {}
    source: str = "client"


@router.post("/client")
async def ingest_client_traces(
    request: Request,
    spans: list[ClientSpan],
) -> dict[str, int]:
    """Receive client-side trace spans and write to the trace file store."""
    settings = request.app.state.settings
    traces_dir = os.path.join(str(settings.data_dir), "traces")

    now = datetime.now(timezone.utc)
    date_str = now.strftime("%Y-%m-%d")
    day_dir = os.path.join(traces_dir, date_str)
    os.makedirs(day_dir, exist_ok=True)

    written = 0
    for span in spans:
        trace_short = span.trace_id[:6]
        span_name_safe = span.name.replace("/", "_").replace(" ", "_")
        filename = f"{trace_short}_{span_name_safe}.jsonl"
        filepath = os.path.join(day_dir, filename)

        record = {
            "trace_id": span.trace_id,
            "trace_id_short": trace_short,
            "span_id": span.span_id,
            "parent_span_id": span.parent_span_id,
            "name": span.name,
            "start_time": span.start_time,
            "duration_ms": span.duration_ms,
            "status": "OK",
            "attributes": span.attributes,
            "source": "client",
            "action_label": CLIENT_ACTION_LABELS.get(span.name, span.name),
        }

        try:
            with open(filepath, "a") as f:
                f.write(json.dumps(record) + "\n")
            written += 1
        except OSError:
            pass

    return {"written": written}
