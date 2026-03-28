interface ChatMessageProps {
  role: "interviewer" | "candidate";
  content: string;
  isStreaming?: boolean;
}

export default function ChatMessage({ role, content, isStreaming }: ChatMessageProps) {
  const isInterviewer = role === "interviewer";

  return (
    <div className={`chat-row ${isInterviewer ? "chat-row-left" : "chat-row-right"}`}>
      <div className={`chat-avatar ${isInterviewer ? "chat-avatar-interviewer" : "chat-avatar-candidate"}`}>
        {isInterviewer ? "I" : "Y"}
      </div>
      <div className={`chat-bubble ${isInterviewer ? "chat-bubble-interviewer" : "chat-bubble-candidate"}`}>
        {content}
        {isStreaming && <span className="chat-cursor" />}
      </div>
    </div>
  );
}
