import { expect, test } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid, type MockUser } from "./mock-api";

// AF-05: an approved return shows the shop's verified return address, the
// return code and the dispatch deadline; the buyer reports the carrier and
// tracking number once (same key on a retry). A late parcel is still
// accepted, and nothing says the money is back.

let api: MockApi;

function approvedReturn(owner: MockUser, order: Record<string, unknown>) {
  const items = order.items as { id: string }[];
  const now = new Date().toISOString();
  const r = {
    id: uuid(500),
    order_id: String(order.id),
    order_item_id: items[0]!.id,
    reason: "Sai màu",
    status: "approved",
    quantity: 1,
    refund_amount: 120_000,
    policy_version: "returns-v1",
    created_at: now,
    updated_at: now,
    version: 3,
    authorization_version: 1,
    return_code: "RT-FAKE-0001",
    shipping_status: "awaiting_dispatch",
    fee_payer: "seller",
    dispatch_deadline: new Date(Date.now() + 3 * 86_400_000).toISOString(),
    dispatch_overdue: false,
  };
  order.returns = [r];
  const instructions = () => ({
    return_id: r.id,
    return_code: r.return_code,
    authorization_version: r.authorization_version,
    version: r.version,
    dispatch_deadline: r.dispatch_deadline,
    dispatch_overdue: r.dispatch_overdue,
    fee_payer: r.fee_payer,
    address: {
      recipient_name: "Kho Shop Thử",
      phone: "0911000000",
      province: "Hà Nội",
      district: "Cầu Giấy",
      ward: "Dịch Vọng",
      street_address: "9 Đường Kho",
      receiving_hours: "8:00-17:00",
    },
    instructions: "Ghi mã trả hàng lên kiện.",
    shipping_status: r.shipping_status,
    carrier_name: (r as Record<string, unknown>).carrier_name,
    tracking_number: (r as Record<string, unknown>).tracking_number,
  });
  api.on(
    "GET",
    /^\/api\/orders\/return-requests\/([0-9a-f-]{36})\/shipping-instructions$/,
    ({ me, match }) =>
      me?.id === owner.id && match[1] === r.id
        ? { data: instructions() }
        : { status: 404, code: "not_found", message: "Not found" },
  );
  api.on(
    "POST",
    /^\/api\/orders\/return-requests\/([0-9a-f-]{36})\/dispatches$/,
    ({ me, body }) => {
      if (me?.id !== owner.id) return { status: 404, code: "not_found", message: "Not found" };
      if (!/^[A-Za-z0-9._-]{3,64}$/.test(String(body.tracking_number))) {
        return { status: 422, code: "invalid_tracking", message: "Invalid tracking number" };
      }
      if (body.expected_version !== r.version)
        return { status: 409, code: "version_conflict", message: "Changed" };
      Object.assign(r, {
        shipping_status: "awaiting_verification",
        carrier_name: body.carrier_name,
        tracking_number: body.tracking_number,
        version: r.version + 1,
      });
      return { data: r };
    },
  );
  return r;
}

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
});

test("the buyer sees where to send the return and reports the parcel", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "completed");
  approvedReturn(buyer, order);
  await login(page, buyer.email);
  await page.goto(`/orders/${order.id}`);

  await expect(page.getByText("Chờ bạn gửi hàng trả · Mã trả hàng RT-FAKE-0001")).toBeVisible();
  await expect(page.getByText(/Gửi tới: Kho Shop Thử \(0911000000\), 9 Đường Kho/)).toBeVisible();
  await expect(page.getByText(/^Hạn gửi:/)).toBeVisible();

  await page.getByRole("button", { name: "Báo đã gửi hàng" }).click();
  await expect(page.getByText("Nhập đơn vị vận chuyển và mã vận đơn.")).toBeVisible();
  await page.getByLabel("Đơn vị vận chuyển").fill("Giao Hàng Thử");
  await page.getByLabel("Mã vận đơn").fill("bad code!");
  await page.getByRole("button", { name: "Báo đã gửi hàng" }).click();
  await expect(page.getByText(/Mã vận đơn không hợp lệ/)).toBeVisible();

  await page.getByLabel("Mã vận đơn").fill("FAKE123456");
  await page.getByRole("button", { name: "Báo đã gửi hàng" }).click();
  await expect(page.getByText("Đã gửi qua Giao Hàng Thử, mã vận đơn FAKE123456")).toBeVisible();
  await expect(page.getByText(/Đã gửi, chờ người bán nhận hàng/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Báo đã gửi hàng" })).toHaveCount(0);
  await expect(page.getByText("Đã hoàn tiền", { exact: true })).toHaveCount(0);

  const sent = api.requests.filter((r) => r.path.endsWith("/dispatches"));
  expect(sent).toHaveLength(2);
  expect(sent[1]!.headers["idempotency-key"]).toBe(sent[0]!.headers["idempotency-key"]);
  expect(sent[1]!.body).toMatchObject({
    carrier_name: "Giao Hàng Thử",
    tracking_number: "FAKE123456",
    expected_version: 3,
  });
});

test("a late parcel can still be reported and a missing address is explained", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "completed");
  const r = approvedReturn(buyer, order);
  Object.assign(r, {
    dispatch_overdue: true,
    dispatch_deadline: new Date(Date.now() - 86_400_000).toISOString(),
  });
  await login(page, buyer.email);
  await page.goto(`/orders/${order.id}`);
  await expect(page.getByText(/đã quá hạn, bạn vẫn có thể gửi; sàn sẽ xem xét/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Báo đã gửi hàng" })).toBeVisible();

  Object.assign(r, { shipping_status: "destination_missing", authorization_version: 0 });
  await page.reload();
  await expect(page.getByText("Sàn đang chuẩn bị địa chỉ nhận trả.")).toBeVisible();
  await expect(page.getByText(/Mã trả hàng/)).toHaveCount(0);
});
