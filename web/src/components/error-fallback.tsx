import type { FallbackProps } from "react-error-boundary";

import { BrandName } from "@/components/brand-name";

export function ErrorFallback({ resetErrorBoundary }: FallbackProps) {
  return (
    <div className="flex h-screen flex-col items-center justify-center gap-4 text-center">
      <BrandName className="text-sm font-semibold tracking-wider text-muted-foreground mb-4" />
      <h1 className="text-2xl font-semibold">Something went wrong</h1>
      <p className="text-muted-foreground">
        An unexpected error occurred. Please try reloading the page.
      </p>
      <button
        onClick={resetErrorBoundary}
        className="rounded-md bg-primary px-4 py-2 text-primary-foreground hover:bg-primary/90"
      >
        Reload page
      </button>
    </div>
  );
}
