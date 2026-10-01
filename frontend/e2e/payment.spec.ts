import { expect, test } from "@playwright/test";

import { MockApi } from "./mock-api";

// FE-03: coming back from the payment provider shows only what Order says.

let api: MockApi;

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  api.session = api.user("buyer.a@example.test");
  await api.install(page);
});

test("the provider's query string cannot mark an order paid", async ({ page }) => {
  const order = api.addOrder(api.session!);
  await page.goto(`/orders/payment-return?order_id=${order.id}&status=PAID&code=00&cancel=false`);
  await expect(page.getByText("Đang xác nhận thanh toán…")).toBeVisible();
  await page.waitForTimeout(2500);
  await expect(page.getByText("Thanh toán thành công")).toHaveCount(0);

  order.status = "paid"; // the verified webhook reached Order
  await expect(page.getByText("Thanh toán thành công")).toBeVisible({ timeout: 15_000 });
});

test("no confirmation in time is shown as unknown, not as failed", async ({ page }) => {
  const order = api.addOrder(api.session!);
  await page.clock.install();
  await page.goto(`/orders/payment-return?order_id=${order.id}`);
  await expect(page.getByText("Đang xác nhận thanh toán…")).toBeVisible();
  await page.clock.fastForward("02:05");
  await expect(page.getByText("Chưa nhận được xác nhận thanh toán")).toBeVisible();
  await expect(page.getByText(/đừng thanh toán lại/)).toBeVisible();
});

test("cancelling on the provider leaves the order unpaid; cancelling it uses Order's API", async ({
  page,
}) => {
  const order = api.addOrder(api.session!);
  await page.goto(`/orders/payment-cancel?order_id=${order.id}&cancel=true&status=CANCELLED`);
  await expect(page.getByText("Bạn chưa hoàn tất thanh toán")).toBeVisible();
  expect(api.cancelCalls).toHaveLength(0);
  await page.getByRole("button", { name: "Hủy đơn hàng" }).click();
  await page.getByRole("alertdialog").getByRole("button", { name: "Hủy đơn hàng" }).click();
  await expect(page.getByText("Đơn hàng đã hủy")).toBeVisible();
  expect(api.cancelCalls).toEqual([order.id]);
});

test("another buyer's order or a malformed id shows nothing of it", async ({ page }) => {
  const other = api.addOrder(api.user("buyer.b@example.test"), "paid");
  await page.goto(`/orders/payment-return?order_id=${other.id}`);
  await expect(page.getByText("Không xem được đơn hàng này")).toBeVisible();
  await expect(page.getByText("Thanh toán thành công")).toHaveCount(0);
  await page.goto("/orders/payment-return?order_id=../admin");
  await expect(page.getByText("Không xác định được đơn hàng")).toBeVisible();
});
