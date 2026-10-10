import { expect, test, type Page } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid, type MockUser } from "./mock-api";

// AF-04: a package that could not be delivered. The buyer is asked before
// any redelivery and may pick a saved address; declining asks for a
// refund, shown as pending until confirmed. A refund decided meanwhile
// refuses the redelivery with an explanation.

let api: MockApi;

function exceptionOf(owner: MockUser, order: { id: unknown }, vendorOrderId: string) {
  const now = new Date().toISOString();
  const d = {
    id: uuid(600),
    order_id: String(order.id),
    vendor_order_id: vendorOrderId,
    vendor_id: uuid(80),
    shipment_id: uuid(610),
    exception_type: "attempts_exhausted",
    current_shipment_id: uuid(610),
    attempt_no: 1,
    carrier_outcome: "attempts_exhausted",
    failed_attempts: 2,
    status: "awaiting_buyer",
    resolution: undefined as string | undefined,
    redelivery_address: undefined as Record<string, string> | undefined,
    policy_version: "delivery-v1",
    version: 4,
    created_at: now,
    updated_at: now,
  };
  const state: { locked: boolean } = { locked: false };
  api.on("GET", /^\/api\/orders\/([0-9a-f-]{36})\/delivery-exceptions$/, ({ me, match }) => ({
    data: me?.id === owner.id && match[1] === d.order_id ? [d] : [],
  }));
  api.on(
    "POST",
    /^\/api\/orders\/delivery-exceptions\/([0-9a-f-]{36})\/redelivery-consents$/,
    ({ me, body, match }) => {
      if (me?.id !== owner.id || match[1] !== d.id)
        return { status: 404, code: "not_found", message: "Not found" };
      if (state.locked) return { status: 409, code: "resolution_locked", message: "Refund chosen" };
      if (body.expected_version !== d.version)
        return { status: 409, code: "version_conflict", message: "Changed" };
      d.version++;
      if (body.accept) {
        d.status = "redelivery_pending";
        d.resolution = "redelivery";
        d.redelivery_address = {
          recipient_name: "Người nhận thử",
          street_address: "1 Đường Thử",
          district: "Ba Đình",
          province: "Hà Nội",
        };
      } else {
        d.status = "refund_pending";
        d.resolution = "refund";
      }
      return { data: d };
    },
  );
  return { d, state };
}

async function openOrder(page: Page, buyer: MockUser, orderId: string) {
  await login(page, buyer.email);
  await page.goto(`/orders/${orderId}`);
}

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
});

test("the buyer accepts a redelivery to a saved address", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "shipped");
  const pkg = api.addPackage(order, "shipped");
  exceptionOf(buyer, order, pkg.id);
  await openOrder(page, buyer, String(order.id));

  await expect(
    page.getByText("Gói hàng 1 · Sàn đề nghị giao lại, đang chờ bạn xác nhận"),
  ).toBeVisible();
  await page.getByRole("radio").nth(1).check();
  await page.getByRole("button", { name: "Đồng ý giao lại" }).click();
  await expect(page.getByText("Gói hàng 1 · Đang giao lại kiện hàng")).toBeVisible();
  await expect(page.getByText(/Giao lại tới: Người nhận thử, 1 Đường Thử/)).toBeVisible();
  expect(api.sent("POST", /redelivery-consents$/)).toEqual([
    { accept: true, address_id: uuid(70), expected_version: 4 },
  ]);
});

test("declining asks for a refund that stays pending until confirmed", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "shipped");
  const pkg = api.addPackage(order, "shipped");
  exceptionOf(buyer, order, pkg.id);
  await openOrder(page, buyer, String(order.id));

  await page.getByRole("button", { name: "Không nhận, tôi muốn hoàn tiền" }).click();
  await expect(page.getByText("Gói hàng 1 · Đang hoàn tiền cho kiện hàng")).toBeVisible();
  await expect(
    page.getByText("Khoản hoàn tiền được thông báo riêng khi được xác nhận."),
  ).toBeVisible();
  expect(api.sent("POST", /redelivery-consents$/)[0]).toEqual({
    accept: false,
    expected_version: 4,
  });
});

test("a refund decided meanwhile refuses the redelivery with a reason", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "shipped");
  const pkg = api.addPackage(order, "shipped");
  const { state } = exceptionOf(buyer, order, pkg.id);
  state.locked = true;
  await openOrder(page, buyer, String(order.id));

  await page.getByRole("button", { name: "Đồng ý giao lại" }).click();
  await expect(page.getByText("Kiện này đã chọn hoàn tiền nên không thể giao lại.")).toBeVisible();
});

test("a lost parcel is said so", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "shipped");
  const pkg = api.addPackage(order, "shipped");
  const { d } = exceptionOf(buyer, order, pkg.id);
  Object.assign(d, { status: "investigating", carrier_outcome: "lost", exception_type: "lost" });
  await openOrder(page, buyer, String(order.id));
  await expect(page.getByText("Đơn vị vận chuyển xác nhận kiện hàng bị thất lạc.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Đồng ý giao lại" })).toHaveCount(0);
});
