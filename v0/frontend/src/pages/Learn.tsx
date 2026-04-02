import { useState, useEffect } from "react";
import { useParams, useNavigate } from "react-router-dom";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import api from "../api/client";

interface EducatorContent {
  evaluation_id: number;
  model_answer: string;
  gap_deepdives: string;
}

export default function Learn() {
  const { sessionId } = useParams<{ sessionId: string }>();
  const navigate = useNavigate();
  const [content, setContent] = useState<EducatorContent | null>(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    api.sessions.educator(Number(sessionId))
      .then((data) => { setContent(data); setLoading(false); })
      .catch(() => setLoading(false));
  }, [sessionId]);

  if (loading) return <div className="loading">Loading...</div>;
  if (!content) return <div className="container"><p className="text-muted">No educator content yet.</p></div>;

  return (
    <div className="learn-page">
      <header className="learn-header">
        <button className="btn-secondary" onClick={() => navigate(`/sessions/${sessionId}`)}>
          Back to Review
        </button>
        <h1>Deep Analysis</h1>
      </header>
      <div className="learn-body">
        <section className="learn-section">
          <h2>Model Answer</h2>
          <div className="learn-markdown">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{content.model_answer}</ReactMarkdown>
          </div>
        </section>
        <section className="learn-section">
          <h2>Gap Deep-Dives</h2>
          <div className="learn-markdown">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{content.gap_deepdives}</ReactMarkdown>
          </div>
        </section>
      </div>
    </div>
  );
}
