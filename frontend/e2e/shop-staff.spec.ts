import { expect, test, type Page } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid } from "./mock-api";

// AF-17: a staff member (a buyer account) sees, per shop, only the console
// pages their permissions open, never the owner's payout account; the
// invitation token travels only in the request body and the link is used
// once; a revoked membership leaves no shop to open.

let api: MockApi;

const shopA = {
  vendor_id: uuid(81),
  shop_name: "Shop Áo Thử",
  status: "approved",
  role: "staff",
  capabilities: ["orders.read", "orders.fulfill"],
  membership_version: 2,
};
const shopB = {
  vendor_id: uuid(82),
  shop_name: "Shop Giày Thử",
  status: "approved",
  role: "staff",
  capabilities: ["products.read"],
  membership_version: 1,
};

const nav = (page: Page, name: string) =>
  page.locator("aside").getByRole("link", { name, exact: true });

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
});

test("each shop shows only the pages its permissions open", async ({ page }) => {
  const staff = api.user("buyer.b@example.test");
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, ({ me }) => ({
    data: { shops: me?.id === staff.id ? [shopA, shopB] : [], staff_enabled: true },
  }));
  await login(page, staff.email);
  await page.goto("/vendor/orders");

  await expect(nav(page, "Đơn hàng")).toBeVisible();
  await expect(nav(page, "Sản phẩm")).toHaveCount(0);
  await expect(nav(page, "Tài khoản nhận tiền")).toHaveCount(0);
  await expect(nav(page, "Nhân viên")).toHaveCount(0);
  await expect(nav(page, "Cửa hàng")).toHaveCount(0);

  await page.getByRole("combobox").first().click();
  await page.getByRole("option", { name: "Shop Giày Thử" }).click();
  await expect(nav(page, "Sản phẩm")).toBeVisible();
  await expect(nav(page, "Đơn hàng")).toHaveCount(0);
  await expect(nav(page, "Tài khoản nhận tiền")).toHaveCount(0);
});

test("the invitation token stays out of URLs and a used link is explained", async ({ page }) => {
  const staff = api.user("buyer.b@example.test");
  const token = "A".repeat(43);
  let used = false;
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: { shops: used ? [shopA] : [], staff_enabled: true },
  }));
  api.on("POST", /^\/api\/vendor\/staff-invitations\/accept$/, ({ me, body }) => {
    if (body.token !== token) return { status: 404, code: "not_found", message: "Not found" };
    if (used) return { status: 409, code: "invitation_used", message: "Used" };
    used = true;
    return {
      data: {
        vendor_id: shopA.vendor_id,
        user_id: me!.id,
        role: "staff",
        status: "active",
        version: 1,
        permissions: shopA.capabilities,
        created_at: new Date().toISOString(),
      },
    };
  });
  await login(page, staff.email);
  await page.goto(`/staff-invitations/accept#token=${token}`);
  await expect(page.getByText(/Bạn đang đăng nhập là buyer\.b@example\.test/)).toBeVisible();
  expect(page.url()).not.toContain(token);
  await page.getByRole("button", { name: "Chấp nhận lời mời" }).click();
  await expect(page.getByText("Xem đơn hàng · Xử lý và giao đơn")).toBeVisible();
  expect(api.requests.some((r) => r.path.includes(token))).toBe(false);
  expect(api.sent("POST", /staff-invitations\/accept$/)).toEqual([{ token }]);

  // Opening the link again (a fresh page load: a hash-only change would not
  // reload the page).
  await page.goto("/");
  await page.goto(`/staff-invitations/accept#token=${token}`);
  await page.getByRole("button", { name: "Chấp nhận lời mời" }).click();
  await expect(page.getByRole("main").getByRole("alert")).toHaveText(
    "Lời mời này đã được sử dụng.",
  );

  await page.goto("/");
  await page.goto("/staff-invitations/accept#token=short");
  await expect(page.getByText(/Liên kết thiếu hoặc sai mã mời/)).toBeVisible();
});

test("a revoked membership leaves no shop to open", async ({ page }) => {
  const staff = api.user("buyer.b@example.test");
  let revoked = false;
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: { shops: revoked ? [] : [shopA], staff_enabled: true },
  }));
  await login(page, staff.email);
  await page.goto("/vendor/orders");
  await expect(nav(page, "Đơn hàng")).toBeVisible();

  revoked = true;
  await page.reload();
  await expect(
    page.getByText(
      "Bạn chưa là nhân viên của cửa hàng nào. Mở liên kết trong email mời để tham gia.",
    ),
  ).toBeVisible();
  await expect(nav(page, "Đơn hàng")).toHaveCount(0);
});
