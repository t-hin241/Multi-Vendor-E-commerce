import { expect, test } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid } from "./mock-api";

// AF-19: the admin console shows only the pages an admin's bundles open,
// and a page opened anyway shows the service's refusal. With approvals on,
// recording a refund result prepares a draft request for a second admin
// instead of changing the money.

let api: MockApi;

const admin = {
  id: uuid(30),
  email: "admin.read@example.test",
  full_name: "Admin Read",
  role: "admin",
};

function permissions(list: string[]) {
  api.on("GET", /^\/api\/auth\/permissions$/, () => ({
    data: { permissions: list, permission_version: 1, scoped: true, bundles: list },
  }));
}

const refund = {
  id: uuid(950),
  payment_intent_id: uuid(951),
  order_id: uuid(952),
  amount: 120_000,
  currency: "VND",
  reason: "Hàng lỗi",
  status: "awaiting_provider_refund",
  requested_by: uuid(1),
  created_at: new Date().toISOString(),
  updated_at: new Date().toISOString(),
};

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  api.addUser(admin);
  await api.install(page);
});

test("a read-only admin sees only audit and is refused money pages", async ({ page }) => {
  permissions(["audit.read"]);
  api.on("GET", /^\/api\/payments\/admin\/refunds$/, () => ({
    status: 403,
    code: "missing_permission",
    message: "This action needs the finance.read permission",
  }));
  await login(page, admin.email);
  await page.goto("/admin/refunds");
  const aside = page.locator("aside");
  await expect(aside.getByRole("link", { name: "Audit log" })).toBeVisible();
  await expect(aside.getByRole("link", { name: "Refunds" })).toHaveCount(0);
  await expect(aside.getByRole("link", { name: "Vendor payouts" })).toHaveCount(0);
  await expect(page.getByText("This action needs the finance.read permission")).toBeVisible();
  await expect(page.getByRole("button", { name: "Failed" })).toHaveCount(0);
});

test("with approvals on, a refund result becomes a draft for a second admin", async ({ page }) => {
  permissions(["finance.read", "finance.prepare"]);
  api.on("GET", /^\/api\/payments\/admin\/refunds$/, () => ({ data: [refund] }));
  api.on("POST", /^\/api\/payments\/admin\/refunds\/([0-9a-f-]{36})\/resolve$/, () => ({
    status: 409,
    code: "approval_required",
    message: "This action needs a second admin: create an approval request instead",
  }));
  api.on("POST", /^\/api\/payments\/admin\/approval-requests$/, ({ body }) => ({
    status: 201,
    data: { id: uuid(960), status: "draft", ...body },
  }));
  await login(page, admin.email);
  await page.goto("/admin/refunds");
  await page.getByRole("button", { name: "Failed" }).click();
  await page.getByPlaceholder("Reason…").fill("Ngân hàng trả về: sai tên");
  await page.getByRole("button", { name: "Record failure" }).click();
  await expect(
    page.getByText(/A second admin must approve this\. Draft request 00000960 was prepared/),
  ).toBeVisible();
  expect(api.sent("POST", /approval-requests$/)).toEqual([
    {
      operation_kind: "refund_resolution",
      target_id: refund.id,
      payload: { outcome: "failed", note: "Ngân hàng trả về: sai tên" },
      reason: "Ngân hàng trả về: sai tên",
    },
  ]);
  expect(refund.status).toBe("awaiting_provider_refund");
});
