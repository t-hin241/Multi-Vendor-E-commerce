import { afterEach, describe, expect, it, vi } from "vitest";

import { fetchGatewayHealth } from "./api-client";

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
