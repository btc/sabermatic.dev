import { render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { Library } from "../library";

function renderLibrary() {
  return render(
    <MemoryRouter>
      <Library />
    </MemoryRouter>,
  );
}

// jsdom does not implement IntersectionObserver; stub it so useScrollReveal
// doesn't throw.
beforeAll(() => {
  const mockIO = vi.fn().mockImplementation(() => ({
    observe: vi.fn(),
    unobserve: vi.fn(),
    disconnect: vi.fn(),
  }));
  vi.stubGlobal("IntersectionObserver", mockIO);
});

// Mock the connect-query hook to return canned data.
vi.mock("@connectrpc/connect-query", () => ({
  useQuery: vi.fn(),
}));

import { useQuery } from "@connectrpc/connect-query";

const fakeQuestions = [
  { id: "1", title: "Video Streaming", difficulty: 2, tags: ["streaming", "cdn"], imageUrl: undefined },
  { id: "2", title: "News Feed", difficulty: 2, tags: ["social", "fanout"], imageUrl: undefined },
  { id: "3", title: "Ride Sharing", difficulty: 2, tags: ["geospatial"], imageUrl: undefined },
  { id: "4", title: "Chat System", difficulty: 1, tags: ["real-time"], imageUrl: undefined },
  { id: "5", title: "Search Autocomplete", difficulty: 1, tags: ["trie"], imageUrl: undefined },
  { id: "6", title: "Social Graph", difficulty: 1, tags: ["graph"], imageUrl: undefined },
];

describe("Library", () => {
  it("renders 6 cards in order with total count", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: { questions: fakeQuestions, totalCount: 18 },
      isLoading: false,
      isError: false,
    });

    renderLibrary();

    const titles = screen.getAllByRole("heading", { level: 3 }).map((h) => h.textContent);
    expect(titles).toEqual([
      "Video Streaming",
      "News Feed",
      "Ride Sharing",
      "Chat System",
      "Search Autocomplete",
      "Social Graph",
    ]);
    expect(screen.getByText(/18 questions/)).toBeInTheDocument();
  });

  it("renders skeletons while loading", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: undefined,
      isLoading: true,
      isError: false,
    });

    renderLibrary();
    expect(screen.getAllByTestId("library-skeleton")).toHaveLength(6);
  });

  it("renders nothing on error", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: undefined,
      isLoading: false,
      isError: true,
    });

    const { container } = renderLibrary();
    expect(container).toBeEmptyDOMElement();
  });

  it("renders nothing when total_count is 0", () => {
    (useQuery as ReturnType<typeof vi.fn>).mockReturnValue({
      data: { questions: [], totalCount: 0 },
      isLoading: false,
      isError: false,
    });

    const { container } = renderLibrary();
    expect(container).toBeEmptyDOMElement();
  });
});
