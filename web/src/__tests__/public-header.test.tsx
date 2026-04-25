import { render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it } from "vitest";

import { PublicHeader } from "@/components/public-header";

function renderAt(pathname: string) {
  return render(
    <MemoryRouter initialEntries={[pathname]}>
      <PublicHeader />
    </MemoryRouter>,
  );
}

describe("PublicHeader", () => {
  it("renders anchor nav on the landing page without a Stack link", () => {
    renderAt("/");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const linkNames = Array.from(nav.querySelectorAll("a")).map((a) => a.textContent?.trim());
    expect(linkNames).toEqual(["Questions", "How it works", "Log in", "Sign up"]);
    expect(screen.queryByRole("link", { name: "Stack" })).toBeNull();
  });

  it("hides anchor links off the landing paths but keeps auth links", () => {
    renderAt("/login");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const linkNames = Array.from(nav.querySelectorAll("a")).map((a) => a.textContent?.trim());
    expect(linkNames).toEqual(["Log in", "Sign up"]);
  });
});
