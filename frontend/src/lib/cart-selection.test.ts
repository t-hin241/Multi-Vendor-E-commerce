import { describe, expect, it } from "vitest";
import type { Cart } from "./api-client";
import { restoreSelection, selectedCartView, selectionKey } from "./cart-selection";
import { checkoutAttempt } from "./order-workflow";

const cart = {
  items: [
    { line_id: "a", state: "available", currency: "VND", price_changed: false },
    { line_id: "b", state: "out_of_stock", currency: "VND", price_changed: true },
  ],
  degraded: { catalog: false, inventory: false },
} as Cart;

describe("selective cart checkout", () => {
  it("keeps explicit empty selections and removes stale IDs without selecting other lines", () => {
    expect(restoreSelection(null, cart)).toEqual(["a", "b"]);
    expect(restoreSelection("[]", cart)).toEqual([]);
    expect(restoreSelection('["a","removed"]', cart)).toEqual(["a"]);
  });
  it("unselected stock and price errors do not block selection; no local money quote", () => {
    expect(selectedCartView(cart, ["a"])).toMatchObject({ checkout_ready: true, subtotal: null });
    expect(selectedCartView(cart, ["b"]).checkout_ready).toBe(false);
    expect(selectedCartView(cart, []).checkout_ready).toBe(false);
  });
  it("canonicalizes selection in query and retry keys", () => {
    expect(selectionKey(["b", "a"])).toBe(selectionKey(["a", "b"]));
    const input = {
      addressId: "addr",
      cartVersion: 3,
      expectedTotalAmount: 5000,
      selectedLineIds: ["b", "a"],
    };
    const first = checkoutAttempt(null, input, () => "first-key");
    expect(checkoutAttempt(first, { ...input, selectedLineIds: ["a", "b"] })).toBe(first);
    expect(
      checkoutAttempt(first, { ...input, selectedLineIds: ["a"] }, () => "second-key").key,
    ).toBe("second-key");
  });
});
