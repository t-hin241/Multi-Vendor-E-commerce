import { expect, test } from "@playwright/test";
import { login } from "./login";
import { MockApi, uuid } from "./mock-api";

test("a paused shop stays public while new purchases are blocked and the cart line stays", async ({
  page,
}) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(800);
  const availability = {
    accepting_orders: false,
    message: "Shop nghỉ lễ, đơn cũ vẫn giao.",
    next_open_at: "2026-10-20T01:00:00Z",
  };
  api.on("GET", new RegExp(`^/api/vendor/public/${shop}$`), () => ({
    data: { id: shop, shop_name: "Shop nghỉ lễ", description: "Shop thử nghiệm", availability },
  }));
  api.on("GET", /^\/api\/catalog\/products$/, () => ({
    data: { products: [], total: 0, vendor_info_degraded: false, sales_info_degraded: false },
  }));
  api.products.set("ao-nghi-le", {
    id: uuid(60),
    vendor_id: shop,
    category_id: uuid(90),
    name: "Áo nghỉ lễ",
    slug: "ao-nghi-le",
    description: "Áo cotton",
    price_amount: 120000,
    currency: "VND",
    status: "approved",
    is_active: true,
    images: [],
    variants: [],
    created_at: "2026-10-01T00:00:00Z",
    updated_at: "2026-10-01T00:00:00Z",
  });
  api.cartItems[0] = { ...api.cartItems[0], state: "shop_paused", available: false };
  await login(page, "buyer.a@example.test");
  await page.goto(`/shop/${shop}`);
  await expect(page.getByRole("heading", { name: "Shop nghỉ lễ", exact: true })).toBeVisible();
  await expect(
    page.getByText("Shop đang tạm nghỉ, chưa nhận đơn mới", { exact: true }),
  ).toBeVisible();
  await page.goto("/products/ao-nghi-le");
  await expect(
    page.getByText("Shop đang tạm nghỉ, chưa nhận đơn mới", { exact: true }).first(),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Thêm vào giỏ hàng", exact: true })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Mua ngay", exact: true })).toHaveCount(0);
  await page.goto("/cart");
  await expect(page.getByText("Áo thun thử nghiệm").first()).toBeVisible();
  await expect(page.getByText("Shop đang tạm nghỉ", { exact: true }).first()).toBeVisible();
  expect(api.checkoutRequests).toHaveLength(0);
  expect(api.cartItems).toHaveLength(1);
});

test("staff can pause and resume with a version while handing off an existing paid order", async ({
  page,
}) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(800),
    vendorOrder = uuid(810),
    shipmentId = uuid(820);
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: {
      staff_enabled: true,
      shops: [
        {
          vendor_id: shop,
          shop_name: "Shop nghỉ lễ",
          status: "approved",
          role: "staff",
          capabilities: ["shop.availability.write", "orders.read", "orders.fulfill"],
          membership_version: 1,
        },
      ],
    },
  }));
  let paused = false,
    version = 0;
  const state = () => ({
    vendor_id: shop,
    shop_status: "approved",
    accepting_orders: !paused,
    availability_version: version,
    sale_version: version + 1,
    enforcement_pending: paused,
    enabled: true,
    current: paused
      ? {
          id: uuid(830),
          status: "active",
          starts_at: "2026-10-14T01:00:00Z",
          message: "Nghỉ lễ",
          updated_at: "2026-10-14T01:00:00Z",
        }
      : undefined,
  });
  api.on("GET", new RegExp(`^/api/vendor/${shop}/availability$`), () => ({ data: state() }));
  api.on("PUT", new RegExp(`^/api/vendor/${shop}/availability$`), ({ body }) => {
    expect(body.expected_version).toBe(version);
    paused = true;
    version++;
    return { status: 202, data: state() };
  });
  api.on("DELETE", new RegExp(`^/api/vendor/${shop}/availability$`), ({ url }) => {
    expect(url.searchParams.get("expected_version")).toBe(String(version));
    paused = false;
    version++;
    return { status: 202, data: state() };
  });
  const vo = {
    id: vendorOrder,
    order_id: uuid(811),
    vendor_id: shop,
    status: "paid",
    subtotal_amount: 120000,
    shipping_fee_amount: 0,
    total_amount: 120000,
    currency: "VND",
    items: [],
    created_at: "2026-10-13T01:00:00Z",
    updated_at: "2026-10-13T01:00:00Z",
  };
  const shipment = {
    id: shipmentId,
    vendor_order_id: vendorOrder,
    vendor_id: shop,
    status: "ready_to_ship",
    tracking_number: "",
    failed_attempts: 0,
    created_at: vo.created_at,
  };
  api.on("GET", /^\/api\/orders\/vendor\/mine$/, () => ({ data: [vo] }));
  api.on("GET", /^\/api\/shipments\/mine$/, () => ({ data: [shipment] }));
  api.on(
    "GET",
    /^\/api\/orders\/vendor\/(cancellation-requests|return-requests|delivery-exceptions)$/,
    () => ({ data: [] }),
  );
  api.on("POST", new RegExp(`^/api/shipments/${shipmentId}/ship$`), ({ body }) => {
    expect(paused).toBe(true);
    shipment.status = "shipped";
    shipment.tracking_number = String(body.tracking_number);
    vo.status = "shipped";
    return { data: shipment };
  });
  await login(page, "buyer.b@example.test");
  await page.goto("/vendor/availability");
  await page.getByLabel("Lời nhắn cho khách (tối đa 500 ký tự)").fill("Nghỉ lễ");
  await page.getByRole("button", { name: "Lưu lịch tạm nghỉ" }).click();
  await expect(page.getByText("Đang tạm nghỉ", { exact: true })).toBeVisible();
  await expect(page.getByText("Đang đồng bộ", { exact: true })).toBeVisible();
  await page.goto("/vendor/orders");
  await page.getByLabel("Mã vận đơn", { exact: true }).fill("FAKE-TRACKING-001");
  await page.getByRole("button", { name: "Đã giao cho vận chuyển", exact: true }).click();
  await expect(page.getByText("Mã vận đơn FAKE-TRACKING-001", { exact: false })).toBeVisible();
  expect(api.sent("POST", /\/ship$/)).toEqual([{ tracking_number: "FAKE-TRACKING-001" }]);
  await page.goto("/vendor/availability");
  await page.getByRole("button", { name: "Mở bán lại ngay" }).click();
  await expect(page.getByText("Đang nhận đơn", { exact: true })).toBeVisible();
  expect(version).toBe(2);
});
