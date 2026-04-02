import { useCallback, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useWebSocket } from "../hooks/useWebSocket";
import { useAudio } from "../hooks/useAudio";
import { useTimer } from "../hooks/useTimer";
import ChatMessage from "../components/ChatMessage";
import Timer from "../components/Timer";
import AudioControls from "../components/AudioControls";
import TextInput from "../components/TextInput";
import TraceWidget from "../components/TraceWidget";
import { setSessionTraceId, startSpan, recordEvent } from "../tracer";
import type { Session, WSServerMessage } from "../types";

interface ChatEntry {
  role: "interviewer" | "candidate" | "system";
  content: string;
  isStreaming: boolean;
}


interface InterviewProps {
  sessionId: number;
  session: Session;
}

export default function Interview({ sessionId, session }: InterviewProps) {
  const navigate = useNavigate();

  const [messages, setMessages] = useState<ChatEntry[]>([]);
  const [serverState, setServerState] = useState("");
  const [questionTitle, setQuestionTitle] = useState("Interview");
  const [micNeedsGesture, setMicNeedsGesture] = useState(false);

  const chatEndRef = useRef<HTMLDivElement>(null);
  const spaceDownRef = useRef(false);
  const hasConnectedRef = useRef(false);

  const {
    isRecording,
    isPlaying,
    micReady,
    analyserData,
    pendingSegments,
    pendingDuration,
    initMic,
    releaseMic,
    startRecording,
    stopRecording,
    submitRecording,
    discardRecording,
    playAudioChunk,
    flushPlayback,
    stopPlayback,
  } = useAudio();

  const { seconds, setStartedAt } = useTimer();

  // ---------- WebSocket message handler ----------

  const handleMessage = useCallback(
    (msg: WSServerMessage) => {
      switch (msg.type) {
        case "session_loaded":
          hasConnectedRef.current = true;
          setStartedAt(msg.started_at);
          setMessages([]);  // Clear on reconnect — history replay follows
          break;

        case "message_history":
          setMessages((prev) => [
            ...prev,
            {
              role: msg.role as "interviewer" | "candidate",
              content: msg.content,
              isStreaming: false,
            },
          ]);
          break;

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
          flushPlayback(); // Play the accumulated TTS audio
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
          setServerState(msg.state);
          break;

        case "session_ended":
          // Re-fetch session status to trigger SessionPage to re-render
          window.location.reload();
          break;

        case "tts_error":
          setMessages((prev) => [
            ...prev,
            {
              role: "system",
              content: msg.message,
              isStreaming: false,
            },
          ]);
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
    [playAudioChunk, flushPlayback, setStartedAt]
  );

  const { connect, send, disconnect, connectionStatus } = useWebSocket(handleMessage);

  // ---------- Connect on mount ----------
  const initRef = useRef(false);

  useEffect(() => {
    if (initRef.current) return;
    initRef.current = true;

    setSessionTraceId();
    recordEvent("user.begin_interview", { session_id: sessionId });

    // Connect WS with sessionId
    connect(sessionId).catch(() => {});

    // Try to init mic immediately (may fail without user gesture)
    initMic().catch(() => {
      setMicNeedsGesture(true);
    });

    return () => {
      disconnect();
      releaseMic();
    };
  }, [sessionId, connect, disconnect, releaseMic, initMic]);

  // ---------- Fetch question title ----------

  useEffect(() => {
    async function fetchTitle() {
      try {
        const res = await fetch(`/api/questions/${session.question_id}`);
        if (res.ok) {
          const data = await res.json();
          setQuestionTitle(data.title);
        }
      } catch {
        // Ignore — keep default title
      }
    }
    fetchTitle();
  }, [session.question_id]);

  // ---------- Auto-scroll ----------

  useEffect(() => {
    chatEndRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  // ---------- Push-to-talk (spacebar) ----------

  const recordingSpanRef = useRef<ReturnType<typeof startSpan> | null>(null);

  useEffect(() => {
    function handleKeyDown(e: KeyboardEvent) {
      // Spacebar — start recording (ignore if in text input)
      if (
        e.code === "Space" &&
        !e.repeat &&
        !spaceDownRef.current &&
        !(e.target instanceof HTMLInputElement) &&
        !(e.target instanceof HTMLTextAreaElement)
      ) {
        e.preventDefault();
        spaceDownRef.current = true;
        recordingSpanRef.current = startSpan("user.spacebar_press");
        stopPlayback();
        startRecording();
      }

      // Enter — submit all buffered segments (only when not in text input)
      if (
        e.code === "Enter" &&
        !e.repeat &&
        !(e.target instanceof HTMLInputElement) &&
        !(e.target instanceof HTMLTextAreaElement)
      ) {
        // Let the async handler run — we just need to prevent default here
        // if there are pending segments to avoid any form submission side effects
      }

      // Escape — discard all buffered segments
      if (e.code === "Escape") {
        discardRecording();
      }
    }

    async function handleKeyUp(e: KeyboardEvent) {
      // Spacebar up — stop recording, buffer the segment (do not send)
      if (e.code === "Space" && spaceDownRef.current) {
        e.preventDefault();
        spaceDownRef.current = false;

        const recSpan = recordingSpanRef.current;
        recordingSpanRef.current = null;

        try {
          await stopRecording();
          recSpan?.end({ result: "segment_buffered" });
        } catch (err) {
          recSpan?.end({ error: String(err) });
        }
      }

      // Enter up — submit buffered segments (only when not in text input)
      if (
        e.code === "Enter" &&
        !(e.target instanceof HTMLInputElement) &&
        !(e.target instanceof HTMLTextAreaElement)
      ) {
        if (pendingSegments > 0) {
          try {
            const encodeSpan = startSpan("audio.encode");
            const audioData = await submitRecording();
            encodeSpan.end({ audio_length: audioData?.length ?? 0 });

            if (audioData) {
              const sendSpan = startSpan("ws.send", { msg_type: "end_turn" });
              send({ type: "end_turn", audio_data: audioData });
              sendSpan.end();
            }
          } catch (err) {
            recordEvent("audio.submit_error", { error: String(err) });
          }
        }
      }
    }

    window.addEventListener("keydown", handleKeyDown);
    window.addEventListener("keyup", handleKeyUp);
    return () => {
      window.removeEventListener("keydown", handleKeyDown);
      window.removeEventListener("keyup", handleKeyUp);
    };
  }, [startRecording, stopRecording, submitRecording, discardRecording, stopPlayback, send, pendingSegments]);

  // ---------- Text submit ----------

  function handleTextSubmit(text: string) {
    recordEvent("user.text_submit", { text_length: text.length });
    send({ type: "text_input", text });
    setMessages((prev) => [
      ...prev,
      { role: "candidate", content: text, isStreaming: false },
    ]);
  }

  // ---------- End session ----------

  function handleEndSession() {
    recordEvent("user.end_session");
    send({ type: "end_session" });
  }

  function handleCancel() {
    recordEvent("user.cancel_session");
    disconnect();
    navigate("/");
  }

  async function handleEnableMic() {
    try {
      await initMic();
      setMicNeedsGesture(false);
    } catch {
      // Still can't get mic
    }
  }

  // ---------- Render ----------

  return (
    <div className="interview-page">
      {/* Header */}
      <header className="interview-header">
        <h2 className="interview-title">{questionTitle}</h2>
        <Timer elapsed={seconds} total={session.timer_setting_sec} />
        <button className="interview-cancel-btn" onClick={handleCancel}>
          Cancel
        </button>
        <button className="interview-end-btn" onClick={handleEndSession}>
          End Session
        </button>
      </header>

      {/* Chat log */}
      <div className="interview-chat">
        <div className="interview-chat-inner">
          <div className="interview-chat-spacer" />
          {messages.map((msg, i) =>
            msg.role === "system" ? (
              <div key={i} className="chat-system">{msg.content}</div>
            ) : (
              <ChatMessage
                key={i}
                role={msg.role}
                content={msg.content}
                isStreaming={msg.isStreaming}
              />
            )
          )}
          {serverState === "processing" && (
            <div className="chat-row chat-row-right">
              <div className="chat-bubble chat-bubble-candidate chat-thinking">
                Transcribing...
              </div>
              <div className="chat-avatar chat-avatar-candidate">Y</div>
            </div>
          )}
          {serverState === "interviewer_speaking" && !messages.some(m => m.isStreaming) && (
            <div className="chat-row chat-row-left">
              <div className="chat-avatar chat-avatar-interviewer">I</div>
              <div className="chat-bubble chat-bubble-interviewer chat-thinking">
                Thinking...
              </div>
            </div>
          )}
          {isPlaying && (
            <div className="chat-row chat-row-left">
              <div className="chat-avatar chat-avatar-interviewer">I</div>
              <div className="chat-bubble chat-bubble-interviewer chat-speaking">
                &#9835; Speaking...
              </div>
            </div>
          )}
          {hasConnectedRef.current && connectionStatus === "disconnected" && (
            <div className="chat-system">Connection lost</div>
          )}
          {hasConnectedRef.current && connectionStatus === "reconnecting" && (
            <div className="chat-system">Reconnecting...</div>
          )}
          <div ref={chatEndRef} />
        </div>
      </div>

      {/* Bottom area */}
      <div className="interview-bottom">
        <AudioControls
          isRecording={isRecording}
          micReady={micReady}
          analyserData={analyserData}
          pendingSegments={pendingSegments}
          pendingDuration={pendingDuration}
        />
        {micNeedsGesture && (
          <button className="btn-secondary" onClick={handleEnableMic} style={{ marginBottom: "0.5rem" }}>
            Enable Mic
          </button>
        )}
        <TextInput
          onSubmit={handleTextSubmit}
          disabled={serverState === "processing" || serverState === "interviewer_speaking"}
          placeholder="Type instead of speaking..."
        />
      </div>
      <TraceWidget />
    </div>
  );
}
