import { cn } from "@/lib/utils";

interface BrandNameProps {
  className?: string;
}

export function BrandName({ className }: BrandNameProps) {
  return (
    <span className={cn("whitespace-nowrap", className)}>
      Sabermatic
      <span className="opacity-60">[.DEV]</span>
    </span>
  );
}
