import { describe, expect, it } from "vitest";

import { safeRedirect } from "./redirect";

describe("safeRedirect", () => {
  it.each([
    ["/", "/"],
    ["/sessions/new?question=abc", "/sessions/new?question=abc"],
    ["/history#latest", "/history#latest"],
  ])("keeps same-origin path %j", (raw, want) => {
    expect(safeRedirect(raw)).toBe(want);
  });

  it.each([
    [null],
    [""],
    ["https://evil.com/pwn"],
    ["evil.com"],
    ["//evil.com/pwn"],
    // Browsers treat "\" as "/" in http(s) URLs, so these become "//evil.com".
    ["/\\evil.com"],
    ["/\\/evil.com"],
    // The URL parser strips ASCII tab and newline before parsing.
    ["/\t/evil.com"],
    ["/\n/evil.com"],
    // Dot segments collapse during normalization and can leave a leading "//".
    ["/.//evil.com"],
    ["/..//evil.com"],
    ["/%2e//evil.com"],
    ["/a/..//evil.com"],
    ["/./\\evil.com"],
  ])("falls back to / for %j", (raw) => {
    expect(safeRedirect(raw)).toBe("/");
  });
});
