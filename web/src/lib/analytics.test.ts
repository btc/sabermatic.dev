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
    Object.defineProperty(document, "referrer", {
      configurable: true,
      value: "https://news.ycombinator.com/",
    });
    Object.defineProperty(window, "location", {
      configurable: true,
      value: { search: "?utm_source=hn&utm_medium=social&utm_campaign=show-hn" },
    });
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("posts a landing_view event with referrer and UTM extracted from page state", async () => {
    // Capture the JSON body via Blob constructor spy.
    const captured: BlobPart[] = [];
    const OrigBlob = globalThis.Blob;
    vi.spyOn(globalThis, "Blob").mockImplementation((parts, opts) => {
      if (parts) captured.push(...parts);
      return new OrigBlob(parts, opts);
    });

    track({ event: "landing_view" });

    expect(sendBeaconMock).toHaveBeenCalledTimes(1);
    const url = sendBeaconMock.mock.calls[0]?.[0] as string;
    expect(url).toBe("/api/beacon");
    const body = JSON.parse(captured.join("")) as Record<string, unknown>;
    expect(body.event_name).toBe("landing_view");
    expect(body.referrer).toBe("https://news.ycombinator.com/");
    expect(body.utm_source).toBe("hn");
    expect(body.utm_medium).toBe("social");
    expect(body.utm_campaign).toBe("show-hn");
    expect(body.properties).toBeUndefined();
  });

  it("posts a signup_started event with auth_method nested under properties", () => {
    const captured: BlobPart[] = [];
    const OrigBlob = globalThis.Blob;
    vi.spyOn(globalThis, "Blob").mockImplementation((parts, opts) => {
      if (parts) captured.push(...parts);
      return new OrigBlob(parts, opts);
    });

    track({ event: "signup_started", props: { auth_method: "google" } });

    expect(sendBeaconMock).toHaveBeenCalledTimes(1);
    const body = JSON.parse(captured.join("")) as {
      event_name: string;
      properties?: { auth_method?: string };
    };
    expect(body.event_name).toBe("signup_started");
    expect(body.properties?.auth_method).toBe("google");
  });

  it("falls back to fetch when sendBeacon refuses (returns false)", () => {
    sendBeaconMock.mockReturnValue(false);
    const fetchMock = vi.fn().mockResolvedValue(new Response(null, { status: 204 }));
    Object.defineProperty(globalThis, "fetch", { configurable: true, value: fetchMock });

    track({ event: "landing_view" });

    expect(sendBeaconMock).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, opts] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/beacon");
    expect(opts.method).toBe("POST");
    expect(opts.keepalive).toBe(true);
  });
});
