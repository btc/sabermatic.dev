import { render, screen } from "@testing-library/react";
import React from "react";
import { describe, expect, it } from "vitest";

import { OAuthError } from "@/pages/auth/oauth-error";

describe("OAuthError", () => {
  it("renders nothing when code is null", () => {
    const { container } = render(<OAuthError code={null} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("renders the friendly message for oauth_failed", () => {
    render(<OAuthError code="oauth_failed" />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Sign-in didn't complete. Please try again.");
  });

  it("renders the friendly message for internal", () => {
    render(<OAuthError code="internal" />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Something went wrong on our end. Please try again.");
  });

  it("falls back to the oauth_failed message for unknown codes", () => {
    render(<OAuthError code="future_unknown_code" />);
    const alert = screen.getByRole("alert");
    expect(alert).toHaveTextContent("Sign-in didn't complete. Please try again.");
  });
});
