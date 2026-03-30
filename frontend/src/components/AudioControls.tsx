interface AudioControlsProps {
  isRecording: boolean;
  micReady: boolean;
  analyserData: Uint8Array | null;
  pendingSegments: number;
  pendingDuration: number;
}

export default function AudioControls({
  isRecording,
  micReady,
  analyserData,
  pendingSegments,
  pendingDuration,
}: AudioControlsProps) {
  if (isRecording) {
    return (
      <div className="audio-controls audio-controls-recording">
        <span className="rec-dot" />
        <span className="rec-label">Speak now</span>
        <div className="waveform">
          {analyserData &&
            Array.from(analyserData)
              .slice(0, 32)
              .map((v, i) => {
                const height = Math.max(4, ((v - 128) / 128) * 24 + 12);
                return (
                  <div
                    key={i}
                    className="waveform-bar"
                    style={{ height: `${height}px` }}
                  />
                );
              })}
        </div>
      </div>
    );
  }

  if (pendingSegments > 0) {
    const durationStr = pendingDuration.toFixed(1);
    return (
      <div className="audio-controls audio-controls-pending">
        <span className="pending-dot" />
        <span className="pending-label">
          {pendingSegments} {pendingSegments === 1 ? "segment" : "segments"} ({durationStr}s)
          {" "}&mdash; SPACE for more | ENTER to submit | ESC to discard
        </span>
      </div>
    );
  }

  return (
    <div className="audio-controls">
      <span className="audio-hint">
        {micReady ? "Hold SPACE to talk, ENTER to submit" : "Mic not available — use text input"}
      </span>
    </div>
  );
}
