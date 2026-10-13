import { expect, test } from "@playwright/test";
import { login } from "./login";
import { MockApi, uuid } from "./mock-api";

test("inventory staff retries the same receipt after a lost response and reads movement history", async ({
  page,
}) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(880),
    item = uuid(881);
  let available = 10,
    version = 1;
  const submitted: { key: string; body: unknown }[] = [];
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: {
      staff_enabled: true,
      shops: [
        {
          vendor_id: shop,
          shop_name: "Kho thử nghiệm",
          status: "approved",
          role: "staff",
          capabilities: ["inventory.read", "inventory.adjust"],
          membership_version: 1,
        },
      ],
    },
  }));
  api.on("GET", /^\/api\/inventory\/items\/mine$/, () => ({
    data: [
      {
        id: item,
        product_id: uuid(882),
        variant_id: null,
        vendor_id: shop,
        available_quantity: available,
        reserved_quantity: 2,
        version,
        alert_enabled: false,
        low_stock_threshold: 0,
      },
    ],
  }));
  api.on("GET", /^\/api\/inventory\/vendor\/self-service-policy$/, () => ({
    data: { enabled: true, feature_enabled: true, version: 1, limits: { receipt: 10000 } },
  }));
  api.on("GET", /^\/api\/inventory\/vendor\/movements$/, () => ({
    data: {
      items: [
        {
          id: uuid(885),
          kind: "receipt",
          quantity_delta: 4,
          reserved_delta: 0,
          reason: "Nhập theo phiếu kiểm thử",
          evidence_ref: "fake-note-001",
          created_at: "2026-10-13T00:00:00Z",
        },
      ],
    },
  }));
  api.on("POST", /^\/api\/inventory\/vendor\/stock-adjustments$/, ({ body, headers }) => {
    submitted.push({ key: headers["idempotency-key"] ?? "", body });
    if (submitted.length === 1) {
      available = 14;
      version = 2;
      return { abort: true };
    }
    return {
      data: {
        operation_id: uuid(884),
        status: "applied",
        available,
        reserved: 2,
        on_hand: 16,
        version,
        status_url: `/api/inventory/vendor/stock-adjustments/${uuid(884)}`,
      },
    };
  });
  await login(page, "buyer.b@example.test");
  await page.goto("/vendor/inventory");
  await expect(
    page.locator("aside").getByRole("link", { name: "Tồn kho", exact: true }),
  ).toBeVisible();
  await expect(
    page.locator("aside").getByRole("link", { name: "Sản phẩm", exact: true }),
  ).toHaveCount(0);
  await page.getByRole("button", { name: "Điều chỉnh kho và lịch sử" }).click();
  await page.getByLabel("Số lượng điều chỉnh", { exact: true }).fill("4");
  await page.getByLabel("Lý do điều chỉnh", { exact: true }).fill("Nhập theo phiếu kiểm thử");
  await page.getByLabel("Mã chứng từ", { exact: true }).fill("fake-note-001");
  await page.getByRole("button", { name: "Ghi nhận phiếu", exact: true }).click();
  await expect(page.getByRole("button", { name: "Thử lại cùng phiếu" })).toBeVisible();
  await expect(page.getByLabel("Số lượng điều chỉnh", { exact: true })).toBeDisabled();
  await page.getByRole("button", { name: "Thử lại cùng phiếu" }).click();
  await expect(
    page.getByRole("status").filter({ hasText: "Đã ghi nhận. Tồn khả dụng: 14." }),
  ).toBeVisible();
  expect(submitted).toHaveLength(2);
  expect(submitted[0]).toEqual(submitted[1]);
  expect(submitted[0]?.key).toBeTruthy();
  await expect(page.getByText("fake-note-001", { exact: false })).toBeVisible();
});

test("disabled policy keeps adjustments unavailable and preserves history", async ({ page }) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(890);
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: {
      staff_enabled: true,
      shops: [
        {
          vendor_id: shop,
          shop_name: "Kho chỉ đọc",
          status: "approved",
          role: "staff",
          capabilities: ["inventory.read"],
          membership_version: 1,
        },
      ],
    },
  }));
  api.on("GET", /^\/api\/inventory\/items\/mine$/, () => ({
    data: [
      {
        id: uuid(891),
        product_id: uuid(892),
        vendor_id: shop,
        available_quantity: 10,
        reserved_quantity: 0,
        version: 1,
      },
    ],
  }));
  api.on("GET", /^\/api\/inventory\/vendor\/self-service-policy$/, () => ({
    data: { enabled: false, feature_enabled: false, version: 0, limits: { receipt: 10000 } },
  }));
  api.on("GET", /^\/api\/inventory\/vendor\/movements$/, () => ({ data: { items: [] } }));
  await login(page, "buyer.b@example.test");
  await page.goto("/vendor/inventory");
  await page.getByRole("button", { name: "Điều chỉnh kho và lịch sử" }).click();
  await expect(page.getByRole("button", { name: "Ghi nhận phiếu" })).toHaveCount(0);
  await expect(page.getByText("Chưa có biến động.")).toBeVisible();
});
