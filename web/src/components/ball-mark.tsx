import { cn } from "@/lib/utils";

interface BallMarkProps {
  /** Additional Tailwind classes applied to the inner <svg>.
   *  Use for hover animation (e.g. "group-hover:animate-spin") or
   *  per-surface color overrides. Do not pass transform utilities —
   *  they will conflict with animate-spin on the same element (both
   *  write to `transform`, and CSS animation takes precedence).
   *
   *  Two-node DOM by design: wrapper span owns baseline `translateY`,
   *  inner svg owns caller-supplied rotation. Keeping them on separate
   *  elements lets the transforms compose instead of clobbering.
   *
   *  Wrapper positioning is not externally overridable by design;
   *  if a surface needs a different baseline, prefer a new variant. */
  className?: string;
}

export function BallMark({ className }: BallMarkProps) {
  return (
    <span className="inline-block translate-y-[0.05em]">
      <svg
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden="true"
        focusable="false"
        viewBox="0 0 32 32"
        className={cn("h-[0.55em] w-[0.55em]", className)}
      >
        <g transform="rotate(-28 16 16)" strokeLinejoin="round" fill="none">
          <circle cx="16" cy="16" r="12" fill="#fef3c7" stroke="#92400e" strokeWidth="1.3" />
          <g stroke="#b45309" strokeWidth="1" strokeLinecap="butt">
            <path d="M 5.61 10 Q 14 17 5.61 22" />
            <path d="M 26.39 10 Q 14 17 26.39 22" />
          </g>
          <g stroke="#92400e" strokeWidth="0.38" strokeLinecap="round">
            <line x1="8.97" y1="12.20" x2="7.61" y2="13.24" />
            <line x1="9.88" y1="13.63" x2="8.38" y2="14.42" />
            <line x1="10.46" y1="15.06" x2="8.82" y2="15.50" />
            <line x1="10.65" y1="16.50" x2="8.95" y2="16.50" />
            <line x1="10.46" y1="17.92" x2="8.82" y2="17.44" />
            <line x1="9.86" y1="19.26" x2="8.40" y2="18.38" />
            <line x1="8.91" y1="20.50" x2="7.67" y2="19.34" />
            <line x1="22.99" y1="13.36" x2="21.86" y2="12.08" />
            <line x1="21.86" y1="14.54" x2="20.52" y2="13.50" />
            <line x1="21.23" y1="15.60" x2="19.65" y2="14.96" />
            <line x1="21.05" y1="16.50" x2="19.35" y2="16.50" />
            <line x1="21.22" y1="17.35" x2="19.66" y2="18.01" />
            <line x1="21.83" y1="18.26" x2="20.55" y2="19.38" />
            <line x1="22.93" y1="19.23" x2="21.93" y2="20.61" />
          </g>
        </g>
      </svg>
    </span>
  );
}
