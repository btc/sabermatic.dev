import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { track } from "./analytics";

describe("track", () => {
  let sendBeaconMock: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    sendBeaconMock = vi.fn().mockReturnValue(true);
    Object.defineProperty(navigator, "sendBeacon", {
      configurable: true,
      value: sendBeaconMock,
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sends a beacon with the serialized event payload", () => {
    track({ event: "landing_view" });

    expect(sendBeaconMock).toHaveBeenCalledTimes(1);
    const [url, blob] = sendBeaconMock.mock.calls[0] as [string, Blob];
    expect(url).toBe("/api/beacon");
    expect(blob).toBeInstanceOf(Blob);
    expect(blob.type).toBe("application/json");
  });

  it("includes auth_method in the payload for signup_started", () => {
    // Intercept the JSON body string via the Blob constructor.
    const capturedParts: BlobPart[] = [];
    const OrigBlob = globalThis.Blob;
    vi.spyOn(globalThis, "Blob").mockImplementation((parts, opts) => {
      if (parts) capturedParts.push(...parts);
      return new OrigBlob(parts, opts);
    });

    track({ event: "signup_started", props: { auth_method: "google" } });

    expect(sendBeaconMock).toHaveBeenCalledTimes(1);
    const body = capturedParts.join("");
    const parsed = JSON.parse(body) as { event: string; props: { auth_method: string } };
    expect(parsed.event).toBe("signup_started");
    expect(parsed.props.auth_method).toBe("google");
  });
});
