import { render } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("@/lib/analytics", () => ({
  track: vi.fn(),
}));
vi.mock("@/components/public-footer", () => ({
  PublicFooter: () => <div data-testid="public-footer" />,
}));
vi.mock("@/components/public-header", () => ({
  PublicHeader: () => <div data-testid="public-header" />,
}));
vi.mock("@/pages/session/layout", () => ({
  TabLink: ({ children }: { children: React.ReactNode }) => <span>{children}</span>,
}));

import { track } from "@/lib/analytics";
import SampleSession from "@/pages/sample";

// The module-scoped `sampleViewFired` flag in `@/pages/sample` persists for
// the full lifetime of this test file's vitest worker. After the first render
// fires it, subsequent renders in any test in this file will short-circuit
// without firing — including a hypothetical second test asserting "fires on
// fresh mount". If you add another test here that needs a fresh mount, either
// (a) collapse it into the existing combined test, or (b) call
// `vi.resetModules()` in `beforeEach` and dynamically re-import
// `@/pages/sample` (and re-mock `@/lib/analytics`) inside each test.
describe("SampleSession layout", () => {
  beforeEach(() => {
    vi.mocked(track).mockClear();
  });

  it("fires sample_view beacon once on mount and does not re-fire on remount", () => {
    const { unmount } = render(
      <MemoryRouter initialEntries={["/sample"]}>
        <SampleSession />
      </MemoryRouter>,
    );
    expect(track).toHaveBeenCalledTimes(1);
    expect(vi.mocked(track).mock.calls[0]?.[0]).toEqual({ event: "sample_view" });

    unmount();
    render(
      <MemoryRouter initialEntries={["/sample"]}>
        <SampleSession />
      </MemoryRouter>,
    );
    // Module-scoped guard prevents a second emission within the same page load
    // (auth-flip remounts, browser-back, StrictMode dev double-invoke).
    expect(track).toHaveBeenCalledTimes(1);
  });
});
