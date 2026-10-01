import { describe, expect, it } from "vitest";

import { orderIdFromQuery, paymentView, pollDelay } from "./payment-polling";

describe("paymentView", () => {
  it("shows paid only when Order says so", () => {
    expect(paymentView({ status: "paid" }, true, false)).toBe("paid");
    expect(paymentView({ status: "shipped" }, false, false)).toBe("paid");
    expect(paymentView({ status: "pending_payment" }, true, false)).toBe("confirming");
  });

  it("says unknown, not failed, when the confirmation does not arrive in time", () => {
    expect(paymentView({ status: "pending_payment" }, true, true)).toBe("unknown");
    expect(paymentView(undefined, true, true)).toBe("unknown");
  });

  it("treats a cancel return as still unpaid and a cancelled order as cancelled", () => {
    expect(paymentView({ status: "pending_payment" }, false, false)).toBe("unpaid");
    expect(paymentView({ status: "cancelled" }, true, false)).toBe("cancelled");
  });

  it("does not guess for a status it does not know", () => {
    expect(paymentView({ status: "on_hold" as never }, true, false)).toBe("unknown");
  });
});

describe("pollDelay", () => {
  it("backs off and stops after the limit", () => {
    expect([0, 1, 2, 3, 4, 5, 9].map((n) => pollDelay(n, 0))).toEqual([
      1000, 2000, 3000, 5000, 8000, 10000, 10000,
    ]);
    expect(pollDelay(3, 120_000)).toBeNull();
  });
});

describe("orderIdFromQuery", () => {
  it("accepts an order id only", () => {
    expect(orderIdFromQuery("0b6f8c1e-6a4e-4d2b-9a43-1d1a6f0e9b11")).toBe(
      "0b6f8c1e-6a4e-4d2b-9a43-1d1a6f0e9b11",
    );
    expect(orderIdFromQuery("../admin")).toBeNull();
    expect(orderIdFromQuery("<script>")).toBeNull();
    expect(orderIdFromQuery(null)).toBeNull();
  });
});
