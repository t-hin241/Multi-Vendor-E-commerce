import { expect, test } from "@playwright/test";

import { MockApi } from "./mock-api";

// AF-09 on a phone: the bell sits next to the menu and the inbox fits the
// screen (no horizontal scroll), even with a long notice.
test("the inbox works on a phone", async ({ page }) => {
  const api = new MockApi();
  await api.install(page);
  const buyer = api.user("buyer.a@example.test");
  api.addNotice(buyer, "Một thông báo có tiêu đề rất dài ".repeat(4), "/orders");
  await page.goto("/login");
  await page.getByLabel("Email").fill("buyer.a@example.test");
  await page.getByLabel("Mật khẩu").fill("Fake-password-123");
  await page.getByRole("button", { name: "Đăng nhập" }).click();
  await expect(page).not.toHaveURL(/\/login/);

  const bell = page.getByRole("button", { name: "Thông báo, 1 chưa đọc" });
  await expect(bell).toBeVisible();
  await page.goto("/notifications");
  await expect(page.getByText(/Một thông báo có tiêu đề rất dài/)).toBeVisible();
  const overflow = await page.evaluate(
    () => document.documentElement.scrollWidth - window.innerWidth,
  );
  expect(overflow).toBeLessThanOrEqual(0);
});
