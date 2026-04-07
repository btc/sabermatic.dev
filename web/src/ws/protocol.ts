// --- Client → Server ---

export type ClientMessage =
  | { type: "session_init"; last_seq: number | null; trace_context?: TraceContext }
  | { type: "end_turn"; content: string; input_method: "text"; trace_context?: TraceContext }
  | { type: "end_turn"; audio: string; input_method: "voice"; trace_context?: TraceContext }
  | { type: "end_session" }
  | { type: "cancel_session" }
  | { type: "ping" };

export interface TraceContext {
  traceparent: string;
}

// --- Server → Client ---

export type ServerMessage =
  | { type: "session_loaded"; session_id: string; question: { title: string; prompt: string }; duration: number; tts_enabled: boolean }
  | { type: "reconnect_state"; messages: ReconnectMessage[] }
  | { type: "state_change"; state: string }
  | { type: "interviewer_token"; token: string }
  | { type: "interviewer_done"; message_id: string }
  | { type: "tts_chunk"; data: string; message_id: string; seq: number }
  | { type: "tts_done"; message_id: string }
  | { type: "tts_error" }
  | { type: "audio_upload_failed" }
  | { type: "transcription_result"; text: string }
  | { type: "timer_warning"; minutes_remaining: number }
  | { type: "timer_overtime" }
  | { type: "ack"; action: "cancel_session" | "end_session" }
  | { type: "reconnect_please" }
  | { type: "error"; code: string; message: string }
  | { type: "pong" };

export interface ReconnectMessage {
  id: string;
  seq: number;
  role: "interviewer" | "candidate";
  content: string;
  input_method: string | null;
  audio_url: string | null;
  created_at: string;
}
