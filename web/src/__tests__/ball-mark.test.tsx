import { render } from "@testing-library/react";
import React from "react";
import { describe, expect, it } from "vitest";

import { BallMark } from "@/components/ball-mark";

describe("BallMark", () => {
  it("renders inline SVG with aria-hidden and viewBox", () => {
    const { container } = render(<BallMark />);
    const svg = container.querySelector("svg");
    expect(svg).not.toBeNull();
    expect(svg?.getAttribute("aria-hidden")).toBe("true");
    expect(svg?.getAttribute("viewBox")).toBe("0 0 32 32");
  });

  it("pins the canonical ball geometry (1 circle + 2 seams + 14 ticks) — change deliberately", () => {
    const { container } = render(<BallMark />);
    const svg = container.querySelector("svg")!;
    expect(svg.querySelectorAll("circle")).toHaveLength(1);
    expect(svg.querySelectorAll("path")).toHaveLength(2);
    expect(svg.querySelectorAll("line")).toHaveLength(14);
  });

  it("nests the svg inside the span (two-node DOM)", () => {
    const { container } = render(<BallMark />);
    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.children).toHaveLength(1);
    expect(wrapper.firstElementChild?.tagName.toLowerCase()).toBe("svg");
  });

  it("wraps the svg in an inline-block span with baseline translateY", () => {
    const { container } = render(<BallMark />);
    const wrapper = container.firstChild as HTMLElement;
    expect(wrapper.tagName).toBe("SPAN");
    expect(wrapper.className).toContain("inline-block");
    expect(wrapper.className).toContain("translate-y-[0.05em]");
  });

  it("applies caller className to the inner svg", () => {
    const { container } = render(<BallMark className="group-hover:animate-spin" />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("class")).toContain("group-hover:animate-spin");
  });

  it("keeps em-relative sizing on the inner svg", () => {
    const { container } = render(<BallMark />);
    const svg = container.querySelector("svg")!;
    expect(svg.getAttribute("class")).toContain("h-[0.55em]");
    expect(svg.getAttribute("class")).toContain("w-[0.55em]");
  });
});
