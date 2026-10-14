import { expect, test } from "@playwright/test";
import { login } from "./login";
import { MockApi, uuid } from "./mock-api";

test("finance staff sees reconciliation, retries the same export and cannot download after revocation", async ({
  page,
}) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(890),
    operation = uuid(891);
  const keys: string[] = [];
  const snapshot = {
    vendor_id: shop,
    filters: { from: "2026-10-01T00:00:00Z", to: "2026-10-14T00:00:00Z", currency: "VND" },
    balances: {
      opening: 100,
      credit: 1000,
      debit: 200,
      closing: 900,
      held: 100,
      not_eligible: 0,
      payable: 800,
      in_payout: 0,
    },
    balances_available: true,
    gap_count: 0,
    entries: [],
    as_of: "2026-10-14T00:00:00Z",
    watermark: null,
  };
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: {
      staff_enabled: true,
      shops: [
        {
          vendor_id: shop,
          shop_name: "Shop tài chính",
          status: "approved",
          role: "staff",
          capabilities: ["finance.read"],
          membership_version: 1,
        },
      ],
    },
  }));
  api.on("GET", new RegExp(`/api/payments/vendor/${shop}/statement-capabilities$`), () => ({
    data: { statements_enabled: true },
  }));
  api.on("GET", new RegExp(`/api/payments/vendor/${shop}/statements$`), () => ({ data: snapshot }));
  api.on("POST", new RegExp(`/api/payments/vendor/${shop}/statement-exports$`), ({ headers }) => {
    keys.push(headers["idempotency-key"] ?? "");
    if (keys.length === 1) return { abort: true };
    return {
      data: {
        operation_id: operation,
        status: "pending",
        expires_at: "2026-10-15T00:00:00Z",
        snapshot,
      },
    };
  });
  api.on("GET", new RegExp(`/api/payments/vendor/${shop}/statement-exports/${operation}$`), () => ({
    data: {
      operation_id: operation,
      status: "ready",
      expires_at: "2026-10-15T00:00:00Z",
      snapshot,
    },
  }));
  api.on(
    "POST",
    new RegExp(`/api/payments/vendor/${shop}/statement-exports/${operation}/download-links$`),
    () => ({
      status: 403,
      code: "permission_denied",
      message: "Quyền tài chính đã bị thu hồi",
    }),
  );
  await login(page, "buyer.b@example.test");
  await page.goto("/vendor/statements");
  await expect(page.getByRole("heading", { name: "Sao kê tài chính · VND" })).toBeVisible();
  await expect(page.getByText("Số dư đầu kỳ", { exact: true })).toBeVisible();
  await expect(
    page.locator("aside").getByRole("link", { name: "Tài khoản nhận tiền", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Xuất CSV", exact: true }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "Không nhận được phản hồi" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Xuất CSV", exact: true }).click();
  await expect(page.getByRole("button", { name: "Tải CSV (liên kết 60 giây)" })).toBeVisible();
  expect(keys).toHaveLength(2);
  expect(keys[0]).toBeTruthy();
  expect(keys[1]).toBe(keys[0]);
  await page.getByRole("button", { name: "Tải CSV (liên kết 60 giây)" }).click();
  await expect(
    page.getByRole("alert").filter({ hasText: "Quyền tài chính đã bị thu hồi" }),
  ).toBeVisible();
});
