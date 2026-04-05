import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { App } from "@/app";

describe("404 Not Found", () => {
  it("renders not-found page for unknown routes", async () => {
    render(
      <MemoryRouter initialEntries={["/this-does-not-exist"]}>
        <App />
      </MemoryRouter>,
    );

    // App uses React.lazy() for page components, so use findByText (async).
    expect(await screen.findByText(/page not found/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /home/i })).toHaveAttribute(
      "href",
      "/",
    );
  });
});
