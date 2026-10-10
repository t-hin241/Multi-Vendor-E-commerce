import { expect, test } from "@playwright/test";

import { login } from "./login";
import { MockApi, uuid, type MockUser } from "./mock-api";

// AF-01: a buyer opens a support case from the order page; a resend after a
// lost response carries the same Idempotency-Key, so the service returns
// the same case. A buyer without an order id sends a request on /support
// and later sees the marketplace's answer.

let api: MockApi;

const capability = {
  enabled: true,
  attachments_enabled: false,
  max_attachments: 0,
  max_attachment_bytes: 0,
  max_message_chars: 4000,
  reopen_window_days: 7,
  poll_interval_seconds: 30,
  pilot_only: false,
};

function supportCase(owner: MockUser, orderId: string, vendorOrderId: string, category: string) {
  const now = new Date().toISOString();
  return {
    id: uuid(700),
    order_id: orderId,
    vendor_order_id: vendorOrderId,
    vendor_id: uuid(80),
    buyer_id: owner.id,
    category,
    status: "open",
    policy_version: "support-v1",
    due_at: null,
    financial_hold: false,
    resolution_kind: null,
    resolution_ref: null,
    resolution_note: null,
    resolved_at: null,
    closed_at: null,
    version: 1,
    created_at: now,
    updated_at: now,
    messages: [],
    events: [],
  };
}

test.beforeEach(async ({ page }) => {
  api = new MockApi();
  await api.install(page);
  api.on("GET", /^\/api\/orders\/support-cases\/capability$/, () => ({ data: capability }));
});

test("a resend after a lost response opens one case, with the same key", async ({ page }) => {
  const buyer = api.user("buyer.a@example.test");
  const order = api.addOrder(buyer, "completed");
  const pkg = api.addPackage(order);
  const cases = new Map<string, ReturnType<typeof supportCase>>();
  let lose = true;
  api.on(
    "POST",
    /^\/api\/orders\/([0-9a-f-]{36})\/support-cases$/,
    ({ me, body, headers, match }) => {
      if (me?.id !== buyer.id || match[1] !== order.id) {
        return { status: 404, code: "not_found", message: "Order not found" };
      }
      const key = headers["idempotency-key"] ?? "";
      const c =
        cases.get(key) ??
        supportCase(buyer, String(order.id), String(body.vendor_order_id), String(body.category));
      cases.set(key, c);
      if (lose) {
        lose = false;
        return { abort: true };
      }
      return { status: 201, data: c };
    },
  );
  api.on("GET", /^\/api\/orders\/support-cases\/([0-9a-f-]{36})$/, ({ me, match }) => {
    const c = [...cases.values()].find((x) => x.id === match[1]);
    return c && me?.id === buyer.id
      ? { data: c }
      : { status: 404, code: "not_found", message: "Case not found" };
  });
  api.on("GET", /^\/api\/orders\/support-cases$/, ({ me }) => ({
    data: { items: me?.id === buyer.id ? [...new Set(cases.values())] : [], next_cursor: "" },
  }));

  await login(page, buyer.email);
  await page.goto(`/orders/${order.id}`);
  await page.getByRole("button", { name: "Cần hỗ trợ" }).click();
  await page.getByRole("combobox").click();
  await page.getByRole("option", { name: "Chưa nhận được hàng" }).click();
  await page.getByLabel("Mô tả").fill("Đơn báo đã giao nhưng tôi chưa nhận được.");
  await page.getByRole("button", { name: "Gửi yêu cầu" }).click();
  await expect(page.getByRole("alertdialog").locator(".text-destructive")).toBeVisible();

  await page.getByRole("button", { name: "Gửi yêu cầu" }).click();
  await expect(page).toHaveURL(new RegExp(`/support/${uuid(700)}$`));
  const sent = api.requests.filter((r) => r.method === "POST" && r.path.endsWith("/support-cases"));
  expect(sent).toHaveLength(2);
  expect(sent[0]!.headers["idempotency-key"]).toBeTruthy();
  expect(sent[1]!.headers["idempotency-key"]).toBe(sent[0]!.headers["idempotency-key"]);
  expect(sent[1]!.body).toMatchObject({ vendor_order_id: pkg.id, category: "not_received" });
  expect(cases.size).toBe(1);

  await page.goto("/support");
  await expect(page.getByText("Chưa nhận được hàng")).toBeVisible();
});

test("a request without an order id is sent, checked first, and shows the answer", async ({
  page,
}) => {
  const buyer = api.user("buyer.a@example.test");
  type Intake = {
    id: string;
    reference_kind: string;
    reference: string;
    message: string;
    status: string;
    close_reason?: string;
    version: number;
    created_at: string;
  };
  const intakes: Intake[] = [];
  api.on("GET", /^\/api\/orders\/support-cases$/, () => ({ data: { items: [], next_cursor: "" } }));
  api.on("GET", /^\/api\/orders\/support-intakes$/, ({ me }) => ({
    data: me?.id === buyer.id ? intakes : [],
  }));
  api.on("POST", /^\/api\/orders\/support-intakes$/, ({ me, body }) => {
    if (me?.id !== buyer.id) return { status: 401, code: "unauthorized", message: "Sign in" };
    const intake = {
      id: uuid(710 + intakes.length),
      reference_kind: String(body.reference_kind),
      reference: String(body.reference),
      message: String(body.message),
      status: "open",
      version: 1,
      created_at: new Date().toISOString(),
    };
    intakes.unshift(intake);
    return { status: 201, data: intake };
  });

  await login(page, buyer.email);
  await page.goto("/support");
  await expect(page.getByText("Bạn chưa có yêu cầu hỗ trợ nào")).toBeVisible();
  await page.getByRole("button", { name: "Gửi yêu cầu không có mã đơn" }).click();
  await page.getByLabel("Mã", { exact: true }).fill("ab");
  await page.getByLabel("Mô tả").fill("Tôi đã chuyển khoản nhưng không thấy đơn");
  await page.getByRole("button", { name: "Gửi yêu cầu" }).click();
  await expect(page.getByText(/^Mã gồm 3-100 ký tự/)).toBeVisible();
  expect(api.sent("POST", /support-intakes$/)).toHaveLength(0);

  await page.getByLabel("Mã", { exact: true }).fill("FAKE FT 0001");
  await page.getByRole("button", { name: "Gửi yêu cầu" }).click();
  await expect(page.getByText("FAKE FT 0001")).toBeVisible();
  await expect(page.getByText("Đang chờ sàn tra cứu")).toBeVisible();
  expect(api.sent("POST", /support-intakes$/)[0]).toEqual({
    reference_kind: "bank_transfer",
    reference: "FAKE FT 0001",
    message: "Tôi đã chuyển khoản nhưng không thấy đơn",
  });

  // The marketplace finds no payment and closes the request with a reason.
  Object.assign(intakes[0]!, {
    status: "closed",
    close_reason: "Không tìm thấy giao dịch khớp mã này",
    version: 2,
  });
  await page.reload();
  await expect(page.getByText("Đã đóng")).toBeVisible();
  await expect(page.getByText("Không tìm thấy giao dịch khớp mã này")).toBeVisible();
});
