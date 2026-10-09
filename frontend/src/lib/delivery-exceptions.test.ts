import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api-client";
import type { DeliveryException } from "@/lib/api-client";
import {
  buyerDeliveryLabel,
  canClose,
  canRedeliver,
  canRefund,
  deliveryErrorMessage,
  needsReceipt,
  receiptLines,
} from "@/lib/delivery-exceptions";

function dx(overrides: Partial<DeliveryException> = {}): DeliveryException {
  return {
    id: "dx1",
    order_id: "o1",
    vendor_order_id: "vo1",
    vendor_id: "v1",
    shipment_id: "s1",
    exception_type: "returned",
    current_shipment_id: "s1",
    attempt_no: 1,
    carrier_outcome: "returned",
    failed_attempts: 2,
    status: "awaiting_goods",
    policy_version: "delivery-resolution-v1",
    redelivery_limit: 1,
    redelivery_count: 0,
    stock_recovered: false,
    version: 3,
    created_at: "2026-10-08T00:00:00Z",
    updated_at: "2026-10-08T00:00:00Z",
    ...overrides,
  };
}

const sellable = {
  version: 1,
  actor_role: "vendor",
  created_at: "2026-10-08T00:00:00Z",
  lines: [{ order_item_id: "i1", condition: "sellable" as const, quantity: 2 }],
};

describe("receiptLines", () => {
  const items = [
    { id: "i1", quantity: 2 },
    { id: "i2", quantity: 1 },
  ];
  it("accounts for every unit once", () => {
    const out = receiptLines(items, { i1: { sellable: 1, damaged: 1 }, i2: { missing: 1 } });
    expect(out.error).toBeUndefined();
    expect(out.lines).toEqual([
      { item_id: "i1", quantity: 1, condition: "sellable" },
      { item_id: "i1", quantity: 1, condition: "damaged" },
      { item_id: "i2", quantity: 1, condition: "missing" },
    ]);
  });
  it("refuses too few or too many units", () => {
    expect(receiptLines(items, { i1: { sellable: 1 }, i2: { sellable: 1 } }).error).toBeDefined();
    expect(receiptLines(items, { i1: { sellable: 3 }, i2: { sellable: 1 } }).error).toBeDefined();
  });
});

describe("admin decisions", () => {
  it("redelivers only sellable goods back at the shop, once, never after a refund", () => {
    expect(canRedeliver(dx())).toBe(false);
    expect(canRedeliver(dx({ receipt: sellable }))).toBe(true);
    expect(canRedeliver(dx({ receipt: sellable, resolution: "refund" }))).toBe(false);
    expect(canRedeliver(dx({ receipt: sellable, redelivery_count: 1 }))).toBe(false);
    const damaged = { ...sellable, lines: [{ order_item_id: "i1", condition: "damaged" as const, quantity: 2 }] };
    expect(canRedeliver(dx({ receipt: damaged }))).toBe(false);
  });
  it("refunds only once the carrier is done with the package", () => {
    expect(canRefund(dx({ status: "investigating", carrier_outcome: "attempts_exhausted" }))).toBe(false);
    expect(canRefund(dx({ status: "investigating", carrier_outcome: "lost" }))).toBe(true);
    expect(canRefund(dx({ refund_id: "r1" }))).toBe(false);
  });
  it("closes only a package delivered after all", () => {
    expect(canClose(dx({ status: "needs_review" }))).toBe(false);
    expect(
      canClose(dx({ status: "needs_review", carrier_outcome: "delivered", late_delivery_at: "2026-10-09T00:00:00Z" })),
    ).toBe(true);
  });
  it("asks the shop for a receipt while the goods are unrecorded", () => {
    expect(needsReceipt(dx())).toBe(true);
    expect(needsReceipt(dx({ receipt: sellable }))).toBe(false);
    expect(needsReceipt(dx({ carrier_outcome: "lost", status: "investigating" }))).toBe(false);
  });
});

describe("buyer wording", () => {
  it("never says the money is back while the refund is pending", () => {
    expect(buyerDeliveryLabel("refund_pending")).toMatch(/Đang hoàn tiền/);
    expect(buyerDeliveryLabel("awaiting_buyer")).toMatch(/xác nhận/);
  });
  it("explains conflicts", () => {
    expect(deliveryErrorMessage(new ApiError(409, "resolution_locked", "x"), "f")).toMatch(/hoàn tiền/);
    expect(deliveryErrorMessage(new Error("x"), "fallback")).toBe("fallback");
  });
});
