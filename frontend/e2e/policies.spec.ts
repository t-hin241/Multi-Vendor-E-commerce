import { expect, test } from "@playwright/test";

import { MockApi, uuid } from "./mock-api";

// AF-02: checkout names the exact policy versions the order is placed
// under and sends them back; a version published meanwhile is refused
// (409 policy_changed), the page shows the new one and the buyer places the
// order again. An order keeps the rules it was placed under.

let api: MockApi;
let version = 2;

const snapshot = (returnsVersion: number, windowDays: number) => ({
  source: "published",
  policies: [
    {
      kind: "returns",
      policy_id: uuid(400 + returnsVersion),
      version: returnsVersion,
      content_hash: "fake-hash",
    },
    { kind: "terms", policy_id: uuid(450), version: 1, content_hash: "fake-hash-terms" },
  ],
  returns_window_days: windowDays,
  return_shipping_refund: "none",
  return_policy_version: `returns-v${returnsVersion}`,
  taken_at: new Date().toISOString(),
});

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  api.session = api.user("buyer.a@example.test");
  version = 2;
  api.on("POST", /^\/api\/orders\/checkout\/preview$/, () => ({
    data: {
      cart_version: api.cartVersion,
      currency: "VND",
      subtotal_amount: 120_000,
      shipping_amount: 30_000,
      total_amount: 150_000,
      ready: true,
      vendors: [
        {
          vendor_id: uuid(80),
          subtotal_amount: 120_000,
          shipping_fee_amount: 30_000,
          item_count: 1,
        },
      ],
      policy_versions: { returns: version, terms: 1 },
      policies: snapshot(version, version === 2 ? 7 : 15),
    },
  }));
  await api.install(page);
});

test("the order is placed under the versions shown, and a newer one is reviewed first", async ({
  page,
}) => {
  const accepted: unknown[] = [];
  let publishOnce = true;
  api.on("POST", /^\/api\/orders\/checkout$/, ({ me, body }) => {
    accepted.push(body.accepted_policy_versions);
    if (publishOnce) {
      publishOnce = false;
      version = 3; // published while the buyer was on the page
      return { status: 409, code: "policy_changed", message: "A newer policy is in force" };
    }
    const order = api.addOrder(me!);
    return { status: 201, data: order };
  });

  await page.goto("/checkout");
  await expect(page.getByText(/Yêu cầu trả hàng trong 7 ngày/)).toBeVisible();
  const v2 = page.getByRole("link", { name: "Chính sách đổi trả và hoàn tiền (phiên bản 2)" });
  await expect(v2).toHaveAttribute("href", "/policies/returns/v/2");

  await page.getByRole("button", { name: /^Đặt hàng ·/ }).click();
  await expect(page.getByText(/Chính sách của sàn vừa có phiên bản mới/)).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Chính sách đổi trả và hoàn tiền (phiên bản 3)" }),
  ).toBeVisible();
  await expect(page.getByText(/Yêu cầu trả hàng trong 15 ngày/)).toBeVisible();
  expect(api.orders.size).toBe(0);

  await page.getByRole("button", { name: /^Đặt hàng ·/ }).click();
  await expect(page).toHaveURL(/\/orders\/[0-9a-f-]{36}$/);
  expect(accepted).toEqual([
    { returns: 2, terms: 1 },
    { returns: 3, terms: 1 },
  ]);
});

test("an order keeps the return rules it was placed under", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "completed");
  api.on("GET", /^\/api\/orders\/([0-9a-f-]{36})\/policy-snapshot$/, ({ me, match }) =>
    me?.id === buyer.id && match[1] === order.id
      ? { data: { order_id: order.id, legacy: false, order: snapshot(2, 7), vendor_orders: [] } }
      : { status: 404, code: "not_found", message: "Not found" },
  );
  version = 3; // a newer policy is in force today
  await page.goto(`/orders/${order.id}`);
  await expect(page.getByText(/Yêu cầu trả hàng trong 7 ngày kể từ khi nhận hàng/)).toBeVisible();
  await expect(
    page.getByRole("link", { name: "Chính sách đổi trả và hoàn tiền — phiên bản 2" }),
  ).toBeVisible();
});
