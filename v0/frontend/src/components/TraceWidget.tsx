import { useState, useEffect, useRef, useCallback } from "react";
import type { TraceEntry } from "../types";
import api from "../api/client";

function timeAgo(isoStr: string): string {
  const sec = Math.floor((Date.now() - new Date(isoStr).getTime()) / 1000);
  if (sec < 5) return "just now";
  if (sec < 60) return `${sec}s ago`;
  const min = Math.floor(sec / 60);
  if (min < 60) return `${min}m ago`;
  const hr = Math.floor(min / 60);
  return `${hr}h ago`;
}

export default function TraceWidget() {
  const [traces, setTraces] = useState<TraceEntry[]>([]);
  const [expanded, setExpanded] = useState(false);
  const [expandedTrace, setExpandedTrace] = useState<string | null>(null);
  const [copiedId, setCopiedId] = useState<string | null>(null);
  const intervalRef = useRef<ReturnType<typeof setInterval> | null>(null);

  const fetchTraces = useCallback(async () => {
    try {
      const data = await api.traces.recent(20);
      setTraces(data);
    } catch {
      // silent — trace widget is non-critical
    }
  }, []);

  useEffect(() => {
    fetchTraces();
    intervalRef.current = setInterval(fetchTraces, 5000);
    return () => {
      if (intervalRef.current) clearInterval(intervalRef.current);
    };
  }, [fetchTraces]);

  const copyTraceId = async (traceId: string) => {
    try {
      await navigator.clipboard.writeText(traceId);
      setCopiedId(traceId);
      setTimeout(() => setCopiedId(null), 1500);
    } catch {
      // clipboard not available
    }
  };

  const latest = traces[0];

  if (traces.length === 0) return null;

  return (
    <div className={`trace-widget ${expanded ? "trace-widget-expanded" : ""}`}>
      {!expanded ? (
        <div className="trace-collapsed" onClick={() => setExpanded(true)}>
          {latest && (
            <>
              <span className="trace-id-short">{latest.trace_id_short}</span>
              <span className="trace-action">{latest.action_label}</span>
              <span className="trace-ago">{timeAgo(latest.start_time)}</span>
              <button
                className="trace-copy-btn"
                onClick={(e) => {
                  e.stopPropagation();
                  copyTraceId(latest.trace_id);
                }}
                title="Copy trace ID"
              >
                {copiedId === latest.trace_id ? "ok" : "cp"}
              </button>
            </>
          )}
        </div>
      ) : (
        <div className="trace-expanded-panel">
          <div className="trace-expanded-header">
            <span>Recent Traces</span>
            <button className="trace-close-btn" onClick={() => setExpanded(false)}>
              x
            </button>
          </div>
          <div className="trace-list">
            {traces.map((t) => (
              <div key={t.span_id} className="trace-item">
                <div
                  className="trace-item-header"
                  onClick={() =>
                    setExpandedTrace(expandedTrace === t.span_id ? null : t.span_id)
                  }
                >
                  <span className="trace-id-short">{t.trace_id_short}</span>
                  <span className="trace-action">{t.action_label}</span>
                  <span className="trace-ago">{timeAgo(t.start_time)}</span>
                  {t.duration_ms != null && (
                    <span className="trace-duration">{(t.duration_ms / 1000).toFixed(1)}s</span>
                  )}
                  <span
                    className={`trace-status ${t.status === "OK" ? "trace-status-ok" : "trace-status-err"}`}
                  >
                    {t.status ?? "?"}
                  </span>
                  <button
                    className="trace-copy-btn"
                    onClick={(e) => {
                      e.stopPropagation();
                      copyTraceId(t.trace_id);
                    }}
                  >
                    {copiedId === t.trace_id ? "ok" : "cp"}
                  </button>
                </div>
                {expandedTrace === t.span_id && (
                  <div className="trace-item-detail">
                    <div>Name: {t.name}</div>
                    <div>Start: {new Date(t.start_time).toLocaleTimeString()}</div>
                    {Object.keys(t.attributes).length > 0 && (
                      <pre className="trace-attrs">
                        {JSON.stringify(t.attributes, null, 2)}
                      </pre>
                    )}
                  </div>
                )}
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
