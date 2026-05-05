import { render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { LegalPage } from "@/components/legal-page";

describe("LegalPage", () => {
  it("renders the title and children inside the prose region", () => {
    render(
      <MemoryRouter>
        <LegalPage title="Test Title">
          <p>Body paragraph</p>
        </LegalPage>
      </MemoryRouter>,
    );
    expect(screen.getByRole("heading", { level: 1, name: "Test Title" })).toBeInTheDocument();
    expect(screen.getByText("Body paragraph")).toBeInTheDocument();
  });
});
