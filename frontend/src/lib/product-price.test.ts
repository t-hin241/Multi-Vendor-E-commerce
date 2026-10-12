import { describe, expect, it } from "vitest";
import type { Product } from "./api-client";
import { productPriceText } from "./product-price";
import { formatMoney } from "./format";
import { checkoutAttempt } from "./order-workflow";

const product = {
  price_amount: 100,
  currency: "VND",
  min_price: 150,
  max_price: 250,
  variants: [
    { id: "small", price_amount: 150 },
    { id: "large", price_amount: 250 },
  ],
} as Product;

describe("SKU prices", () => {
  it("shows the range then the selected SKU price", () => {
    expect(productPriceText(product)).toBe(
      `${formatMoney(150, "VND")} – ${formatMoney(250, "VND")}`,
    );
    expect(productPriceText(product, "large")).toBe(formatMoney(250, "VND"));
  });
  it("does not replace a missing selected SKU with product price", () => {
    expect(productPriceText(product, "removed")).toBe("Chưa có giá");
  });
  it("keeps plain product pricing", () => {
    expect(productPriceText({ ...product, variants: [], min_price: 100, max_price: 100 })).toBe(
      formatMoney(100, "VND"),
    );
  });
  it("starts a new checkout attempt when quote version changes at the same total", () => {
    const input = {
      addressId: "address",
      cartVersion: 1,
      expectedTotalAmount: 100,
      expectedQuoteVersion: "first",
    };
    const first = checkoutAttempt(null, input, () => "key-one");
    expect(checkoutAttempt(first, input, () => "unused")).toBe(first);
    expect(
      checkoutAttempt(first, { ...input, expectedQuoteVersion: "second" }, () => "key-two").key,
    ).toBe("key-two");
  });
});
