interface AudioControlsProps {
  isRecording: boolean;
  analyserData: Uint8Array | null;
}

export default function AudioControls({ isRecording, analyserData }: AudioControlsProps) {
  if (!isRecording) {
    return (
      <div className="audio-controls">
        <span className="audio-hint">Hold SPACE to talk</span>
      </div>
    );
  }

  return (
    <div className="audio-controls audio-controls-recording">
      <span className="rec-dot" />
      <span className="rec-label">REC</span>
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
