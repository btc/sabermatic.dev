import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useWebSocket } from "../hooks/useWebSocket";
import { useAudio } from "../hooks/useAudio";
import { useTimer } from "../hooks/useTimer";
import ChatMessage from "../components/ChatMessage";
import Timer from "../components/Timer";
import AudioControls from "../components/AudioControls";
import TextInput from "../components/TextInput";
import TraceWidget from "../components/TraceWidget";
import type { WSServerMessage } from "../types";

interface ChatEntry {
  role: "interviewer" | "candidate";
  content: string;
  isStreaming: boolean;
}

const TIMER_TOTAL = 45 * 60; // 45 minutes default

export default function Interview() {
  const { questionId } = useParams<{ questionId: string }>();
  const navigate = useNavigate();

  const [messages, setMessages] = useState<ChatEntry[]>([]);
  const [processing, setProcessing] = useState(false);
  const [disconnected, setDisconnected] = useState(false);
  const [questionTitle, setQuestionTitle] = useState("Interview");
  const [started, setStarted] = useState(false);

  const chatEndRef = useRef<HTMLDivElement>(null);
  const spaceDownRef = useRef(false);

  const {
    isRecording,
    analyserData,
    startRecording,
    stopRecording,
    playAudioChunk,
    stopPlayback,
  } = useAudio();

  const { seconds, start: startTimer } = useTimer();

  // ---------- WebSocket message handler ----------

  const handleMessage = useCallback(
    (msg: WSServerMessage) => {
      switch (msg.type) {
        case "interviewer_text":
          setMessages((prev) => {
            // If last message is a streaming interviewer message, update it
            const last = prev[prev.length - 1];
            if (last && last.role === "interviewer" && last.isStreaming) {
              const updated = [...prev];
              updated[updated.length - 1] = {
                ...last,
                content: last.content + msg.content,
              };
              return updated;
            }
            // Otherwise start a new interviewer message
            return [
              ...prev,
              { role: "interviewer", content: msg.content, isStreaming: true },
            ];
          });
          break;

        case "interviewer_audio":
          playAudioChunk(msg.data);
          break;

        case "interviewer_done":
          setMessages((prev) => {
            const last = prev[prev.length - 1];
            if (last && last.role === "interviewer" && last.isStreaming) {
              const updated = [...prev];
              updated[updated.length - 1] = { ...last, isStreaming: false };
              return updated;
            }
            return prev;
          });
          break;

        case "transcription":
          setMessages((prev) => [
            ...prev,
            { role: "candidate", content: msg.text, isStreaming: false },
          ]);
          break;

        case "state":
          setProcessing(msg.state === "processing");
          break;

        case "session_ended":
          navigate(`/results/${msg.session_id}`);
          break;

        case "error":
          setMessages((prev) => [
            ...prev,
            {
              role: "interviewer",
              content: `Error: ${msg.message}`,
              isStreaming: false,
            },
          ]);
          break;
      }
    },
    [navigate, playAudioChunk]
  );

  const { connect, send, disconnect } = useWebSocket(handleMessage);

  // ---------- Connect on mount ----------
  // Connect WS on mount, but don't send "start" until user clicks Begin
  const initRef = useRef(false);

  useEffect(() => {
    if (initRef.current) return;
    initRef.current = true;

    connect().catch(() => setDisconnected(true));

    return () => {
      disconnect();
    };
  }, [connect, disconnect]);

  async function handleBegin() {
    // Request mic permission upfront (user gesture unlocks both mic and audio)
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      stream.getTracks().forEach((t) => t.stop()); // release immediately
    } catch {
      // User denied mic — they can still use text input
    }

    setStarted(true);
    send({
      type: "start",
      question_id: Number(questionId),
    });
    startTimer();
  }

  // ---------- Fetch question title ----------

  useEffect(() => {
    async function fetchTitle() {
      try {
        const res = await fetch(`/api/questions/${questionId}`);
        if (res.ok) {
          const data = await res.json();
          setQuestionTitle(data.title);
        }
      } catch {
        // Ignore — keep default title
      }
    }
    fetchTitle();
  }, [questionId]);

  // ---------- Auto-scroll ----------

  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // ---------- Push-to-talk (spacebar) ----------

  useEffect(() => {
    async function handleKeyDown(e: KeyboardEvent) {
      if (
        e.code === "Space" &&
        !e.repeat &&
        !spaceDownRef.current &&
        !(e.target instanceof HTMLInputElement) &&
        !(e.target instanceof HTMLTextAreaElement)
      ) {
        e.preventDefault();
        spaceDownRef.current = true;
        stopPlayback();
        try {
          await startRecording();
        } catch {
          spaceDownRef.current = false;
        }
      }
    }

    async function handleKeyUp(e: KeyboardEvent) {
      if (e.code === "Space" && spaceDownRef.current) {
        e.preventDefault();
        spaceDownRef.current = false;
        try {
          const audioData = await stopRecording();
          if (audioData) {
            send({ type: "end_turn", audio_data: audioData });
          }
        } catch {
          // Ignore recording errors
        }
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    window.addEventListener("keyup", handleKeyUp);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("keyup", handleKeyUp);
    };
  }, [startRecording, stopRecording, stopPlayback, send]);

  // ---------- Text submit ----------

  function handleTextSubmit(text: string) {
    send({ type: "text_input", text });
    setMessages((prev) => [
      ...prev,
      { role: "candidate", content: text, isStreaming: false },
    ]);
  }

  // ---------- End session ----------

  function handleEndSession() {
    send({ type: "end_session" });
  }

  // ---------- Render ----------

  if (!started) {
    return (
      <div className="interview-page">
        <div className="interview-begin">
          <h2>{questionTitle}</h2>
          <p className="interview-begin-hint">
            The interviewer will speak to you. Use spacebar to talk back, or type below.
          </p>
          <button className="interview-begin-btn" onClick={handleBegin}>
            Begin Interview
          </button>
        </div>
        <TraceWidget />
      </div>
    );
  }

  return (
    <div className="interview-page">
      {/* Header */}
      <header className="interview-header">
        <h2 className="interview-title">{questionTitle}</h2>
        <Timer elapsed={seconds} total={TIMER_TOTAL} />
        <button className="interview-end-btn" onClick={handleEndSession}>
          End Session
        </button>
      </header>

      {/* Chat log */}
      <div className="interview-chat">
        <div className="interview-chat-inner">
          {messages.map((msg, i) => (
            <ChatMessage
              key={i}
              role={msg.role}
              content={msg.content}
              isStreaming={msg.isStreaming}
            />
          ))}
          {processing && (
            <div className="chat-row chat-row-left">
              <div className="chat-avatar chat-avatar-interviewer">I</div>
              <div className="chat-bubble chat-bubble-interviewer chat-thinking">
                Thinking...
              </div>
            </div>
          )}
          {disconnected && (
            <div className="chat-system">Connection lost</div>
          )}
          <div ref={chatEndRef} />
        </div>
      </div>

      {/* Bottom area */}
      <div className="interview-bottom">
        <AudioControls isRecording={isRecording} analyserData={analyserData} />
        <TextInput
          onSubmit={handleTextSubmit}
          disabled={processing}
          placeholder="Type instead of speaking..."
        />
      </div>
      <TraceWidget />
    </div>
  );
}
