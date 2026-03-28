import { useState, useEffect, useRef } from "react";
import { useParams, useNavigate } from "react-router-dom";
import type { Session, Evaluation } from "../types";
import api from "../api/client";
import ScoreBar, { scoreColor } from "../components/ScoreBar";
import TraceWidget from "../components/TraceWidget";

export default function Results() {
  const { sessionId } = useParams<{ sessionId: string }>();
  const navigate = useNavigate();
  const [evaluation, setEvaluation] = useState<Evaluation | null>(null);
  const [session, setSession] = useState<Session | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [timedOut, setTimedOut] = useState(false);
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);
  const startRef = useRef(Date.now());

  const sid = Number(sessionId);

  useEffect(() => {
    let cancelled = false;

    async function tryFetchEval() {
      try {
        const s = await api.sessions.get(sid);
        if (cancelled) return;
        setSession(s);

        if (s.status === "evaluation_failed") {
          setError(s.status_detail ?? "Evaluation failed");
          return true; // stop polling
        }

        if (s.status === "reviewed") {
          const evalData = await api.sessions.evaluation(sid);
          if (cancelled) return;
          setEvaluation(evalData.evaluation);
          return true; // stop polling
        }

        // If completed but not evaluating, trigger evaluation
        if (s.status === "completed") {
          try {
            await api.evaluate.trigger(sid);
          } catch {
            // might already be evaluating
          }
        }

        return false; // keep polling
      } catch {
        return false;
      }
    }

    async function startPolling() {
      startRef.current = Date.now();
      const done = await tryFetchEval();
      if (cancelled || done) return;

      pollRef.current = setInterval(async () => {
        if (cancelled) return;
        const elapsed = Date.now() - startRef.current;
        if (elapsed > 60000) {
          if (pollRef.current) clearInterval(pollRef.current);
          if (!cancelled) setTimedOut(true);
          return;
        }
        const finished = await tryFetchEval();
        if (finished && pollRef.current) {
          clearInterval(pollRef.current);
        }
      }, 3000);
    }

    startPolling();

    return () => {
      cancelled = true;
      if (pollRef.current) clearInterval(pollRef.current);
    };
  }, [sid]);

  const handleRetry = async () => {
    setTimedOut(false);
    setError(null);
    try {
      await api.evaluate.trigger(sid);
    } catch {
      // ignore
    }
    // Restart polling
    startRef.current = Date.now();
    pollRef.current = setInterval(async () => {
      const elapsed = Date.now() - startRef.current;
      if (elapsed > 60000) {
        if (pollRef.current) clearInterval(pollRef.current);
        setTimedOut(true);
        return;
      }
      try {
        const s = await api.sessions.get(sid);
        setSession(s);
        if (s.status === "evaluation_failed") {
          setError(s.status_detail ?? "Evaluation failed");
          if (pollRef.current) clearInterval(pollRef.current);
          return;
        }
        if (s.status === "reviewed") {
          const evalData = await api.sessions.evaluation(sid);
          setEvaluation(evalData.evaluation);
          if (pollRef.current) clearInterval(pollRef.current);
        }
      } catch {
        // keep polling
      }
    }, 3000);
  };

  // Loading / Error states
  if (error) {
    return (
      <div className="container">
        <div className="card results-error">
          <h2>Evaluation Failed</h2>
          <p>{error}</p>
          <div className="results-actions">
            <button onClick={handleRetry}>Retry Evaluation</button>
            <button className="btn-secondary" onClick={() => navigate("/")}>
              Home
            </button>
          </div>
        </div>
        <TraceWidget />
      </div>
    );
  }

  if (timedOut) {
    return (
      <div className="container">
        <div className="card results-error">
          <h2>Evaluation Timed Out</h2>
          <p>The evaluation is taking longer than expected.</p>
          <div className="results-actions">
            <button onClick={handleRetry}>Retry?</button>
            <button className="btn-secondary" onClick={() => navigate("/")}>
              Home
            </button>
          </div>
        </div>
        <TraceWidget />
      </div>
    );
  }

  if (!evaluation) {
    return (
      <div className="container">
        <div className="card">
          <h2>Evaluating Your Session...</h2>
          <p className="text-muted">
            {session?.status === "evaluating"
              ? "The AI evaluator is reviewing your transcript..."
              : "Preparing evaluation..."}
          </p>
          <div className="results-spinner" />
        </div>
        <TraceWidget />
      </div>
    );
  }

  const overallColor = scoreColor(evaluation.score_overall);

  return (
    <div className="container">
      <div className="results-hero">
        <div className="results-score-big" style={{ color: overallColor }}>
          {evaluation.score_overall.toFixed(1)}
        </div>
        <div className="results-score-label">Overall Score</div>
      </div>

      <div className="card">
        <ScoreBar label="Requirements" score={evaluation.score_requirements} />
        <ScoreBar label="High-Level" score={evaluation.score_highlevel} />
        <ScoreBar label="Deep Dive" score={evaluation.score_deepdive} />
        <ScoreBar label="Scalability" score={evaluation.score_scalability} />
        <ScoreBar label="Communication" score={evaluation.score_communication} />
      </div>

      {evaluation.strengths.length > 0 && (
        <div className="results-section results-strengths">
          <h2>Strengths</h2>
          <ul>
            {evaluation.strengths.map((s, i) => (
              <li key={i}>{s}</li>
            ))}
          </ul>
        </div>
      )}

      {evaluation.gaps.length > 0 && (
        <div className="results-section results-gaps">
          <h2>Areas for Improvement</h2>
          <ul>
            {evaluation.gaps.map((g, i) => (
              <li key={i}>{g}</li>
            ))}
          </ul>
        </div>
      )}

      <div className="results-section results-advice">
        <h2>Advice</h2>
        <p>{evaluation.advice}</p>
      </div>

      <div className="results-actions">
        <button onClick={() => navigate(`/session/${sid}`)}>
          Review Transcript
        </button>
        <button className="btn-secondary" onClick={() => navigate("/")}>
          Home
        </button>
      </div>

      <TraceWidget />
    </div>
  );
}
