interface TimelineProps {
  currentTime: number;
  duration: number;
  onSeek: (time: number) => void;
}

const STEP_SEC = 5;

export function Timeline({ currentTime, duration, onSeek }: TimelineProps) {
  const pct = duration > 0 ? (currentTime / duration) * 100 : 0;

  function handleClick(e: React.MouseEvent<HTMLDivElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - rect.left) / rect.width;
    onSeek(x * duration);
  }

  function handleKeyDown(e: React.KeyboardEvent) {
    switch (e.key) {
      case "ArrowRight":
        e.preventDefault();
        onSeek(Math.min(currentTime + STEP_SEC, duration));
        break;
      case "ArrowLeft":
        e.preventDefault();
        onSeek(Math.max(currentTime - STEP_SEC, 0));
        break;
      case "Home":
        e.preventDefault();
        onSeek(0);
        break;
      case "End":
        e.preventDefault();
        onSeek(duration);
        break;
    }
  }

  return (
    <div
      className="h-1.5 w-full cursor-pointer rounded-full bg-muted"
      onClick={handleClick}
      onKeyDown={handleKeyDown}
      role="slider"
      tabIndex={0}
      aria-label="Replay timeline"
      aria-orientation="horizontal"
      aria-valuenow={Math.floor(currentTime)}
      aria-valuemin={0}
      aria-valuemax={Math.floor(duration)}
    >
      <div
        className="h-1.5 rounded-full bg-primary transition-[width] duration-100"
        style={{ width: `${pct}%` }}
      />
    </div>
  );
}
