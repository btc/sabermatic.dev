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

  describe("with responsiveCompact", () => {
    it("renders the mobile 'S' fragment and the desktop 'Sabermatic' fragment with the right Tailwind tokens", () => {
      const { container } = render(<BrandName responsiveCompact />);
      const desktop = container.querySelector("span.hidden.sm\\:inline");
      const mobile = container.querySelector("span.sm\\:hidden");
      expect(desktop?.textContent).toBe("Sabermatic");
      expect(mobile?.textContent).toBe("S");
    });

    it("places the bracketed [DEV] suffix immediately after the mobile 'S' fragment", () => {
      const { container } = render(<BrandName responsiveCompact />);
      const mobile = container.querySelector("span.sm\\:hidden");
      const next = mobile?.nextElementSibling as HTMLElement | null;
      expect(next).not.toBeNull();
      expect(next?.getAttribute("aria-hidden")).toBe("true");
      expect(next?.textContent).toContain("[");
      expect(next?.textContent).toContain("DEV");
      expect(next?.textContent).toContain("]");
    });

    it("keeps aria-label canonical regardless of the visible variant", () => {
      const compact = render(<BrandName responsiveCompact />);
      const compactImg = compact.getByRole("img", { name: "Sabermatic dot DEV" });
      expect(compactImg).toBeInTheDocument();
      compact.unmount();

      const full = render(<BrandName />);
      const fullImg = full.getByRole("img", { name: "Sabermatic dot DEV" });
      expect(fullImg).toBeInTheDocument();
    });
  });
});
