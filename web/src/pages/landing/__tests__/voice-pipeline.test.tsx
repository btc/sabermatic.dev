import { render, screen } from "@testing-library/react";
import React from "react";
import { describe, expect, it } from "vitest";

import { VoicePipeline } from "@/pages/landing/voice-pipeline";

describe("VoicePipeline", () => {
  it("renders the new headline", () => {
    render(<VoicePipeline />);
    const heading = screen.getByRole("heading", { level: 2 });
    // Use jest-dom's toHaveTextContent because React preserves the JSX
    // newline+indent whitespace inside the <h2>; toBe() against the bare
    // string would fail on the surrounding whitespace.
    expect(heading).toHaveTextContent(
      "Conversational mock interviews with an expert interviewer.",
    );
  });

  it("renders the new subhead", () => {
    render(<VoicePipeline />);
    expect(
      screen.getByText(
        "Adaptive follow-ups. Pushback when you hand-wave. Patient when you're mid-thought.",
      ),
    ).toBeInTheDocument();
  });

  it("is labelled as section 01 — Conversation", () => {
    const { container } = render(<VoicePipeline />);
    const label = container.querySelector("section header > div");
    expect(label?.textContent?.replace(/\s+/g, " ").trim()).toBe("01 — Conversation");
  });
});
