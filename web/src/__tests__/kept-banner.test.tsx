import { fireEvent, render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

import * as queries from "@/api/queries";
import { KeptBanner } from "@/components/kept-banner";

function renderBanner() {
  return render(
    <MemoryRouter>
      <KeptBanner />
    </MemoryRouter>,
  );
}

describe("KeptBanner", () => {
  it("renders nothing when pendingKeptBanner is false", () => {
    vi.spyOn(queries, "useMe").mockReturnValue({
      data: { user: { pendingKeptBanner: false } },
    } as ReturnType<typeof queries.useMe>);
    vi.spyOn(queries, "useAckKeptBanner").mockReturnValue({
      mutate: vi.fn(),
    } as unknown as ReturnType<typeof queries.useAckKeptBanner>);

    const { container } = renderBanner();
    expect(container.firstChild).toBeNull();
  });

  it("renders nothing when meData is undefined", () => {
    vi.spyOn(queries, "useMe").mockReturnValue({
      data: undefined,
    } as ReturnType<typeof queries.useMe>);
    vi.spyOn(queries, "useAckKeptBanner").mockReturnValue({
      mutate: vi.fn(),
    } as unknown as ReturnType<typeof queries.useAckKeptBanner>);

    const { container } = renderBanner();
    expect(container.firstChild).toBeNull();
  });

  it("renders banner when pendingKeptBanner is true", () => {
    vi.spyOn(queries, "useMe").mockReturnValue({
      data: { user: { pendingKeptBanner: true } },
    } as ReturnType<typeof queries.useMe>);
    vi.spyOn(queries, "useAckKeptBanner").mockReturnValue({
      mutate: vi.fn(),
    } as unknown as ReturnType<typeof queries.useAckKeptBanner>);

    renderBanner();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    expect(screen.getByText(/welcome back/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /manage subscription/i })).toBeInTheDocument();
  });

  it("dismissing hides banner and calls ackKeptBanner", () => {
    const mutate = vi.fn();
    vi.spyOn(queries, "useMe").mockReturnValue({
      data: { user: { pendingKeptBanner: true } },
    } as ReturnType<typeof queries.useMe>);
    vi.spyOn(queries, "useAckKeptBanner").mockReturnValue({
      mutate,
    } as unknown as ReturnType<typeof queries.useAckKeptBanner>);

    renderBanner();
    fireEvent.click(screen.getByRole("button", { name: /dismiss/i }));
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(mutate).toHaveBeenCalledTimes(1);
  });
});
