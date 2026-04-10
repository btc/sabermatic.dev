import { beforeEach, describe, expect, it, vi } from "vitest";

import { apiClient, ApiError } from "../client";

describe("apiClient", () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    delete window.__csrfToken;
  });

  it("sends GET request and parses JSON", async () => {
    const mockData = { id: "123", email: "test@example.com" };
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify(mockData), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    const result = await apiClient.get("/api/me");
    expect(result).toEqual(mockData);
    expect(fetch).toHaveBeenCalledWith("/api/me", expect.objectContaining({
      method: "GET",
      credentials: "same-origin",
    }));
  });

  it("sends POST with JSON body and CSRF token", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(JSON.stringify({ id: "456" }), { status: 200 }),
    );
    window.__csrfToken = "abc123";
    await apiClient.post("/api/sessions", { question_id: "q1" });
    expect(fetch).toHaveBeenCalledWith("/api/sessions", expect.objectContaining({
      method: "POST",
      headers: expect.objectContaining({
        "Content-Type": "application/json",
        "X-CSRF-Token": "abc123",
      }),
      body: JSON.stringify({ question_id: "q1" }),
    }));
  });

  it("throws ApiError on 4xx response", async () => {
    vi.spyOn(globalThis, "fetch").mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify({ error: "not found" }), { status: 404 }),
      ),
    );
    await expect(apiClient.get("/api/sessions/bad")).rejects.toThrow(ApiError);
    await expect(apiClient.get("/api/sessions/bad")).rejects.toMatchObject({ status: 404 });
  });

  it("returns null for DELETE with 204", async () => {
    vi.spyOn(globalThis, "fetch").mockResolvedValue(
      new Response(null, { status: 204 }),
    );
    const result = await apiClient.delete("/api/auth/account");
    expect(result).toBeNull();
  });
});
