import { expect, test } from "@playwright/test";

import { MockApi } from "./mock-api";

// FE-02: one checkout attempt creates at most one order, whatever happens
// to the response, the page or the buttons.

let api: MockApi;

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  api.session = api.user("buyer.a@example.test");
  await api.install(page);
});

const placeOrder = (page: import("@playwright/test").Page) =>
  page.getByRole("button", { name: /^Đặt hàng ·/ });

test("a lost response is retried with the same key, also after a reload", async ({ page }) => {
  api.loseNextCheckoutResponse = true;
  api.emptyCartOnLostResponse = false; // the order exists, the page cannot tell
  await page.goto("/checkout");
  await placeOrder(page).click();
  await expect(page.getByText("Chưa rõ đơn hàng đã được tạo chưa").first()).toBeVisible();

  await page.reload();
  await placeOrder(page).click();
  await expect(page).toHaveURL(/\/orders\/[0-9a-f-]{36}$/);

  expect(api.orders.size).toBe(1);
  expect(api.checkoutRequests).toHaveLength(2);
  expect(api.checkoutRequests[0]?.key).toBeTruthy();
  expect(api.checkoutRequests[1]?.key).toBe(api.checkoutRequests[0]?.key);
});

test("when the lost order emptied the cart, the buyer is pointed to it", async ({ page }) => {
  api.loseNextCheckoutResponse = true;
  await page.goto("/checkout");
  await placeOrder(page).click();
  await expect(page.getByText("Đơn hàng của bạn có thể đã được tạo")).toBeVisible();
  await page.reload();
  await expect(page.getByText("Đơn hàng của bạn có thể đã được tạo")).toBeVisible();
  await expect(
    page.getByRole("main").getByRole("link", { name: "Đơn hàng của tôi" }),
  ).toBeVisible();
  expect(api.orders.size).toBe(1);
});

test("double clicking places one order", async ({ page }) => {
  await page.goto("/checkout");
  const button = placeOrder(page);
  await button.dblclick();
  await expect(page).toHaveURL(/\/orders\/[0-9a-f-]{36}$/);
  expect(api.orders.size).toBe(1);
  expect(new Set(api.checkoutRequests.map((r) => r.key)).size).toBe(1);
});

test("a changed total is shown and must be confirmed again", async ({ page }) => {
  api.changeTotalOnce = true;
  await page.goto("/checkout");
  await placeOrder(page).click();
  await expect(page.getByText(/Tổng tiền đã đổi từ 150\.000 VND thành/)).toBeVisible();
  await expect(placeOrder(page)).toContainText("165.000");
  expect(api.orders.size).toBe(0);
  await placeOrder(page).click();
  await expect(page).toHaveURL(/\/orders\//);
  expect(api.checkoutRequests.at(-1)?.expected).toBe(165_000);
  expect(api.checkoutRequests.at(-1)?.key).not.toBe(api.checkoutRequests[0]?.key);
});
