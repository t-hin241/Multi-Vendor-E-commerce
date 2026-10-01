import { afterEach, describe, expect, it, vi } from "vitest";

import { type ApiError, fetchGatewayHealth, refreshSession } from "./api-client";

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

describe("admin operation outcome", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("sends one operation id as request id and idempotency key", async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      status: 201,
      headers: new Headers(),
      json: async () => ({ data: { id: "refund-1" } }),
    });
    vi.stubGlobal("fetch", fetcher);
    const { requestOrderRefund } = await import("./api-client");
    await requestOrderRefund(
      "token",
      "order-1",
      { reason_code: "dispute", amount: 10, reason: "test", vendor_order_id: "vo-1" },
      "op-12345678",
    );
    const [, init] = fetcher.mock.calls[0] as [string, RequestInit];
    const headers = init.headers as Record<string, string>;
    expect(headers["X-Request-Id"]).toBe("op-12345678");
    expect(headers["Idempotency-Key"]).toBe("op-12345678");
  });

  it("reports a lost response as an unknown outcome with its request id", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    const { ApiError, isOutcomeUnknown, request } = await import("./api-client");
    const err = (await request("/x", { method: "POST", requestId: "op-abcdefgh" }).catch(
      (e: unknown) => e,
    )) as InstanceType<typeof ApiError>;
    expect(err).toBeInstanceOf(ApiError);
    expect(isOutcomeUnknown(err)).toBe(true);
    expect(err.requestId).toBe("op-abcdefgh");
  });

  it("treats a refusal as a definite failure", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: false,
        status: 409,
        headers: new Headers({ "X-Request-Id": "req-server-1" }),
        json: async () => ({ error: { code: "conflict", message: "Already open" } }),
      }),
    );
    const { isOutcomeUnknown, request } = await import("./api-client");
    const err = (await request("/x", { method: "POST" }).catch((e: unknown) => e)) as ApiError;
    expect(isOutcomeUnknown(err)).toBe(false);
    expect(err.requestId).toBe("req-server-1");
  });
});
