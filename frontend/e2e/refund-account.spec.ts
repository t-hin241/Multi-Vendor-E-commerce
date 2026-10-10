import { expect, test } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid, type MockUser } from "./mock-api";

// AF-06: a refund paid by bank transfer asks the buyer for the account on
// the order page. The page checks the account before sending, shows only
// the masked account back, lets the buyer give another one after a
// rejection, and never says the money is back before it is confirmed.

let api: MockApi;

type Destination = { version: number; masked: string; status: string; decision_reason?: string };

function refundsOf(api: MockApi, owner: MockUser, orderId: string) {
  const refund = {
    id: uuid(900),
    order_id: orderId,
    amount: 120_000,
    currency: "VND",
    status: "awaiting_provider_refund",
    stage: "awaiting_destination",
    destination: undefined as Destination | undefined,
    can_submit_destination: true,
    // transferring: finance claimed a transfer; the account cannot change.
    transferring: false,
    timeline: [{ event: "requested", at: new Date().toISOString() }],
    created_at: new Date().toISOString(),
  };
  api.on("GET", /^\/api\/payments\/refunds$/, ({ me, url }) => ({
    data: me?.id === owner.id && url.searchParams.get("order_id") === orderId ? [refund] : [],
  }));
  api.on(
    "POST",
    /^\/api\/payments\/refunds\/([0-9a-f-]{36})\/beneficiary$/,
    ({ me, body, match }) => {
      if (me?.id !== owner.id || match[1] !== refund.id) {
        return { status: 404, code: "not_found", message: "Refund not found" };
      }
      if (refund.transferring) {
        return { status: 409, code: "attempt_active", message: "A transfer is in progress" };
      }
      if (body.expected_version !== (refund.destination?.version ?? 0)) {
        return { status: 409, code: "destination_changed", message: "Destination changed" };
      }
      const number = String(body.account_number);
      refund.destination = {
        version: (refund.destination?.version ?? 0) + 1,
        masked: `${String(body.bank_code).toUpperCase()} ••••${number.slice(-4)}`,
        status: "pending_verification",
      };
      refund.stage = "verifying";
      refund.timeline.push({ event: "destination_submitted", at: new Date().toISOString() });
      return { data: refund };
    },
  );
  return refund;
}

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
});

test("the buyer gives a refund account, sees only the masked account, and gives another after a rejection", async ({
  page,
}) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "completed");
  const refund = refundsOf(api, buyer, String(order.id));
  await login(page, buyer.email);
  await page.goto(`/orders/${order.id}`);

  await expect(page.getByText("Cần thông tin tài khoản nhận tiền")).toBeVisible();
  await page.getByLabel("Mã ngân hàng").fill("FAKEBANK");
  await page.getByLabel("Số tài khoản").fill("12");
  await page.getByLabel("Tên chủ tài khoản").fill("Nguyen Van Test");
  await page.getByRole("button", { name: "Gửi thông tin tài khoản" }).click();
  await expect(page.getByText("Số tài khoản gồm 6-20 chữ số.")).toBeVisible();
  expect(api.sent("POST", /beneficiary$/)).toHaveLength(0);

  await page.getByLabel("Số tài khoản").fill("9704 0000 1111 2222");
  await page.getByRole("button", { name: "Gửi thông tin tài khoản" }).click();
  await expect(page.getByText("Đang xác minh tài khoản nhận tiền")).toBeVisible();
  await expect(page.getByText("Tài khoản nhận: FAKEBANK ••••2222")).toBeVisible();
  await expect(page.getByText(/9704\s?0000/)).toHaveCount(0);
  await expect(page.getByText("Đã hoàn tiền", { exact: true })).toHaveCount(0);
  expect(api.sent("POST", /beneficiary$/)[0]).toMatchObject({ expected_version: 0 });

  // Finance rejects the account; the buyer sees why and gives another.
  refund.stage = "destination_rejected";
  refund.destination = {
    ...refund.destination!,
    status: "rejected",
    decision_reason: "Tên không khớp với người mua",
  };
  await page.reload();
  await expect(page.getByText("Tài khoản nhận tiền chưa hợp lệ, vui lòng nhập lại")).toBeVisible();
  await expect(page.getByText("Lý do: Tên không khớp với người mua")).toBeVisible();
  await page.getByLabel("Mã ngân hàng").fill("FAKEBANK");
  await page.getByLabel("Số tài khoản").fill("9704000033334444");
  await page.getByLabel("Tên chủ tài khoản").fill("Nguyen Van Test");
  await page.getByRole("button", { name: "Gửi thông tin tài khoản" }).click();
  await expect(page.getByText("Tài khoản nhận: FAKEBANK ••••4444")).toBeVisible();
  expect(api.sent("POST", /beneficiary$/)[1]).toMatchObject({ expected_version: 1 });
});

test("a transfer in progress keeps the account and says why", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "completed");
  const refund = refundsOf(api, buyer, String(order.id));
  refund.stage = "verifying";
  refund.destination = { version: 1, masked: "FAKEBANK ••••2222", status: "pending_verification" };
  refund.transferring = true;
  await login(page, buyer.email);
  await page.goto(`/orders/${order.id}`);
  await page.getByRole("button", { name: "Đổi tài khoản nhận" }).click();
  await page.getByLabel("Mã ngân hàng").fill("FAKEBANK");
  await page.getByLabel("Số tài khoản").fill("9704000055556666");
  await page.getByLabel("Tên chủ tài khoản").fill("Nguyen Van Test");
  await page.getByRole("button", { name: "Gửi thông tin tài khoản" }).click();
  await expect(
    page.getByText("Sàn đang chuyển khoản cho yêu cầu này nên không thể đổi tài khoản lúc này."),
  ).toBeVisible();
  await expect(page.getByText("Tài khoản nhận: FAKEBANK ••••2222")).toBeVisible();
});

test("another buyer's order shows no refund", async ({ page }) => {
  const owner = api.user("buyer.a@example.test");
  const order = api.addOrder(owner, "completed");
  refundsOf(api, owner, String(order.id));
  await login(page, "buyer.b@example.test");
  await page.goto(`/orders/${order.id}`);
  await expect(page.getByText("Không thể tải đơn hàng này.")).toBeVisible();
  await expect(page.getByText("Hoàn tiền", { exact: true })).toHaveCount(0);
});
