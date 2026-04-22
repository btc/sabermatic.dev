import { render, screen } from "@testing-library/react";
import React from "react";
import { describe, expect, it } from "vitest";

import { BrandName } from "@/components/brand-name";

describe("BrandName", () => {
  it("renders an accessible img with the canonical label", () => {
    render(<BrandName />);
    const name = screen.getByRole("img", { name: "Sabermatic dot DEV" });
    expect(name).toBeInTheDocument();
  });

  it("renders the visible wordmark text", () => {
    const { container } = render(<BrandName />);
    const text = container.textContent ?? "";
    expect(text).toContain("Sabermatic");
    expect(text).toContain("[DEV]");
  });

  it("marks the styled inner span aria-hidden to avoid double-reading", () => {
    const { container } = render(<BrandName />);
    const outer = container.firstChild as HTMLElement;
    const inner = outer.querySelector("span");
    expect(inner).not.toBeNull();
    expect(inner?.getAttribute("aria-hidden")).toBe("true");
  });

  it("embeds the BallMark primitive inside the inner styled span", () => {
    const { container } = render(<BrandName />);
    const inner = container.querySelector('span[aria-hidden="true"]');
    const svg = inner?.querySelector("svg");
    expect(svg).not.toBeNull();
  });

  it("forwards caller className to the outer span", () => {
    const { container } = render(<BrandName className="text-red-500" />);
    const outer = container.firstChild as HTMLElement;
    expect(outer.className).toContain("text-red-500");
    expect(outer.className).toContain("whitespace-nowrap");
  });
});
