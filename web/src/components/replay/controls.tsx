import type { ReplayState } from "./engine";
import { Timeline } from "./timeline";

function formatTime(seconds: number): string {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return `${m}:${s.toString().padStart(2, "0")}`;
}

interface ReplayControlsProps {
  state: ReplayState;
  onPlay: () => void;
  onPause: () => void;
  onSeek: (time: number) => void;
  onSetSpeed: (speed: number) => void;
}

const SPEEDS = [1, 1.5, 2] as const;

export function ReplayControls({
  state,
  onPlay,
  onPause,
  onSeek,
  onSetSpeed,
}: ReplayControlsProps) {
  return (
    <div className="flex flex-col gap-2 rounded-lg border border-border bg-card p-3">
      <Timeline
        currentTime={state.currentTime}
        duration={state.duration}
        onSeek={onSeek}
      />
      <div className="flex items-center justify-between text-xs text-muted-foreground">
        <div className="flex items-center gap-2">
          <button
            className="rounded px-2 py-1 hover:bg-muted"
            onClick={state.isPlaying ? onPause : onPlay}
            aria-label={state.isPlaying ? "Pause" : "Play"}
            type="button"
          >
            {state.isPlaying ? "\u23F8" : "\u25B6"}
          </button>
          <span>
            {formatTime(state.currentTime)} / {formatTime(state.duration)}
          </span>
        </div>
        <div className="flex items-center gap-1">
          {SPEEDS.map((s) => (
            <button
              key={s}
              className={`rounded px-2 py-1 text-xs ${
                state.speed === s
                  ? "bg-muted font-medium text-foreground"
                  : "hover:bg-muted"
              }`}
              onClick={() => onSetSpeed(s)}
              aria-label={`${s}x speed`}
              aria-pressed={state.speed === s}
              type="button"
            >
              {s}x
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
