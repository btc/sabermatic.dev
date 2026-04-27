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

  it("hides the section anchors below sm and shows them at sm+ via Tailwind tokens", () => {
    renderAt("/");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const anchors = Array.from(nav.querySelectorAll("a")).filter((a) => {
      const text = a.textContent?.trim();
      return text === "Questions" || text === "How it works";
    });
    expect(anchors).toHaveLength(2);
    for (const anchor of anchors) {
      const tokens = anchor.className.split(/\s+/);
      expect(tokens).toContain("hidden");
      expect(tokens).toContain("sm:inline-block");
    }
  });

  it("does NOT apply the responsive-hide tokens to the auth links", () => {
    renderAt("/");
    const nav = screen.getByRole("navigation", { name: "Public navigation" });
    const authLinks = Array.from(nav.querySelectorAll("a")).filter((a) => {
      const text = a.textContent?.trim();
      return text === "Log in" || text === "Sign up";
    });
    expect(authLinks).toHaveLength(2);
    for (const link of authLinks) {
      const tokens = link.className.split(/\s+/);
      expect(tokens).not.toContain("hidden");
      expect(tokens).not.toContain("sm:inline-block");
    }
  });

  it("renders the brand link with an expanded tap area (negative margins cancelling padding)", () => {
    renderAt("/");
    const brandLink = screen.getByRole("link", { name: "Sabermatic dot DEV" });
    const tokens = brandLink.className.split(/\s+/);
    // Hyphen-prefixed classes need exact-token matching, not \b regex,
    // because \b requires a word char on the boundary and "-" is non-word.
    expect(tokens).toContain("-mx-1");
    expect(tokens).toContain("px-1");
    expect(tokens).toContain("-my-3");
    expect(tokens).toContain("py-3");
  });

  it("passes responsiveCompact to the brand mark so mobile renders an 'S' fragment", () => {
    renderAt("/");
    const brandLink = screen.getByRole("link", { name: "Sabermatic dot DEV" });
    const mobileFragment = brandLink.querySelector("span.sm\\:hidden");
    expect(mobileFragment?.textContent).toBe("S");
  });
});
