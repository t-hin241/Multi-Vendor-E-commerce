import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, checkout } from "./api-client";
import type { CheckoutPreview, OrderItem, ReturnRequest } from "./api-client";
import {
  canPlaceOrder,
  checkoutAttempt,
  checkoutErrorKind,
  returnStatusLabel,
  returnableQuantity,
} from "./order-workflow";

describe("checkoutAttempt", () => {
  it("reuses the key for a retry of the same input and renews it on change", () => {
    let n = 0;
    const gen = () => `key-${++n}`;
    const input = { addressId: "a", cartVersion: 3, expectedTotalAmount: 1000 };
    const first = checkoutAttempt(null, input, gen);
    expect(checkoutAttempt(first, { ...input }, gen)).toBe(first);
    const changed = checkoutAttempt(first, { ...input, expectedTotalAmount: 1100 }, gen);
    expect(changed.key).not.toBe(first.key);
  });
});

describe("checkout request", () => {
  afterEach(() => vi.unstubAllGlobals());
  it("sends the idempotency key header and the confirmed total", async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: { id: "order-1" } }),
    });
    vi.stubGlobal("fetch", fetcher);
    await checkout("synthetic-token", {
      addressId: "addr",
      cartVersion: 2,
      expectedTotalAmount: 150000,
      idempotencyKey: "fake-idempotency-key",
    });
    const init = fetcher.mock.calls[0]?.[1] as { headers: Record<string, string>; body: string };
    expect(init.headers["Idempotency-Key"]).toBe("fake-idempotency-key");
    expect(JSON.parse(init.body)).toEqual({
      address_id: "addr",
      cart_version: 2,
      expected_total_amount: 150000,
    });
  });
});

describe("canPlaceOrder", () => {
  const preview: CheckoutPreview = {
    cart_version: 4,
    currency: "VND",
    subtotal_amount: 100,
    shipping_amount: 20,
    total_amount: 120,
    ready: true,
    vendors: [],
  };
  it("requires a ready preview of the cart version on screen", () => {
    expect(canPlaceOrder(preview, 4)).toBe(true);
    expect(canPlaceOrder(preview, 5)).toBe(false);
    expect(canPlaceOrder({ ...preview, ready: false, total_amount: null }, 4)).toBe(false);
    expect(canPlaceOrder(undefined, 4)).toBe(false);
  });
});

describe("checkoutErrorKind", () => {
  it("maps Order's checkout codes", () => {
    expect(checkoutErrorKind(new ApiError(409, "checkout_total_changed", "x"))).toBe(
      "total_changed",
    );
    expect(checkoutErrorKind(new ApiError(409, "shipping_unavailable", "x"))).toBe(
      "shipping_unavailable",
    );
    expect(checkoutErrorKind(new ApiError(409, "checkout_in_progress", "x"))).toBe("in_progress");
    expect(checkoutErrorKind(new Error("network"))).toBe("other");
  });
});

describe("returns", () => {
  const item = { id: "i1", quantity: 3 } as OrderItem;
  const ret = (status: ReturnRequest["status"], quantity: number) =>
    ({ order_item_id: "i1", status, quantity }) as ReturnRequest;

  it("counts every non-rejected return against the purchased quantity", () => {
    expect(returnableQuantity(item, [ret("rejected", 3), ret("approved", 1)])).toBe(2);
    expect(returnableQuantity(item, [ret("refunded", 3)])).toBe(0);
    expect(returnableQuantity(item, undefined)).toBe(3);
  });

  it("never presents an approved return as refunded", () => {
    expect(returnStatusLabel("approved")).not.toMatch(/hoàn tiền/i);
    expect(returnStatusLabel("unknown_state")).toBe("unknown_state");
  });
});
