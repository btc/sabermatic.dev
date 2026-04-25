import { render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { AuthLayout } from "@/pages/auth/auth-layout";

function renderAuthLayout() {
  return render(
    <MemoryRouter>
      <AuthLayout>
        <div>child content</div>
      </AuthLayout>
    </MemoryRouter>,
  );
}

describe("AuthLayout", () => {
  it("wraps the brand mark in a link to the root landing page", () => {
    renderAuthLayout();
    const link = screen.getByRole("link", { name: "Sabermatic dot DEV" });
    expect(link.getAttribute("href")).toBe("/");
  });

  it("keeps the tagline and children rendered outside the link", () => {
    renderAuthLayout();
    expect(screen.getByText("system design, measured.")).toBeInTheDocument();
    expect(screen.getByText("child content")).toBeInTheDocument();
    // Tagline must not be inside the link wrapping the brand.
    const link = screen.getByRole("link", { name: "Sabermatic dot DEV" });
    expect(link).not.toContainElement(screen.getByText("system design, measured."));
  });
});
