import { BallMark } from "@/components/ball-mark";
import { cn } from "@/lib/utils";

interface BrandNameProps {
  className?: string;
  /** When true, renders only the initial "S" below sm and the full
   *  "Sabermatic" at sm and above. Use in tight nav contexts.
   *  Default `false` preserves the original full-wordmark behavior. */
  responsiveCompact?: boolean;
}

export function BrandName({ className, responsiveCompact = false }: BrandNameProps) {
  return (
    <span
      role="img"
      aria-label="Sabermatic dot DEV"
      className={cn("whitespace-nowrap", className)}
    >
      {responsiveCompact ? (
        <>
          <span className="hidden sm:inline">Sabermatic</span>
          <span className="sm:hidden">S</span>
        </>
      ) : (
        "Sabermatic"
      )}
      <span className="opacity-60" aria-hidden="true">
        [<BallMark />DEV]
      </span>
    </span>
  );
}
