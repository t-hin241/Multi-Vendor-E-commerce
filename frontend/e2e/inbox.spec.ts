import { expect, test, type Page } from "@playwright/test";

import { MockApi } from "./mock-api";

// AF-09: the header bell and /notifications. The inbox is per person,
// opening a notice marks it read and goes to its page, "read all" stops at
// the newest notice loaded, and the page handles empty, error and paging.

let api: MockApi;

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
});

async function login(page: Page, email: string) {
  await page.goto("/login");
  await page.getByLabel("Email").fill(email);
  await page.getByLabel("Mật khẩu").fill("Fake-password-123");
  await page.getByRole("button", { name: "Đăng nhập" }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

const bell = (page: Page) => page.getByRole("button", { name: /^Thông báo/ }).first();

test("the bell counts unread notices and opening one reads it and opens its page", async ({
  page,
}) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "paid");
  api.addNotice(buyer, "Đơn hàng đã được thanh toán", `/orders/${order.id}`);
  api.addNotice(buyer, "Đơn hàng đã giao cho đơn vị vận chuyển", `/orders/${order.id}`);
  await login(page, "buyer.a@example.test");

  await expect(bell(page)).toHaveAccessibleName("Thông báo, 2 chưa đọc");
  await bell(page).click();
  await page.getByRole("menuitem", { name: /đã được thanh toán/ }).click();
  await expect(page).toHaveURL(new RegExp(`/orders/${order.id}$`));
  await expect(bell(page)).toHaveAccessibleName("Thông báo, 1 chưa đọc");
});

test("another person's notices never show", async ({ page }) => {
  api.addNotice(api.user("buyer.a@example.test"), "Tin riêng của A", "/orders");
  api.addNotice(api.user("buyer.b@example.test"), "Tin riêng của B", "/orders");
  await login(page, "buyer.b@example.test");
  await page.goto("/notifications");
  await expect(page.getByText("Tin riêng của B")).toBeVisible();
  await expect(page.getByText("Tin riêng của A")).toHaveCount(0);
});

test("the page pages, reads all up to what was loaded, and hides a notice", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  for (let i = 1; i <= 25; i++) api.addNotice(buyer, `Thông báo số ${i}`, "/orders");
  await login(page, "buyer.a@example.test");
  await page.goto("/notifications");
  await expect(page.getByText("Thông báo số 25")).toBeVisible();
  await expect(page.getByText("Thông báo số 5", { exact: true })).toHaveCount(0);
  await page.getByRole("button", { name: "Xem thêm" }).click();
  await expect(page.getByText("Thông báo số 5", { exact: true })).toBeVisible();

  // A notice arriving after the page loaded stays unread.
  const late = api.addNotice(buyer, "Thông báo đến sau", "/orders");
  await page.getByRole("button", { name: "Đánh dấu tất cả đã đọc" }).click();
  await expect.poll(() => api.readMarkers.length).toBe(1);
  expect(late.read_at).toBeNull();

  await page.getByRole("button", { name: "Chưa đọc", exact: true }).click();
  await expect(page.getByText("Thông báo đến sau")).toBeVisible();
  await expect(page.getByText("Thông báo số 25")).toHaveCount(0);

  await page.getByRole("button", { name: "Tất cả", exact: true }).click();
  await page.getByRole("button", { name: "Ẩn thông báo" }).first().click();
  await expect(page.getByText("Thông báo đến sau")).toHaveCount(0);
});

test("empty, error and a disabled inbox", async ({ page }) => {
  await login(page, "buyer.a@example.test");
  await page.goto("/notifications");
  await expect(page.getByText("Chưa có thông báo nào")).toBeVisible();

  api.inboxFails = true;
  await page.reload();
  await expect(page.getByText("Không tải được thông báo.")).toBeVisible();

  api.inboxFails = false;
  api.inboxEnabled = false;
  await page.reload();
  await expect(page.getByText("Hộp thông báo chưa được bật.")).toBeVisible();
  await expect(page.getByRole("button", { name: /^Thông báo/ })).toHaveCount(0);
});
