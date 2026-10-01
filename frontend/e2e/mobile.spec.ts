import { expect, test } from "@playwright/test";

import { MockApi } from "./mock-api";

// FE-05: the buyer's purchase path fits a phone screen.

test("checkout and payment pages fit a phone without sideways scrolling", async ({ page }) => {
  const api = new MockApi();
  api.session = api.user("buyer.a@example.test");
  await api.install(page);
  const order = api.addOrder(api.session);
  for (const path of [
    "/checkout",
    `/orders/${order.id}`,
    `/orders/payment-cancel?order_id=${order.id}`,
  ]) {
    await page.goto(path);
    await page.waitForLoadState("networkidle");
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow, `${path} scrolls sideways`).toBeLessThanOrEqual(1);
  }
  await page.goto("/checkout");
  const place = page.getByRole("button", { name: /^Đặt hàng ·/ });
  await place.scrollIntoViewIfNeeded();
  await expect(place).toBeInViewport();
  await expect(place).toBeEnabled();
});
