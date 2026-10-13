import { expect, test } from "@playwright/test";
import { API, APP, MockApi, uuid } from "./mock-api";

function setup() {
  const api = new MockApi();
  api.session = api.user("buyer.a@example.test");
  api.cartItems[0]!.vendor_id = uuid(80);
  api.cartItems.push({
    ...api.cartItems[0],
    line_id: uuid(51),
    vendor_id: uuid(81),
    product_id: uuid(61),
    product_name: "Món để dành",
    state: "out_of_stock",
    available: false,
    stock_status: "out_of_stock",
  });
  api.on("GET", /^\/api\/cart$/, () => ({
    data: {
      cart_id: uuid(90),
      selective_checkout_enabled: true,
      version: api.cartVersion,
      items: api.cartItems,
      subtotal: { amount: 240000, currency: "VND" },
      total: 240000,
      currency: "VND",
      mixed_currency: false,
      item_count: api.cartItems.length,
      line_count: api.cartItems.length,
      unavailable_lines: 1,
      price_changed_lines: 0,
      over_line_limit: false,
      checkout_ready: false,
      degraded: { catalog: false, inventory: false },
      limits: { max_lines: 50, max_quantity_per_line: 999 },
      page: { limit: 50, offset: 0, total: api.cartItems.length },
    },
  }));
  api.on("GET", /^\/api\/orders\/checkout\/capabilities$/, () => ({
    data: { selective_checkout_enabled: true },
  }));
  api.on("POST", /^\/api\/orders\/checkout\/preview$/, ({ body }) => {
    expect(body.selected_line_ids).toEqual([uuid(50)]);
    expect(body.cart_version).toBe(api.cartVersion);
    return {
      data: {
        selected_line_ids: [uuid(50)],
        cart_version: api.cartVersion,
        currency: "VND",
        subtotal_amount: 120000,
        shipping_amount: 30000,
        total_amount: 150000,
        ready: true,
        vendors: [
          {
            vendor_id: uuid(80),
            subtotal_amount: 120000,
            shipping_fee_amount: 30000,
            item_count: 1,
          },
        ],
      },
    };
  });
  api.on("POST", /^\/api\/orders\/checkout$/, ({ body, me }) => {
    expect(body.selected_line_ids).toEqual([uuid(50)]);
    expect(body.expected_total_amount).toBe(150000);
    const order = api.addOrder(me!);
    api.cartItems = api.cartItems.filter((line) => line.line_id !== uuid(50));
    api.cartVersion++;
    return { data: order, status: 201 };
  });
  return api;
}

test("select a shop, persist across pages, and leave unpurchased items in cart", async ({
  page,
}) => {
  const api = setup();
  await api.install(page);
  await page.goto("/cart");
  await page.getByRole("checkbox", { name: "Chọn shop 2", exact: true }).uncheck();
  await expect(page.getByText("Đã chọn 1 sản phẩm · Còn 1 sản phẩm để mua sau.")).toBeVisible();
  await expect(page.getByRole("button", { name: /^Đặt hàng ·/ })).toContainText("150.000");
  await page.goto("/checkout");
  await expect(page.getByRole("checkbox", { name: "Chọn shop 2", exact: true })).not.toBeChecked();
  await page.reload();
  await expect(page.getByRole("checkbox", { name: "Chọn shop 2", exact: true })).not.toBeChecked();
  await page.getByRole("button", { name: /^Đặt hàng ·/ }).click();
  await expect(page).toHaveURL(/\/orders\/[0-9a-f-]{36}$/);
  expect(api.cartItems.map((line) => line.line_id)).toEqual([uuid(51)]);
  await page.goto("/cart");
  await expect(page.getByText("Đã chọn 0 sản phẩm · Còn 1 sản phẩm để mua sau.")).toBeVisible();
});

test("empty selection cannot place an order and selection remains isolated by buyer", async ({
  page,
}) => {
  const api = setup();
  await api.install(page);
  await page.goto("/checkout");
  await page.getByRole("checkbox", { name: "Chọn tất cả", exact: true }).uncheck();
  await expect(page.getByText("Đã chọn 0 sản phẩm · Còn 2 sản phẩm để mua sau.")).toBeVisible();
  await expect(page.getByRole("button", { name: "Cần xử lý giỏ hàng" })).toBeDisabled();
  api.session = api.user("buyer.b@example.test");
  await page.reload();
  await expect(page.getByRole("checkbox", { name: "Chọn tất cả", exact: true })).toBeChecked();
  expect(api.orders.size).toBe(0);
});

test("a late preview cannot make a newer blocked selection placeable", async ({ page }) => {
  const api = setup();
  await api.install(page);
  let started!: () => void;
  let finish!: () => void;
  const requested = new Promise<void>((resolve) => {
    started = resolve;
  });
  const release = new Promise<void>((resolve) => {
    finish = resolve;
  });
  await page.route(`${API}/api/orders/checkout/preview`, async (route) => {
    if (route.request().method() !== "POST") return route.fallback();
    started();
    await release;
    await route.fulfill({
      status: 200,
      headers: { "Access-Control-Allow-Origin": APP, "Access-Control-Allow-Credentials": "true" },
      contentType: "application/json",
      body: JSON.stringify({
        data: {
          cart_version: 1,
          currency: "VND",
          subtotal_amount: 120000,
          shipping_amount: 30000,
          total_amount: 150000,
          ready: true,
          vendors: [
            {
              vendor_id: uuid(80),
              subtotal_amount: 120000,
              shipping_fee_amount: 30000,
              item_count: 1,
            },
          ],
        },
      }),
    });
  });
  await page.goto("/checkout");
  await page.getByRole("checkbox", { name: "Chọn shop 2", exact: true }).uncheck();
  await requested;
  await page.getByRole("checkbox", { name: "Chọn shop 2", exact: true }).check();
  const response = page.waitForResponse(
    (r) => r.url().endsWith("/checkout/preview") && r.request().method() === "POST",
  );
  finish();
  await response;
  await expect(page.getByRole("button", { name: "Cần xử lý giỏ hàng" })).toBeDisabled();
  await expect(page.getByRole("button", { name: /^Đặt hàng ·/ })).toHaveCount(0);
  expect(api.orders.size).toBe(0);
});
