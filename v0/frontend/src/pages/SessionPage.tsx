import { useState, useEffect } from "react";
import { useParams } from "react-router-dom";
import type { Session } from "../types";
import api from "../api/client";
import Interview from "./Interview";
import Results from "./Results";
import SessionReview from "./SessionReview";

export default function SessionPage() {
  const { sessionId } = useParams<{ sessionId: string }>();
  const [session, setSession] = useState<Session | null>(null);

  useEffect(() => {
    api.sessions.get(Number(sessionId)).then(setSession);
  }, [sessionId]);

  if (!session) return <div className="loading">Loading...</div>;

  switch (session.status) {
    case "active":
      return <Interview sessionId={session.id} session={session} />;
    case "completed":
    case "evaluating":
      return <Results sessionId={session.id} />;
    case "evaluation_failed":
    case "reviewed":
      return <SessionReview sessionId={session.id} />;
    default:
      return <div>Unknown session status</div>;
  }
}
