import { expect, test } from "@playwright/test";
import { login } from "./login";
import { MockApi, uuid } from "./mock-api";

const asOf = "2026-10-14T01:00:00.123456Z";
const range = { from: "2026-09-30T17:00:00Z", to: "2026-10-31T17:00:00Z", group: "day" };
const ratio = { numerator: 0, denominator: 0, rate: null };
const order = {
  source: "order",
  definition_version: "business-report-v1",
  as_of: asOf,
  range,
  currency: "VND",
  placed_orders: 2,
  vendor_orders: 3,
  gmv_ordered: 6000,
  paid_vendor_orders: 2,
  awaiting_fulfillment: 1,
  cancellation: { numerator: 1, denominator: 3, rate: 1 / 3 },
  paid_cancellation: ratio,
  returns: ratio,
  series: [{ bucket_start: range.from, placed_orders: 2, vendor_orders: 3, gmv_ordered: 6000 }],
  shops: [
    {
      vendor_id: uuid(900),
      vendor_orders: 3,
      gmv_ordered: 6000,
      cancellation: ratio,
      returns: ratio,
      insufficient_sample: true,
    },
  ],
};
const shipment = {
  source: "shipment",
  definition_version: "business-report-v1",
  as_of: asOf,
  range,
  fulfillable: 2,
  handed_off: 1,
  delivered: 0,
  returned: 0,
  lost: 0,
  cancelled: 0,
  late_handoff: ratio,
  handoff_hours: null,
  delivery_hours: null,
  series: [],
};

test("admin report keeps partial failures visible and drills with the same scope and cutoff", async ({
  page,
}) => {
  const api = new MockApi();
  api.addUser({
    id: uuid(30),
    email: "report.admin@example.test",
    full_name: "Report Admin",
    role: "admin",
  });
  await api.install(page);
  api.on("GET", /^\/api\/auth\/permissions$/, () => ({
    data: {
      permissions: ["analytics.read"],
      permission_version: 1,
      scoped: true,
      bundles: ["analytics.read"],
    },
  }));
  const periods: URLSearchParams[] = [];
  api.on("GET", /^\/api\/admin\/business-reports$/, ({ url }) => {
    periods.push(url.searchParams);
    const selected = {
      from: url.searchParams.get("from"),
      to: url.searchParams.get("to"),
      group: url.searchParams.get("group"),
      vendor_id: url.searchParams.get("vendor_id") || undefined,
    };
    return {
      data: {
        generated_at: asOf,
        definition_version: "business-report-v1",
        range: selected,
        order: { ...order, range: selected },
        payment: null,
        shipment: { ...shipment, range: selected },
        sources: [
          { name: "order", status: "ok", generated_at: asOf },
          { name: "payment", status: "unavailable", error: "refused (missing_permission)" },
          { name: "shipment", status: "ok", generated_at: asOf },
        ],
      },
    };
  });
  const drills: URLSearchParams[] = [];
  api.on("GET", /^\/api\/orders\/admin\/business-reports\/drilldown$/, ({ url }) => {
    drills.push(url.searchParams);
    const more = url.searchParams.has("cursor");
    return {
      data: {
        metric: "vendor_orders",
        cutoff: asOf,
        definition_version: "business-report-v1",
        items: [
          {
            id: uuid(more ? 922 : 921),
            vendor_id: uuid(900),
            kind: "vendor_order",
            status: "paid",
            subtotal_amount: more ? 4000 : 2000,
            currency: "VND",
            created_at: range.from,
          },
        ],
        next_cursor: more ? undefined : "fake-next-cursor",
      },
    };
  });
  await login(page, "report.admin@example.test");
  await page.goto("/admin/reports");
  await expect(page.getByRole("heading", { name: "Business reports", level: 2 })).toBeVisible();
  await expect(
    page.getByText("This source did not answer; its figures are not shown (not zero)."),
  ).toBeVisible();
  await expect(page.getByText("Not enough data")).toHaveCount(2);
  await expect(page.getByText("Not enough data").first()).toBeVisible();
  await page.getByLabel("From", { exact: true }).fill("2026-10-01");
  await page.getByLabel("To", { exact: true }).fill("2026-10-31");
  await page.getByLabel("Group by").selectOption("month");
  await page.getByLabel("Shop id (optional)").fill(uuid(900));
  await page.getByRole("button", { name: "Show", exact: true }).click();
  await expect.poll(() => periods.at(-1)?.get("group")).toBe("month");
  expect(periods.at(-1)?.get("from")).toBe("2026-09-30T17:00:00.000Z");
  expect(periods.at(-1)?.get("to")).toBe("2026-10-31T17:00:00.000Z");
  await page.getByRole("button", { name: /Shop packages \(sub-orders\)/ }).click();
  await expect(page.getByText(uuid(921), { exact: false }).first()).toBeVisible();
  await page.getByRole("button", { name: "Load more", exact: true }).click();
  await expect.poll(() => drills.length).toBe(2);
  for (const q of drills) {
    expect(q.get("cutoff")).toBe(asOf);
    expect(q.get("vendor_id")).toBe(uuid(900));
    expect(q.get("group")).toBe("month");
    expect(q.get("metric")).toBe("vendor_orders");
  }
  expect(drills[1]?.get("cursor")).toBe("fake-next-cursor");
});

test("shop analytics staff sees its report without fetching restricted money", async ({ page }) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(900);
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: {
      staff_enabled: true,
      shops: [
        {
          vendor_id: shop,
          shop_name: "Shop báo cáo",
          status: "approved",
          role: "staff",
          capabilities: ["analytics.read"],
          membership_version: 1,
        },
      ],
    },
  }));
  const scopes: string[] = [];
  api.on("GET", /^\/api\/orders\/vendor\/business-reports$/, ({ url }) => {
    scopes.push(url.searchParams.get("vendor_id") ?? "");
    return { data: { ...order, range: { ...range, vendor_id: shop }, shops: [] } };
  });
  api.on("GET", /^\/api\/shipments\/vendor\/business-reports$/, () => ({
    status: 503,
    code: "report_timeout",
    message: "Report timed out",
  }));
  await login(page, "buyer.b@example.test");
  await page.goto("/vendor/reports");
  await expect(page.getByRole("heading", { name: "Báo cáo kinh doanh" })).toBeVisible();
  await expect(page.getByText("Không có quyền", { exact: true })).toBeVisible();
  await expect(
    page.getByText("Nguồn này không trả lời; số liệu bị ẩn (không phải bằng 0)."),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: /Gói hàng của shop/ })).toBeVisible();
  expect(scopes.length).toBeGreaterThan(0);
  expect(scopes.every((id) => id === shop)).toBe(true);
  expect(
    api.requests.filter(
      (r) => r.path.includes("/api/payments/") && r.path.includes("business-reports"),
    ),
  ).toHaveLength(0);
  await expect(page.getByRole("link", { name: "Sao kê tài chính", exact: true })).toHaveCount(0);
});
