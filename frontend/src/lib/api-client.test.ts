import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchGatewayHealth, refreshSession } from "./api-client";

describe("fetchGatewayHealth", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("returns the parsed health payload on success", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({ status: "ok" }),
      }),
    );

    await expect(fetchGatewayHealth()).resolves.toEqual({ status: "ok" });
  });

  it("throws when the gateway responds with a non-ok status", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 503,
        json: async () => ({}),
      }),
    );

    await expect(fetchGatewayHealth()).rejects.toThrow("503");
  });
});

describe("cookie refresh contract", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("sends credentials and CSRF protection without a token in the body", async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: { access_token: "synthetic-access" } }),
    });
    vi.stubGlobal("fetch", fetcher);
    await refreshSession();
    expect(fetcher).toHaveBeenCalledWith(
      expect.stringContaining("/api/auth/refresh"),
      expect.objectContaining({
        credentials: "include",
        method: "POST",
        headers: { "X-CSRF-Protection": "1" },
        body: undefined,
      }),
    );
  });
});
