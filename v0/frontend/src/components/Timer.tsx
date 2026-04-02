interface TimerProps {
  elapsed: number;
  total: number;
}

function fmt(sec: number): string {
  const m = Math.floor(sec / 60);
  const s = sec % 60;
  return `${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
}

export default function Timer({ elapsed, total }: TimerProps) {
  const remaining = total - elapsed;
  let colorClass = "timer-normal";
  if (remaining <= 0) {
    colorClass = "timer-overtime";
  } else if (remaining <= 300) {
    colorClass = "timer-warning";
  }

  return (
    <span className={`timer ${colorClass}`}>
      {fmt(elapsed)} / {fmt(total)}
    </span>
  );
}
