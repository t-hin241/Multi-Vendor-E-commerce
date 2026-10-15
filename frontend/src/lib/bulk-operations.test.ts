import { describe, expect, it } from "vitest";
import {
  type BulkTemplate,
  csvCell,
  describeValues,
  errorText,
  isActive,
  ownerOf,
  precheck,
  templateCsv,
} from "./bulk-operations";
import { canOpen } from "./shop-access";

const template: BulkTemplate = {
  kind: "prices",
  version: 1,
  columns: [
    { name: "sku", required: false, description: "", example: "AO-1" },
    { name: "price_amount", required: true, description: "", example: "189000" },
    { name: "product_name", required: false, read_only: true, description: "" },
  ],
  notes: [],
  exportable: true,
  enabled: true,
  max_rows: 1000,
  max_bytes: 5242880,
};

describe("bulk operations", () => {
  it("routes each kind to the service that owns its data", () => {
    expect(ownerOf("prices")).toBe("catalog");
    expect(ownerOf("products")).toBe("catalog");
    expect(ownerOf("stock_receipts")).toBe("inventory");
  });
  it("writes a UTF-8 template without read-only columns", () => {
    expect(templateCsv(template)).toBe("\uFEFFsku,price_amount\r\nAO-1,189000\r\n");
  });
  it("neutralizes formulas and quotes CSV cells", () => {
    expect(csvCell("=1+1")).toBe("'=1+1");
    expect(csvCell("-5")).toBe("'-5");
    expect(csvCell('a,"b"')).toBe('"a,""b"""');
    expect(csvCell("Áo thun")).toBe("Áo thun");
  });
  it("refuses files the server would refuse", () => {
    const file = (name: string, size: number) => new File([new Uint8Array(size)], name);
    expect(precheck(file("gia.csv", 10), template)).toBeNull();
    expect(precheck(file("gia.xlsx", 10), template)).toMatch(/csv/);
    expect(precheck(file("gia.csv", 0), template)).toMatch(/trống/);
    expect(precheck(file("gia.csv", 5242881), template)).toMatch(/5 MB/);
  });
  it("polls only while the server is working", () => {
    const job = (status: string) => ({ status }) as Parameters<typeof isActive>[0];
    expect(isActive(job("validating"))).toBe(true);
    expect(isActive(job("applying"))).toBe(true);
    expect(isActive(job("preview_ready"))).toBe(false);
    expect(isActive(job("completed"))).toBe(false);
  });
  it("explains known errors and keeps the server detail", () => {
    expect(errorText("file_invalid", "Unknown column")).toContain("Unknown column");
    expect(errorText("preview_stale", "")).toMatch(/thay đổi/);
    expect(errorText("other", "Server text")).toBe("Server text");
    expect(describeValues({ price_amount: 150, requires_review: true, sku: "" })).toBe(
      "Giá: 150 · Cần duyệt lại: có",
    );
  });
  it("opens the page with product or stock access", () => {
    expect(
      canOpen("/vendor/bulk", { role: "staff", capabilities: ["inventory.read"] }, "buyer"),
    ).toBe(true);
    expect(canOpen("/vendor/bulk", { role: "staff", capabilities: ["orders.read"] }, "buyer")).toBe(
      false,
    );
  });
});
