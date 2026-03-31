import { useState } from "react";
import type { Question } from "../types";

interface SessionConfigProps {
  question: Question;
  onStart: (timerSec: number, ttsEnabled: boolean) => void;
  onCancel: () => void;
}

const DURATION_PRESETS = [15, 30, 45, 60];
const DEFAULT_DURATION = 45;

export default function SessionConfig({ question, onStart, onCancel }: SessionConfigProps) {
  const [durationMin, setDurationMin] = useState(DEFAULT_DURATION);
  const [customInput, setCustomInput] = useState("");
  const [ttsEnabled, setTtsEnabled] = useState(true);

  function handlePreset(min: number) {
    setDurationMin(min);
    setCustomInput("");
  }

  function handleCustomChange(value: string) {
    setCustomInput(value);
    const parsed = parseInt(value, 10);
    if (!isNaN(parsed) && parsed > 0 && parsed <= 180) {
      setDurationMin(parsed);
    }
  }

  function handleStart() {
    onStart(durationMin * 60, ttsEnabled);
  }

  return (
    <div className="session-config card">
      <h2>{question.title}</h2>
      <span className={`tag ${question.difficulty === "hard" ? "badge-red" : "badge-orange"}`}>
        {question.difficulty}
      </span>

      <div className="session-config-section">
        <label className="session-config-label">Duration</label>
        <div className="session-config-durations">
          {DURATION_PRESETS.map((min) => (
            <button
              key={min}
              className={`session-config-duration-btn ${durationMin === min && customInput === "" ? "session-config-duration-btn-active" : ""}`}
              onClick={() => handlePreset(min)}
            >
              {min}m
            </button>
          ))}
          <input
            type="number"
            className="session-config-custom-input"
            placeholder="Custom"
            min={1}
            max={180}
            value={customInput}
            onChange={(e) => handleCustomChange(e.target.value)}
          />
        </div>
      </div>

      <div className="session-config-section">
        <label className="session-config-label">
          <input
            type="checkbox"
            checked={ttsEnabled}
            onChange={(e) => setTtsEnabled(e.target.checked)}
          />
          Voice responses (text-to-speech)
        </label>
      </div>

      <div className="session-config-actions">
        <button className="btn-primary" onClick={handleStart}>
          Start Interview
        </button>
        <button className="btn-secondary" onClick={onCancel}>
          Cancel
        </button>
      </div>
    </div>
  );
}
