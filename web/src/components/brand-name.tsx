import { BallMark } from "@/components/ball-mark";
import { cn } from "@/lib/utils";

interface BrandNameProps {
  className?: string;
}

export function BrandName({ className }: BrandNameProps) {
  return (
    <span
      role="img"
      aria-label="Sabermatic dot DEV"
      className={cn("whitespace-nowrap", className)}
    >
      Sabermatic
      <span className="opacity-60" aria-hidden="true">
        [<BallMark />DEV]
      </span>
    </span>
  );
}
