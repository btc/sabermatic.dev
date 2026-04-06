import { cn } from "@/lib/utils";

const BAR_COUNT = 24;

interface WaveformProps {
  data: Uint8Array;
  variant: "active" | "passive";
  className?: string;
}

export function Waveform({ data, variant, className }: WaveformProps) {
  const isActive = variant === "active";
  const barColor = isActive ? "bg-red-500" : "bg-muted-foreground";

  return (
    <div className={cn("flex items-center gap-[2px]", className)}>
      {Array.from({ length: BAR_COUNT }, (_, i) => {
        const value = i < data.length ? data[i]! : 128;
        const normalized = Math.abs(value - 128) / 128;
        const height = Math.max(2, normalized * 20);
        const opacity = isActive ? 0.6 + normalized * 0.4 : 0.4 + normalized * 0.4;
        return (
          <div
            key={i}
            className={cn("w-[2px] rounded-full", barColor)}
            style={{ height: `${height}px`, opacity }}
          />
        );
      })}
    </div>
  );
}
