interface AudioControlsProps {
  isRecording: boolean;
  isPreparing: boolean;
  analyserData: Uint8Array | null;
}

export default function AudioControls({ isRecording, isPreparing, analyserData }: AudioControlsProps) {
  if (isPreparing) {
    return (
      <div className="audio-controls audio-controls-preparing">
        <span className="prep-dot" />
        <span className="prep-label">Activating mic...</span>
      </div>
    );
  }

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

  return (
    <div className="audio-controls">
      <span className="audio-hint">Hold SPACE to talk</span>
    </div>
  );
}
