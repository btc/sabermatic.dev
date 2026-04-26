import { renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// Capture the options passed to useConnectMutation so we can fire its
// onSuccess/onError directly without spinning up a real Transport + QueryClient.
const capturedOptions: { onSuccess?: () => void; onError?: () => void }[] = [];
vi.mock("@connectrpc/connect-query", async () => {
  const actual = await vi.importActual<typeof import("@connectrpc/connect-query")>(
    "@connectrpc/connect-query",
  );
  return {
    ...actual,
    useMutation: (_method: unknown, options: { onSuccess?: () => void; onError?: () => void }) => {
      capturedOptions.push(options);
      return { mutate: vi.fn(), isPending: false };
    },
  };
});

// useQueryClient must return *something* with a .clear() method.
const clearSpy = vi.fn();
vi.mock("@tanstack/react-query", async () => {
  const actual = await vi.importActual<typeof import("@tanstack/react-query")>(
    "@tanstack/react-query",
  );
  return {
    ...actual,
    useQueryClient: () => ({ clear: clearSpy }),
  };
});

import { useLogout } from "@/api/queries";

describe("useLogout", () => {
  let originalLocation: Location;
  let replaceSpy: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    capturedOptions.length = 0;
    clearSpy.mockClear();
    replaceSpy = vi.fn();
    originalLocation = window.location;
    Object.defineProperty(window, "location", {
      configurable: true,
      writable: true,
      value: { ...originalLocation, replace: replaceSpy },
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "location", {
      configurable: true,
      writable: true,
      value: originalLocation,
    });
  });

  it("on success, clears the query cache and replaces window.location with /", () => {
    renderHook(() => useLogout());
    const opts = capturedOptions.at(-1);
    expect(opts?.onSuccess).toBeDefined();
    opts!.onSuccess!();
    expect(clearSpy).toHaveBeenCalledTimes(1);
    expect(replaceSpy).toHaveBeenCalledWith("/");
  });

  it("on error, also clears the query cache and replaces window.location with /", () => {
    renderHook(() => useLogout());
    const opts = capturedOptions.at(-1);
    expect(opts?.onError).toBeDefined();
    opts!.onError!();
    expect(clearSpy).toHaveBeenCalledTimes(1);
    expect(replaceSpy).toHaveBeenCalledWith("/");
  });
});
