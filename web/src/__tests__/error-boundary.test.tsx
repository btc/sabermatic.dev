import { render, screen } from "@testing-library/react";
import React from "react";
import { ErrorBoundary } from "react-error-boundary";
import { describe, expect, it, vi } from "vitest";

import { ErrorFallback } from "@/components/error-fallback";

function ThrowingComponent(): never {
  throw new Error("Test render error");
}

describe("ErrorFallback", () => {
  it("renders fallback UI when a child component throws", () => {
    vi.spyOn(console, "error").mockImplementation(() => {});

    render(
      <ErrorBoundary FallbackComponent={ErrorFallback}>
        <ThrowingComponent />
      </ErrorBoundary>,
    );

    expect(screen.getByText(/something went wrong/i)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /reload/i })).toBeInTheDocument();
  });
});
