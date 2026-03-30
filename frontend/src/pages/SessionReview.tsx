import { useState, useEffect, useRef } from "react";
import { useNavigate } from "react-router-dom";
import type { Message, Evaluation, MessageAnnotation, Question } from "../types";
import api from "../api/client";
import ScoreBar, { scoreColor } from "../components/ScoreBar";
import TraceWidget from "../components/TraceWidget";

interface SessionReviewProps {
  sessionId: number;
}

type EducatorState = "unknown" | "checking" | "exists" | "none" | "generating" | "error";

export default function SessionReview({ sessionId }: SessionReviewProps) {
  const navigate = useNavigate();
  const sid = sessionId;

  const [messages, setMessages] = useState<Message[]>([]);
  const [evaluation, setEvaluation] = useState<Evaluation | null>(null);
  const [annotations, setAnnotations] = useState<MessageAnnotation[]>([]);
  const [question, setQuestion] = useState<Question | null>(null);
  const [showInterviewerAnnotations, setShowInterviewerAnnotations] = useState(false);
  const [loading, setLoading] = useState(true);
  const [educatorState, setEducatorState] = useState<EducatorState>("checking");
  const pollRef = useRef<ReturnType<typeof setInterval> | null>(null);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const [session, msgs] = await Promise.all([
          api.sessions.get(sid),
          api.sessions.messages(sid),
        ]);
        if (cancelled) return;
        setMessages(msgs);

        try {
          const q = await api.questions.get(session.question_id);
          if (!cancelled) setQuestion(q);
        } catch {
          // question not found
        }

        try {
          const evalData = await api.sessions.evaluation(sid);
          if (cancelled) return;
          setEvaluation(evalData.evaluation);
          setAnnotations(evalData.annotations);
        } catch {
          // no evaluation
        }
      } catch {
        // error
      } finally {
        if (!cancelled) setLoading(false);
      }
    }

    load();
    return () => { cancelled = true; };
  }, [sid]);

  // Check educator content on mount
  useEffect(() => {
    let cancelled = false;
    setEducatorState("checking");

    api.sessions.educator(sid)
      .then(() => { if (!cancelled) setEducatorState("exists"); })
      .catch(() => { if (!cancelled) setEducatorState("none"); });

    return () => { cancelled = true; };
  }, [sid]);

  // Poll while generating
  useEffect(() => {
    if (educatorState !== "generating") {
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
      return;
    }

    pollRef.current = setInterval(async () => {
      try {
        await api.sessions.educator(sid);
        if (pollRef.current) clearInterval(pollRef.current);
        setEducatorState("exists");
      } catch {
        // still generating
      }
    }, 3000);

    return () => {
      if (pollRef.current) {
        clearInterval(pollRef.current);
        pollRef.current = null;
      }
    };
  }, [educatorState, sid]);

  async function handleGenerateEducator() {
    setEducatorState("generating");
    try {
      await api.sessions.triggerEducator(sid);
    } catch {
      setEducatorState("error");
    }
  }

  // Build annotation map: message_id -> annotations
  const annotationMap = new Map<number, MessageAnnotation[]>();
  for (const a of annotations) {
    const list = annotationMap.get(a.message_id) ?? [];
    list.push(a);
    annotationMap.set(a.message_id, list);
  }

  if (loading) {
    return (
      <div className="container">
        <p className="text-muted">Loading session...</p>
      </div>
    );
  }

  return (
    <div className="review-layout">
      {/* Left panel: Transcript */}
      <div className="review-transcript">
        <div className="review-transcript-header">
          <button className="btn-secondary" onClick={() => navigate(-1)}>
            Back
          </button>
          <h2>{question?.title ?? `Session #${sid}`}</h2>
          <label className="review-toggle">
            <input
              type="checkbox"
              checked={showInterviewerAnnotations}
              onChange={(e) => setShowInterviewerAnnotations(e.target.checked)}
            />
            Show interviewer annotations
          </label>
        </div>

        <div className="review-messages">
          {messages.map((msg) => {
            const msgAnnotations = annotationMap.get(msg.id) ?? [];
            const isInterviewer = msg.role === "interviewer";

            // Filter annotations based on toggle
            const visibleAnnotations = showInterviewerAnnotations
              ? msgAnnotations
              : msgAnnotations.filter(() => !isInterviewer);

            return (
              <div key={msg.id} className="review-message-group">
                <div
                  className={`review-message ${isInterviewer ? "review-message-interviewer" : "review-message-candidate"}`}
                >
                  <div className="review-message-header">
                    <span className="review-message-role">
                      {isInterviewer ? "Interviewer" : "You"}
                    </span>
                    <span className="review-message-time">
                      {new Date(msg.timestamp).toLocaleTimeString()}
                    </span>
                  </div>
                  <div className="review-message-content">{msg.content}</div>
                </div>
                {visibleAnnotations.map((a) => (
                  <div
                    key={a.id}
                    className={`review-annotation review-annotation-${a.annotation_type}`}
                  >
                    <span className="review-annotation-type">
                      {a.annotation_type.replace("_", " ")}
                    </span>
                    {a.content}
                  </div>
                ))}
              </div>
            );
          })}
        </div>
      </div>

      {/* Right panel: Evaluation sidebar */}
      <div className="review-sidebar">
        {evaluation ? (
          <>
            <div className="review-sidebar-score">
              <div
                className="review-score-big"
                style={{ color: scoreColor(evaluation.score_overall) }}
              >
                {evaluation.score_overall.toFixed(1)}
              </div>
              <div className="text-muted">Overall</div>
            </div>

            <ScoreBar label="Requirements" score={evaluation.score_requirements} />
            <ScoreBar label="High-Level" score={evaluation.score_highlevel} />
            <ScoreBar label="Deep Dive" score={evaluation.score_deepdive} />
            <ScoreBar label="Scalability" score={evaluation.score_scalability} />
            <ScoreBar label="Communication" score={evaluation.score_communication} />

            {evaluation.strengths.length > 0 && (
              <div className="review-section results-strengths">
                <h3>Strengths</h3>
                <ul>
                  {evaluation.strengths.map((s, i) => (
                    <li key={i}>{s}</li>
                  ))}
                </ul>
              </div>
            )}

            {evaluation.gaps.length > 0 && (
              <div className="review-section results-gaps">
                <h3>Gaps</h3>
                <ul>
                  {evaluation.gaps.map((g, i) => (
                    <li key={i}>{g}</li>
                  ))}
                </ul>
              </div>
            )}

            <div className="review-section results-advice">
              <h3>Advice</h3>
              <p>{evaluation.advice}</p>
            </div>

            {/* Educator button */}
            <div className="review-educator-row">
              {educatorState === "checking" && (
                <button className="btn-secondary" disabled>Checking...</button>
              )}
              {educatorState === "exists" && (
                <button
                  className="review-educator-btn"
                  onClick={() => navigate(`/sessions/${sid}/learn`)}
                >
                  View Deep Analysis
                </button>
              )}
              {educatorState === "none" && (
                <button
                  className="review-educator-btn"
                  onClick={handleGenerateEducator}
                >
                  Generate Deep Analysis (Opus)
                </button>
              )}
              {educatorState === "generating" && (
                <button className="review-educator-btn" disabled>
                  <span className="results-spinner" style={{ width: 14, height: 14, display: "inline-block", marginRight: 6 }} />
                  Generating...
                </button>
              )}
              {educatorState === "error" && (
                <button
                  className="review-educator-btn"
                  onClick={handleGenerateEducator}
                >
                  Retry Deep Analysis
                </button>
              )}
            </div>
          </>
        ) : (
          <p className="text-muted">No evaluation available.</p>
        )}
      </div>

      <TraceWidget />
    </div>
  );
}
