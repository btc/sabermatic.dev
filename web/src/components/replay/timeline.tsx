interface TimelineProps {
  currentTime: number;
  duration: number;
  onSeek: (time: number) => void;
}

export function Timeline({ currentTime, duration, onSeek }: TimelineProps) {
  const pct = duration > 0 ? (currentTime / duration) * 100 : 0;

  function handleClick(e: React.MouseEvent<HTMLDivElement>) {
    const rect = e.currentTarget.getBoundingClientRect();
    const x = (e.clientX - rect.left) / rect.width;
    onSeek(x * duration);
  }

  return (
    <div
      className="h-1.5 w-full cursor-pointer rounded-full bg-muted"
      onClick={handleClick}
      role="slider"
      aria-label="Replay timeline"
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
