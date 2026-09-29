import { afterEach, describe, expect, it, vi } from "vitest";
import { refreshOnce, withSessionLock } from "./session-refresh";
import * as api from "./api-client";

describe("session refresh", () => {
  afterEach(() => {
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  it("coalesces concurrent requests without persisting refresh tokens", async () => {
    const result = {
      user: {
        id: "test",
        email: "buyer@example.invalid",
        full_name: "Buyer",
        role: "buyer" as const,
      },
      access_token: "synthetic-access",
      access_token_expires_at: "",
      refresh_token_expires_at: "",
    };
    const refresh = vi.spyOn(api, "refreshSession").mockResolvedValue(result);
    const results = await Promise.all([refreshOnce(), refreshOnce(), refreshOnce()]);
    expect(refresh).toHaveBeenCalledTimes(1);
    expect(results).toEqual([result, result, result]);
  });
  it("recovers after a transient refresh failure", async () => {
    const refresh = vi
      .spyOn(api, "refreshSession")
      .mockRejectedValueOnce(new Error("offline"))
      .mockRejectedValueOnce(new Error("expired"));
    await expect(refreshOnce()).rejects.toThrow("offline");
    await expect(refreshOnce()).rejects.toThrow("expired");
    expect(refresh).toHaveBeenCalledTimes(2);
  });
  it("uses a cross-tab lock when the browser provides one", async () => {
    const request = vi.fn(async (_name: string, action: () => Promise<number>) => action());
    vi.stubGlobal("navigator", { locks: { request } });
    await expect(withSessionLock(async () => 7)).resolves.toBe(7);
    expect(request).toHaveBeenCalledWith("shopee-session", expect.any(Function));
  });
});
