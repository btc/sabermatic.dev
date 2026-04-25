import { render } from "@testing-library/react";
import React from "react";
import { MemoryRouter } from "react-router-dom";
import { describe, expect, it, vi } from "vitest";

// Stub every landing section so this test exercises ONLY landing/index.tsx
// JSX ordering. Each section becomes a single <div> with a stable data-testid.
// Rationale: Scoring/StrengthsGaps/Transcript/Coaching all read different
// data shapes via `useSampleEvaluation`/`useSampleSession`/`useSampleCoach`
// and early-return null on missing data. Trying to feed them a single canned
// shape from a global useQuery mock results in those sections rendering
// nothing, which would make any DOM-order assertion meaningless. Stubbing
// the section components themselves keeps this test focused on the
// responsibility of `landing/index.tsx`: composing sections in a specific
// order. Per-section label and copy assertions live in their own files
// (e.g. `voice-pipeline.test.tsx`).
vi.mock("@/pages/landing/hero", () => ({
  Hero: () => <div data-testid="section-hero" />,
}));
vi.mock("@/pages/landing/voice-pipeline", () => ({
  VoicePipeline: () => <div data-testid="section-conversation" />,
}));
vi.mock("@/pages/landing/scoring", () => ({
  Scoring: () => <div data-testid="section-evaluation" />,
}));
vi.mock("@/pages/landing/strengths-gaps", () => ({
  StrengthsGaps: () => <div data-testid="section-evidence" />,
}));
vi.mock("@/pages/landing/transcript", () => ({
  Transcript: () => <div data-testid="section-transcript" />,
}));
vi.mock("@/pages/landing/coaching", () => ({
  Coaching: () => <div data-testid="section-coaching" />,
}));
vi.mock("@/pages/landing/library", () => ({
  Library: () => <div data-testid="section-library" />,
}));
vi.mock("@/pages/landing/cta-repeat", () => ({
  CTARepeat: () => <div data-testid="section-cta" />,
}));

import Landing from "@/pages/landing";

function renderLanding() {
  return render(
    <MemoryRouter>
      <Landing />
    </MemoryRouter>,
  );
}

describe("Landing (index)", () => {
  it("renders sections in the new order with Conversation first after Hero", () => {
    const { container } = renderLanding();
    const order = Array.from(container.querySelectorAll("[data-testid^='section-']"))
      .map((el) => el.getAttribute("data-testid"));

    expect(order).toEqual([
      "section-hero",
      "section-conversation",
      "section-evaluation",
      "section-evidence",
      "section-transcript",
      "section-coaching",
      "section-library",
      "section-cta",
    ]);
  });

  it("does not render the removed Credits section", () => {
    const { container } = renderLanding();
    // If `landing/index.tsx` were to re-import and render `<Credits />`,
    // either typecheck would fail (the file is deleted in Task 4) or — if
    // somehow re-introduced — the unmocked component would render and add
    // a <section> with id="stack" / "No magic" copy. Neither should appear.
    expect(container.querySelector("[data-testid='section-credits']")).toBeNull();
    expect(container.querySelector("#stack")).toBeNull();
  });
});
