"""Tests for tracing module and traces route."""

import json
import os
import tempfile
from unittest.mock import MagicMock, patch

import pytest
import pytest_asyncio
from asgi_lifespan import LifespanManager
from httpx import ASGITransport, AsyncClient
from opentelemetry import trace
from opentelemetry.sdk.trace import ReadableSpan
from opentelemetry.sdk.trace.export import SpanExportResult
from opentelemetry.trace import SpanContext, TraceFlags

from backend.config import Settings
from backend.main import create_app
from backend.tracing import ACTION_LABELS, FileSpanExporter, get_tracer


# ---------------------------------------------------------------------------
# FileSpanExporter
# ---------------------------------------------------------------------------


def _make_span(name: str, trace_id: int, span_id: int, start_ns: int, end_ns: int) -> ReadableSpan:
    """Build a minimal ReadableSpan mock."""
    span = MagicMock(spec=ReadableSpan)
    span.name = name
    span.start_time = start_ns
    span.end_time = end_ns
    span.status = MagicMock()
    span.status.status_code = MagicMock()
    span.status.status_code.name = "OK"
    span.attributes = {"key": "value"}

    ctx = SpanContext(
        trace_id=trace_id,
        span_id=span_id,
        is_remote=False,
        trace_flags=TraceFlags(TraceFlags.SAMPLED),
    )
    span.get_span_context.return_value = ctx
    return span


def test_file_span_exporter_creates_directory_and_writes_file():
    with tempfile.TemporaryDirectory() as tmpdir:
        exporter = FileSpanExporter(base_dir=tmpdir)

        # ~2025-01-15 00:00:00 UTC in nanoseconds
        start_ns = 1_736_899_200_000_000_000
        end_ns = start_ns + 500_000_000  # 500 ms later

        span = _make_span(
            name="interview.turn",
            trace_id=0xABCDEF123456789012345678901234AB,
            span_id=0x1234567890ABCDEF,
            start_ns=start_ns,
            end_ns=end_ns,
        )

        result = exporter.export([span])
        assert result == SpanExportResult.SUCCESS

        # Directory YYYY-MM-DD should exist
        day_dirs = os.listdir(tmpdir)
        assert len(day_dirs) == 1
        day_dir = os.path.join(tmpdir, day_dirs[0])
        assert os.path.isdir(day_dir)

        # One .jsonl file
        files = os.listdir(day_dir)
        assert len(files) == 1
        assert files[0].endswith(".jsonl")

        # Validate content
        with open(os.path.join(day_dir, files[0])) as f:
            record = json.loads(f.readline())

        assert record["name"] == "interview.turn"
        assert record["status"] == "OK"
        assert record["duration_ms"] == pytest.approx(500.0, abs=1.0)
        assert record["trace_id_short"] == record["trace_id"][:6]


def test_file_span_exporter_filename_uses_trace_id_short_and_span_name():
    with tempfile.TemporaryDirectory() as tmpdir:
        exporter = FileSpanExporter(base_dir=tmpdir)

        start_ns = 1_736_899_200_000_000_000
        span = _make_span(
            name="speech.transcribe",
            trace_id=0x111111000000000000000000000000AA,
            span_id=0xAAAAAAAAAAAAAAAA,
            start_ns=start_ns,
            end_ns=start_ns + 100_000_000,
        )

        exporter.export([span])

        day_dir = os.path.join(tmpdir, os.listdir(tmpdir)[0])
        filename = os.listdir(day_dir)[0]
        trace_id_hex = format(0x111111000000000000000000000000AA, "032x")
        expected_prefix = trace_id_hex[:6]
        assert filename.startswith(expected_prefix)
        assert "speech.transcribe" in filename


def test_file_span_exporter_appends_multiple_spans_to_same_file():
    with tempfile.TemporaryDirectory() as tmpdir:
        exporter = FileSpanExporter(base_dir=tmpdir)

        trace_id = 0xDEADBEEF00000000DEADBEEF00000001
        start_ns = 1_736_899_200_000_000_000

        span1 = _make_span("interview.turn", trace_id, 0x1, start_ns, start_ns + 1_000_000)
        span2 = _make_span("interview.turn", trace_id, 0x2, start_ns + 100_000_000, start_ns + 200_000_000)

        exporter.export([span1])
        exporter.export([span2])

        day_dir = os.path.join(tmpdir, os.listdir(tmpdir)[0])
        files = os.listdir(day_dir)
        # Both spans share trace_id_short + span_name, so same file
        assert len(files) == 1

        with open(os.path.join(day_dir, files[0])) as f:
            lines = [l.strip() for l in f.readlines() if l.strip()]
        assert len(lines) == 2


def test_file_span_exporter_handles_bad_span_gracefully():
    with tempfile.TemporaryDirectory() as tmpdir:
        exporter = FileSpanExporter(base_dir=tmpdir)
        bad_span = MagicMock(spec=ReadableSpan)
        bad_span.get_span_context.return_value = None

        # Should not raise
        result = exporter.export([bad_span])
        assert result == SpanExportResult.SUCCESS


# ---------------------------------------------------------------------------
# ACTION_LABELS
# ---------------------------------------------------------------------------


def test_action_labels_covers_expected_span_names():
    expected = {
        "interview.turn",
        "speech.transcribe",
        "llm.interviewer",
        "speech.tts",
        "interview.evaluate",
        "coach.analyze",
        "db.save_message",
    }
    assert expected.issubset(set(ACTION_LABELS.keys()))


def test_action_labels_values_are_non_empty_strings():
    for key, value in ACTION_LABELS.items():
        assert isinstance(value, str) and len(value) > 0, f"Label for '{key}' is empty"


# ---------------------------------------------------------------------------
# get_tracer
# ---------------------------------------------------------------------------


def test_get_tracer_returns_tracer():
    tracer = get_tracer("test.module")
    assert tracer is not None
    assert isinstance(tracer, trace.Tracer)


# ---------------------------------------------------------------------------
# /api/traces/recent route
# ---------------------------------------------------------------------------


@pytest_asyncio.fixture
async def app_with_traces(test_database, tmp_path):
    """App fixture with a temporary data_dir containing trace files."""
    settings = Settings(
        anthropic_api_key="test",
        openai_api_key="test",
        database_url=test_database,
        data_dir=tmp_path,
        _env_file=None,
    )
    application = create_app(database_url=test_database, settings=settings)
    async with LifespanManager(application) as manager:
        yield manager.app, tmp_path


@pytest_asyncio.fixture
async def traces_client(app_with_traces):
    app, data_dir = app_with_traces
    transport = ASGITransport(app=app)
    async with AsyncClient(transport=transport, base_url="http://test") as c:
        yield c, data_dir


@pytest.mark.asyncio
async def test_recent_traces_empty_when_no_files(traces_client):
    client, _ = traces_client
    resp = await client.get("/api/traces/recent")
    assert resp.status_code == 200
    assert resp.json() == []


@pytest.mark.asyncio
async def test_recent_traces_returns_list(traces_client):
    client, data_dir = traces_client

    # Write a trace file directly
    day_dir = data_dir / "traces" / "2025-01-15"
    day_dir.mkdir(parents=True)
    record = {
        "trace_id": "abcdef" + "0" * 26,
        "trace_id_short": "abcdef",
        "span_id": "0" * 16,
        "name": "interview.turn",
        "start_time": "2025-01-15T00:00:00+00:00",
        "duration_ms": 120.5,
        "status": "OK",
        "attributes": {},
    }
    trace_file = day_dir / "abcdef_interview.turn.jsonl"
    trace_file.write_text(json.dumps(record) + "\n")

    resp = await client.get("/api/traces/recent")
    assert resp.status_code == 200
    data = resp.json()
    assert isinstance(data, list)
    assert len(data) == 1
    assert data[0]["name"] == "interview.turn"
    assert data[0]["action_label"] == ACTION_LABELS["interview.turn"]


@pytest.mark.asyncio
async def test_recent_traces_limit(traces_client):
    client, data_dir = traces_client

    day_dir = data_dir / "traces" / "2025-01-15"
    day_dir.mkdir(parents=True, exist_ok=True)

    # Write 10 records across two files
    for i in range(5):
        tid = f"{i:06x}"
        record = {
            "trace_id": tid + "0" * 26,
            "trace_id_short": tid,
            "span_id": "0" * 16,
            "name": "speech.tts",
            "start_time": "2025-01-15T00:00:00+00:00",
            "duration_ms": 10.0,
            "status": "OK",
            "attributes": {},
        }
        f = day_dir / f"{tid}_speech.tts.jsonl"
        with open(f, "a") as fh:
            for _ in range(2):
                fh.write(json.dumps(record) + "\n")

    resp = await client.get("/api/traces/recent?limit=3")
    assert resp.status_code == 200
    data = resp.json()
    assert len(data) <= 3


@pytest.mark.asyncio
async def test_recent_traces_action_label_fallback(traces_client):
    client, data_dir = traces_client

    day_dir = data_dir / "traces" / "2025-01-15"
    day_dir.mkdir(parents=True, exist_ok=True)

    record = {
        "trace_id": "aaaaaa" + "0" * 26,
        "trace_id_short": "aaaaaa",
        "span_id": "0" * 16,
        "name": "unknown.span",
        "start_time": "2025-01-15T00:00:00+00:00",
        "duration_ms": 5.0,
        "status": "OK",
        "attributes": {},
    }
    (day_dir / "aaaaaa_unknown.span.jsonl").write_text(json.dumps(record) + "\n")

    resp = await client.get("/api/traces/recent")
    assert resp.status_code == 200
    data = resp.json()
    # Fallback: action_label == span name
    entry = next(e for e in data if e["name"] == "unknown.span")
    assert entry["action_label"] == "unknown.span"
