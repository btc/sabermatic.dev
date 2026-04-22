import { render, screen } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { Hero } from "@/pages/landing/hero";

// jsdom does not implement IntersectionObserver or window.matchMedia; stub
// both so useScrollReveal and the tagline cycling effect don't throw.
beforeAll(() => {
  const mockIO = vi.fn().mockImplementation(() => ({
    observe: vi.fn(),
    unobserve: vi.fn(),
    disconnect: vi.fn(),
  }));
  vi.stubGlobal("IntersectionObserver", mockIO);

  vi.stubGlobal(
    "matchMedia",
    vi.fn().mockImplementation((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addListener: vi.fn(),
      removeListener: vi.fn(),
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      dispatchEvent: vi.fn(),
    })),
  );
});

function renderHero() {
  return render(
    <MemoryRouter>
      <Hero />
    </MemoryRouter>,
  );
}

describe("Hero", () => {
  it("gives the <h1> a canonical aria-label", () => {
    renderHero();
    const heading = screen.getByRole("heading", { level: 1 });
    expect(heading.getAttribute("aria-label")).toBe("Sabermatic dot DEV");
  });

  it("embeds the BallMark SVG as a descendant of the <h1>", () => {
    renderHero();
    const heading = screen.getByRole("heading", { level: 1 });
    const svg = heading.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 32 32");
  });

  it("passes the hover-spin className (motion-reduce aware) to BallMark", () => {
    renderHero();
    const heading = screen.getByRole("heading", { level: 1 });
    const svg = heading.querySelector("svg")!;
    const classAttr = svg.getAttribute("class") ?? "";
    expect(classAttr).toContain("group-hover:animate-[spin_2s_linear_infinite]");
    expect(classAttr).toContain("motion-reduce:animate-none");
  });

  it("does NOT wrap the wordmark in <BrandName/> (guards against accidental refactor)", () => {
    renderHero();
    const heading = screen.getByRole("heading", { level: 1 });
    // BrandName introduces a role="img" span inside its wordmark region.
    expect(heading.querySelector('[role="img"]')).toBeNull();
  });

  it("keeps the hero's lowercase casing and bracketed opacity treatment", () => {
    renderHero();
    const heading = screen.getByRole("heading", { level: 1 });
    const text = heading.textContent ?? "";
    expect(text).toContain("sabermatic"); // lowercase, deliberately
    const dimSpan = heading.querySelector('span[aria-hidden="true"]');
    expect(dimSpan?.className).toContain("opacity-40");
  });

  it("adds the group class to <h1> to enable group-hover animation on the ball", () => {
    renderHero();
    const heading = screen.getByRole("heading", { level: 1 });
    expect(heading.className.split(/\s+/)).toContain("group");
  });
});
