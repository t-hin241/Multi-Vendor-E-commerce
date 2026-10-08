import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api-client";
import {
  buyerCancellationLabel,
  canAskCancellation,
  cancellationErrorMessage,
} from "@/lib/cancellations";

describe("canAskCancellation", () => {
  const vo = { id: "vo1", status: "paid" as const };
  it("allows a paid package not handed over and without an open request", () => {
    expect(canAskCancellation(vo, [], { status: "ready_to_ship" })).toBe(true);
    expect(canAskCancellation(vo, [{ vendor_order_id: "vo1", status: "rejected" }])).toBe(true);
  });
  it("refuses once shipped or while a request is open", () => {
    expect(canAskCancellation(vo, [], { status: "shipped" })).toBe(false);
    expect(canAskCancellation({ id: "vo1", status: "shipped" }, [])).toBe(false);
    expect(canAskCancellation(vo, [{ vendor_order_id: "vo1", status: "refund_pending" }])).toBe(
      false,
    );
  });
});

describe("buyer wording", () => {
  it("never says refunded before the refund is confirmed", () => {
    expect(buyerCancellationLabel("refund_pending")).not.toMatch(/Đã hủy và hoàn tiền/);
    expect(buyerCancellationLabel("resolved")).toMatch(/hoàn tiền/);
  });
  it("explains a package already with the carrier", () => {
    expect(cancellationErrorMessage(new ApiError(409, "already_shipped", "x"), "fallback")).toMatch(
      /trả hàng/,
    );
  });
});
