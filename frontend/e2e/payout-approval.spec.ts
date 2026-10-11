import { expect, test } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid, type MockUser } from "./mock-api";

// PW-027: a payout batch waits for an admin other than its creator; no
// transfer result can be recorded before. A batch paying a new account
// needs a second approval, confirmed with the password when AF-19 is on.

let api: MockApi;
const checker: MockUser = {
  id: uuid(41),
  email: "admin.checker@example.test",
  full_name: "Admin Checker",
  role: "admin",
};

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  api.addUser(checker);
  await api.install(page);
});

test("a batch is approved in two steps before transfers are recorded", async ({ page }) => {
  const now = new Date().toISOString();
  const batch = {
    id: uuid(800),
    idempotency_key: "fake-batch-key-800",
    currency: "VND",
    status: "pending",
    created_by: uuid(40),
    created_at: now,
    total_amount: 110000,
    approval_state: "awaiting_approval",
    required_approvals: 2,
    risk_reasons: ["new_destination"],
    items: [
      {
        id: uuid(801),
        batch_id: uuid(800),
        vendor_id: uuid(80),
        amount: 110000,
        currency: "VND",
        destination_account_id: uuid(802),
        destination_version: 1,
        destination_mask: "970400 ****1234",
        status: "pending",
        created_at: now,
      },
    ],
  };
  const proofs: unknown[] = [];
  api.on("GET", /^\/api\/payments\/admin\/settlements\/balances$/, () => ({ data: [] }));
  api.on("GET", /^\/api\/payments\/admin\/payouts\/batches$/, () => ({ data: [batch] }));
  api.on("GET", /^\/api\/payments\/admin\/payouts\/batches\/([0-9a-f-]{36})$/, () => ({
    data: batch,
  }));
  api.on("POST", /^\/api\/auth\/reauthentications$/, ({ body }) => {
    proofs.push(body);
    return { data: { proof: "fake-proof-not-a-secret", expires_at: now } };
  });
  api.on(
    "POST",
    /^\/api\/payments\/admin\/payouts\/batches\/([0-9a-f-]{36})\/approvals$/,
    ({ body }) => {
      if (body.expected_state !== batch.approval_state) {
        return { status: 409, code: "approval_changed", message: "Changed" };
      }
      if (batch.approval_state === "awaiting_second_approval" && !body.proof) {
        return { status: 403, code: "reauthentication_required", message: "Confirm your password" };
      }
      batch.approval_state =
        batch.approval_state === "awaiting_approval" ? "awaiting_second_approval" : "approved";
      return { data: batch };
    },
  );

  await login(page, checker.email);
  await page.goto("/admin/payouts");
  await expect(page.getByText(/Chờ duyệt cấp 1 trên 2 \(tài khoản nhận lần đầu\)/)).toBeVisible();
  await page.getByRole("button", { name: "Items" }).click();
  await expect(page.getByRole("button", { name: "Paid" })).toHaveCount(0);

  await page.getByRole("button", { name: "Duyệt lô" }).click();
  await expect(page.getByRole("button", { name: "Duyệt cấp 2" })).toBeVisible();
  await page.getByRole("button", { name: "Duyệt cấp 2" }).click();
  await page.getByLabel("Your current password").fill("Fake-password-123");
  await page.getByRole("button", { name: "Confirm" }).click();
  await expect(page.getByRole("button", { name: "Paid" })).toBeVisible();
  expect(proofs).toEqual([
    expect.objectContaining({
      purpose: "payment.payout_batch.approve",
      operation_hash: `payout_batch:${batch.id}:awaiting_second_approval`,
    }),
  ]);
  const approvals = api.requests.filter((r) => r.path.endsWith("/approvals"));
  expect(approvals.at(-1)?.body).toMatchObject({
    expected_state: "awaiting_second_approval",
    proof: "fake-proof-not-a-secret",
  });
});
