import json
import os
from datetime import datetime, timezone

from fastapi import APIRouter, Query, Request

from backend.tracing import ACTION_LABELS

router = APIRouter(prefix="/traces", tags=["traces"])


@router.get("/recent")
async def get_recent_traces(
    request: Request,
    limit: int = Query(default=50, le=200),
) -> list[dict]:
    """Return recent trace entries for the widget."""
    settings = request.app.state.settings
    traces_dir = os.path.join(str(settings.data_dir), "traces")

    if not os.path.isdir(traces_dir):
        return []

    # Collect all .jsonl files, sorted by modification time descending
    entries: list[dict] = []
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
                        record["action_label"] = ACTION_LABELS.get(record.get("name", ""), record.get("name", ""))
                        entries.append(record)
                        if len(entries) >= limit:
                            return entries
            except (OSError, json.JSONDecodeError):
                continue

        if len(entries) >= limit:
            break

    return entries
