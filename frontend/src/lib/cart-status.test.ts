import { describe, expect, it } from "vitest";

import { ApiError } from "./api-client";
import type { Cart, CartLine } from "./api-client";
import {
  checkoutBlockers,
  isCartChanged,
  lineNotice,
  priceChangeText,
  priceConfirmations,
} from "./cart-status";

function line(overrides: Partial<CartLine> = {}): CartLine {
  return {
    line_id: "line-1",
    line_version: 1,
    product_id: "product-1",
    quantity: 2,
    price_amount: 120000,
    currency: "VND",
    price_changed: false,
    subtotal: 240000,
    state: "available",
    stock_status: "in_stock",
    available: true,
    ...overrides,
  };
}

function cart(overrides: Partial<Cart> = {}): Cart {
  return {
    version: 3,
    items: [line()],
    subtotal: { amount: 240000, currency: "VND" },
    total: 240000,
    currency: "VND",
    mixed_currency: false,
    item_count: 2,
    line_count: 1,
    unavailable_lines: 0,
    price_changed_lines: 0,
    over_line_limit: false,
    checkout_ready: true,
    degraded: { catalog: false, inventory: false },
    limits: { max_lines: 50, max_quantity_per_line: 999 },
    page: { limit: 50, offset: 0, total: 1 },
    ...overrides,
  };
}

describe("cart line notices", () => {
  it("has no notice for a purchasable line", () => {
    expect(lineNotice(line())).toBeNull();
  });

  it("tells the buyer how much stock is left", () => {
    const notice = lineNotice(line({ state: "insufficient_stock", available_quantity: 1 }));
    expect(notice?.hint).toContain("Chỉ còn 1");
  });

  it("explains every unavailable state instead of hiding the line", () => {
    for (const state of [
      "removed",
      "under_review",
      "not_for_sale",
      "option_required",
      "option_unavailable",
      "out_of_stock",
      "unverified",
    ] as const) {
      expect(lineNotice(line({ state, available: false }))?.hint.length).toBeGreaterThan(10);
    }
  });
});

describe("price changes", () => {
  it("shows the old and new price without choosing one for the buyer", () => {
    const text = priceChangeText(line({ price_changed: true, seen_price_amount: 100000 }));
    expect(text).toContain("tăng");
    expect(text).toContain("100.000");
    expect(text).toContain("120.000");
  });

  it("confirms exactly the prices the buyer is shown", () => {
    const c = cart({
      items: [
        line({ price_changed: true, seen_price_amount: 100000 }),
        line({ line_id: "line-2" }),
        line({ line_id: "line-3", price_changed: true, price_amount: null }),
      ],
    });
    expect(priceConfirmations(c)).toEqual([
      { line_id: "line-1", price_amount: 120000, currency: "VND" },
    ]);
  });
});

describe("checkout blockers", () => {
  it("is empty when Cart says the cart is ready", () => {
    expect(checkoutBlockers(cart())).toEqual([]);
  });

  it("lists every reason the buyer has to act on", () => {
    const reasons = checkoutBlockers(
      cart({
        checkout_ready: false,
        price_changed_lines: 1,
        unavailable_lines: 2,
        mixed_currency: true,
        degraded: { catalog: true, inventory: false },
      }),
    );
    expect(reasons).toHaveLength(4);
    expect(reasons.join(" ")).toContain("xác nhận giá mới");
  });

  it("recognizes Cart's version conflict", () => {
    expect(isCartChanged(new ApiError(409, "cart_changed", "changed"))).toBe(true);
    expect(isCartChanged(new ApiError(409, "conflict", "other"))).toBe(false);
  });
});
