import { expect, test, type Page } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid, type MockUser } from "./mock-api";

// AF-03: the buyer asks to cancel a paid package that is not handed over.
// The panel follows the request step by step and says the money is back
// only once the refund is confirmed; a package handed over meanwhile, a
// disabled feature and a shop that cannot fulfil are explained.

let api: MockApi;

type Request = {
  id: string;
  order_id: string;
  vendor_order_id: string;
  vendor_id: string;
  origin: "buyer" | "vendor";
  reason_code: string;
  reason: string;
  status: string;
  policy_version: string;
  decision_reason?: string;
  version: number;
  created_at: string;
  updated_at: string;
};

function cancellations(owner: MockUser, orderId: string) {
  const list: Request[] = [];
  const outcome: { refuse?: { code: string; message: string } } = {};
  api.on("GET", /^\/api\/orders\/([0-9a-f-]{36})\/cancellation-requests$/, ({ me, match }) => ({
    data: me?.id === owner.id && match[1] === orderId ? list : [],
  }));
  api.on(
    "POST",
    /^\/api\/orders\/vendor-orders\/([0-9a-f-]{36})\/cancellation-requests$/,
    ({ me, body, match }) => {
      if (me?.id !== owner.id)
        return { status: 404, code: "not_found", message: "Package not found" };
      if (outcome.refuse) return { status: 409, ...outcome.refuse };
      const now = new Date().toISOString();
      const r: Request = {
        id: uuid(800 + list.length),
        order_id: orderId,
        vendor_order_id: match[1]!,
        vendor_id: uuid(80),
        origin: "buyer",
        reason_code: String(body.reason_code),
        reason: String(body.reason),
        status: "requested",
        policy_version: "cancel-v1",
        version: 1,
        created_at: now,
        updated_at: now,
      };
      list.push(r);
      return { status: 201, data: r };
    },
  );
  return { list, outcome };
}

async function openOrder(page: Page, buyer: MockUser, orderId: string) {
  await login(page, buyer.email);
  await page.goto(`/orders/${orderId}`);
}

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
});

test("the buyer asks to cancel a paid package and follows it to the confirmed refund", async ({
  page,
}) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "paid");
  const pkg = api.addPackage(order, "paid");
  const { list } = cancellations(buyer, String(order.id));
  await openOrder(page, buyer, String(order.id));

  await page.getByRole("button", { name: "Yêu cầu hủy Gói hàng 1" }).click();
  await page.getByRole("button", { name: "Gửi yêu cầu hủy" }).click();
  await expect(page.getByText("Vui lòng cho biết lý do.")).toBeVisible();
  await page.getByPlaceholder("Mô tả ngắn").fill("Tôi đặt nhầm màu");
  await page.getByRole("button", { name: "Gửi yêu cầu hủy" }).click();
  await expect(
    page.getByText("Gói hàng 1 · Đã tiếp nhận yêu cầu hủy, đang chờ sàn xem xét"),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /^Yêu cầu hủy/ })).toHaveCount(0);
  const sent = api.requests.filter(
    (r) => r.method === "POST" && r.path.includes("/cancellation-requests"),
  );
  expect(sent).toHaveLength(1);
  expect(sent[0]!.path).toContain(pkg.id);
  expect(sent[0]!.headers["idempotency-key"]).toBeTruthy();
  expect(sent[0]!.body).toEqual({ reason_code: "changed_mind", reason: "Tôi đặt nhầm màu" });

  list[0]!.status = "refund_pending";
  await page.reload();
  await expect(page.getByText("Gói hàng 1 · Đã hủy giao, đang hoàn tiền")).toBeVisible();
  await expect(
    page.getByText("Khoản hoàn tiền được thông báo riêng khi được xác nhận."),
  ).toBeVisible();
  await expect(page.getByText(/Đã hủy và hoàn tiền/)).toHaveCount(0);

  list[0]!.status = "resolved";
  await page.reload();
  await expect(page.getByText("Gói hàng 1 · Đã hủy và hoàn tiền")).toBeVisible();
});

test("a package handed over meanwhile and a disabled feature are explained", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "paid");
  api.addPackage(order, "paid");
  const { outcome } = cancellations(buyer, String(order.id));
  outcome.refuse = { code: "already_shipped", message: "Already shipped" };
  await openOrder(page, buyer, String(order.id));

  await page.getByRole("button", { name: "Yêu cầu hủy Gói hàng 1" }).click();
  await page.getByPlaceholder("Mô tả ngắn").fill("Giao chậm quá");
  await page.getByRole("button", { name: "Gửi yêu cầu hủy" }).click();
  await expect(
    page.getByText(/Gói hàng đã được giao cho vận chuyển\. Bạn có thể yêu cầu trả hàng/),
  ).toBeVisible();

  outcome.refuse = { code: "paid_cancellation_disabled", message: "Disabled" };
  await page.getByRole("button", { name: "Gửi yêu cầu hủy" }).click();
  await expect(page.getByText(/Chưa hỗ trợ hủy đơn đã thanh toán trực tuyến/)).toBeVisible();
});

test("a shop that cannot fulfil the package is shown, and a shipped package offers no cancel", async ({
  page,
}) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "paid");
  const first = api.addPackage(order, "paid");
  api.addPackage(order, "shipped");
  const { list } = cancellations(buyer, String(order.id));
  const now = new Date().toISOString();
  list.push({
    id: uuid(850),
    order_id: String(order.id),
    vendor_order_id: first.id,
    vendor_id: uuid(80),
    origin: "vendor",
    reason_code: "out_of_stock",
    reason: "Hết hàng",
    status: "approved",
    policy_version: "cancel-v1",
    version: 3,
    created_at: now,
    updated_at: now,
  });
  await openOrder(page, buyer, String(order.id));
  await expect(page.getByText("Gói hàng 1 · Đã dừng giao")).toBeVisible();
  await expect(page.getByText("Người bán báo không thể giao gói này.")).toBeVisible();
  await expect(page.getByRole("button", { name: /^Yêu cầu hủy/ })).toHaveCount(0);
});
