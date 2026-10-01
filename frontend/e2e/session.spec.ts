import { expect, test, type Page } from "@playwright/test";

import { MockApi } from "./mock-api";

// FE-01: switching accounts never shows the previous account's data, and
// the session lives in an HttpOnly cookie (not in page storage).

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

test("logging out and in as another buyer shows only that buyer's orders", async ({ page }) => {
  const a = api.addOrder(api.user("buyer.a@example.test"));
  const b = api.addOrder(api.user("buyer.b@example.test"));
  await login(page, "buyer.a@example.test");
  await page.goto("/orders");
  await expect(page.getByText(`Đơn hàng #${a.id.slice(0, 8)}`)).toBeVisible();

  await page.getByRole("button", { name: /Buyer A/ }).click();
  await page.getByRole("menuitem", { name: "Đăng xuất" }).click();
  await expect(page.getByRole("button", { name: /Buyer A/ })).toHaveCount(0);

  await login(page, "buyer.b@example.test");
  await page.goto("/orders");
  await expect(page.getByText(`Đơn hàng #${b.id.slice(0, 8)}`)).toBeVisible();
  await expect(page.getByText(`Đơn hàng #${a.id.slice(0, 8)}`)).toHaveCount(0);

  // Nothing about the session is kept where page scripts can read it.
  const stored = await page.evaluate(() => JSON.stringify({ ...localStorage, ...sessionStorage }));
  expect(stored).not.toMatch(/token-|access_token|refresh/);
});

test("another buyer's order page shows nothing of it", async ({ page }) => {
  const b = api.addOrder(api.user("buyer.b@example.test"));
  api.session = api.user("buyer.a@example.test");
  await page.goto(`/orders/${b.id}`);
  await expect(page.getByText("Không thể tải đơn hàng này.")).toBeVisible();
  await expect(page.getByText("Người nhận thử")).toHaveCount(0);
});

test("a buyer opening the admin console is sent away without admin calls", async ({ page }) => {
  api.session = api.user("buyer.a@example.test");
  const adminCalls: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/admin")) adminCalls.push(r.url());
  });
  await page.goto("/admin");
  await expect(page).toHaveURL(/\/login/);
  expect(adminCalls.filter((u) => u.includes("/api/"))).toEqual([]);
});
