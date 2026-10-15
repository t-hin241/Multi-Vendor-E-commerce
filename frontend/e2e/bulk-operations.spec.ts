import { readFile } from "node:fs/promises";
import { expect, test } from "@playwright/test";
import { login } from "./login";
import { API, MockApi, uuid } from "./mock-api";

const priceTemplate = {
  kind: "prices",
  version: 1,
  exportable: true,
  enabled: true,
  max_rows: 1000,
  max_bytes: 5242880,
  notes: ["Unchanged rows are skipped."],
  columns: [
    { name: "sku", required: false, description: "SKU", example: "AO-DO-L" },
    { name: "product_id", required: false, description: "Product" },
    { name: "price_amount", required: true, description: "Price", example: "189000" },
    { name: "expected_version", required: false, description: "Version", example: "3" },
    { name: "product_name", required: false, read_only: true, description: "Reference" },
  ],
};

test("product staff previews a price file, retries a lost upload, confirms a re-used file and gets the error report", async ({
  page,
}) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(900),
    job = uuid(901),
    earlier = uuid(902);
  const uploadKeys: string[] = [];
  let reads = 0;
  let confirmed = false;
  const summary = {
    total: 3,
    invalid: 1,
    unchanged: 1,
    pending: 1,
    succeeded: 0,
    needs_approval: 0,
    failed: 0,
    cancelled: 0,
  };
  const jobBody = (status: string) => ({
    id: job,
    vendor_id: shop,
    kind: "prices",
    template_version: 1,
    status,
    outcome: status === "completed" ? "partial" : undefined,
    file_name: "gia-thang-10.csv",
    file_bytes: 120,
    header: ["sku", "price_amount"],
    preview_hash: "fake-preview-hash",
    preview_at: "2026-10-14T01:00:00Z",
    duplicate_of: earlier,
    version: status === "preview_ready" ? 2 : 3,
    summary: status === "completed" ? { ...summary, pending: 0, succeeded: 1 } : summary,
    created_at: "2026-10-14T00:59:00Z",
  });
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: {
      staff_enabled: true,
      shops: [
        {
          vendor_id: shop,
          shop_name: "Shop giá",
          status: "approved",
          role: "staff",
          capabilities: ["products.read", "products.write"],
          membership_version: 1,
        },
      ],
    },
  }));
  api.on("GET", /^\/api\/catalog\/vendor\/import-templates\/prices$/, () => ({
    data: priceTemplate,
  }));
  api.on("GET", /^\/api\/catalog\/vendor\/import-jobs$/, () => ({
    data: {
      items: uploadKeys.length > 1 ? [jobBody(confirmed ? "completed" : "preview_ready")] : [],
    },
  }));
  api.on("POST", /^\/api\/catalog\/vendor\/import-jobs$/, ({ headers }) => {
    uploadKeys.push(headers["idempotency-key"] ?? "");
    if (uploadKeys.length === 1) return { abort: true };
    return { status: 202, data: jobBody("uploaded") };
  });
  api.on("GET", new RegExp(`^/api/catalog/vendor/import-jobs/${job}$`), () => {
    reads++;
    if (confirmed) return { data: jobBody("completed") };
    return { data: jobBody(reads === 1 ? "validating" : "preview_ready") };
  });
  api.on("GET", new RegExp(`^/api/catalog/vendor/import-jobs/${job}/rows$`), () => ({
    data: {
      items: [
        {
          row_no: 1,
          status: confirmed ? "succeeded" : "valid",
          values: ["AO-DO-L", "150000"],
          old_values: { price_amount: 120000, status: "approved" },
          new_values: { price_amount: 150000, requires_review: true },
        },
        { row_no: 2, status: "unchanged", values: ["AO-XANH", "130000"] },
        {
          row_no: 3,
          status: "invalid",
          values: ["KHAC", "10"],
          error: { code: "target_not_found", field: "sku", message: "SKU not found in this shop" },
        },
      ],
    },
  }));
  api.on("POST", new RegExp(`^/api/catalog/vendor/import-jobs/${job}/confirm$`), ({ body }) => {
    confirmed = true;
    return { data: { ...jobBody("confirmed"), body } };
  });
  const reportURL = `${API}/fake-signed/import-errors.csv`;
  api.on("POST", new RegExp(`^/api/catalog/vendor/import-jobs/${job}/report-downloads$`), () => ({
    data: { url: reportURL, expires_at: "2026-10-14T01:01:00Z" },
  }));
  await page.route(reportURL, (route) =>
    route.fulfill({
      status: 200,
      headers: {
        "Content-Type": "text/csv",
        "Content-Disposition": 'attachment; filename="import-errors.csv"',
      },
      body: "row_no,sku,price_amount,status,error_code\n3,KHAC,10,invalid,target_not_found\n",
    }),
  );

  await login(page, "buyer.b@example.test");
  await page.goto("/vendor/bulk");
  await expect(page.getByRole("heading", { name: "Nhập/xuất hàng loạt" })).toBeVisible();
  await expect(
    page.locator("aside").getByRole("link", { name: "Nhập/xuất hàng loạt", exact: true }),
  ).toBeVisible();
  await expect(page.getByRole("button", { name: "Nhập kho" })).toHaveCount(0);

  // The template is built in the browser: importable columns only.
  const templateDownload = page.waitForEvent("download");
  await page.getByRole("button", { name: "Tải mẫu CSV" }).click();
  const template = await readFile((await (await templateDownload).path())!, "utf8");
  expect(template).toBe("﻿sku,product_id,price_amount,expected_version\r\nAO-DO-L,,189000,3\r\n");

  await page.getByLabel("Tệp CSV").setInputFiles({
    name: "gia-thang-10.csv",
    mimeType: "text/csv",
    buffer: Buffer.from("sku,price_amount\nAO-DO-L,150000\nAO-XANH,130000\nKHAC,10\n"),
  });
  await page.getByRole("button", { name: "Tải lên và kiểm tra" }).click();
  await expect(page.getByText("Không nhận được phản hồi", { exact: false })).toBeVisible();
  await page.getByRole("button", { name: "Thử lại cùng tệp" }).click();
  await expect(page.getByText("Đã tải lên. Hệ thống đang kiểm tra từng dòng.")).toBeVisible();
  expect(uploadKeys).toHaveLength(2);
  expect(uploadKeys[0]).toBeTruthy();
  expect(uploadKeys[1]).toBe(uploadKeys[0]);

  // Polling moves the job from validating to the preview.
  const detail = page.getByLabel("Chi tiết lượt nhập");
  await expect(detail.getByRole("heading")).toContainText("Đã kiểm tra, chờ xác nhận");
  await expect(detail.getByText("SKU not found in this shop")).toBeVisible();
  await expect(detail.getByText("Giá: 120000 · Trạng thái: approved")).toBeVisible();
  const confirm = detail.getByRole("button", { name: "Xác nhận áp dụng 1 dòng" });
  await expect(confirm).toBeDisabled();
  await detail.getByRole("checkbox").check();
  await confirm.click();
  await expect(detail.getByRole("heading")).toContainText("Hoàn tất · Thành công một phần");
  const [sent] = api.sent("POST", new RegExp(`/import-jobs/${job}/confirm$`));
  expect(sent).toEqual({
    preview_hash: "fake-preview-hash",
    expected_version: 2,
    acknowledge_duplicate_file: true,
  });

  const report = page.waitForEvent("download");
  await detail.getByRole("button", { name: "Tải báo cáo dòng lỗi" }).click();
  expect((await report).suggestedFilename()).toBe("import-errors.csv");
});

test("stock viewer without adjust permission sees the stock template but cannot upload", async ({
  page,
}) => {
  const api = new MockApi();
  await api.install(page);
  const shop = uuid(910);
  api.on("GET", /^\/api\/vendor\/accessible-shops$/, () => ({
    data: {
      staff_enabled: true,
      shops: [
        {
          vendor_id: shop,
          shop_name: "Kho xem",
          status: "approved",
          role: "staff",
          capabilities: ["inventory.read"],
          membership_version: 1,
        },
      ],
    },
  }));
  api.on("GET", /^\/api\/inventory\/vendor\/import-templates\/stock_receipts$/, () => ({
    data: {
      kind: "stock_receipts",
      version: 1,
      exportable: true,
      enabled: false,
      disabled_reason: "Stock self-service is not enabled for this shop",
      max_rows: 1000,
      max_bytes: 5242880,
      notes: [],
      columns: [{ name: "quantity", required: true, description: "Units received" }],
    },
  }));
  api.on("GET", /^\/api\/inventory\/vendor\/import-jobs$/, () => ({ data: { items: [] } }));
  await login(page, "buyer.b@example.test");
  await page.goto("/vendor/bulk");
  await expect(page.getByRole("button", { name: "Nhập kho" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Giá bán" })).toHaveCount(0);
  await expect(
    page.getByRole("alert").filter({ hasText: "Stock self-service is not enabled" }),
  ).toBeVisible();
  await expect(page.getByLabel("Tệp CSV")).toHaveCount(0);
  await expect(page.getByText("Bạn chỉ có quyền xem; không thể nhập dữ liệu.")).toBeVisible();
  await expect(page.getByText("Chưa có lượt nhập nào.")).toBeVisible();
});
