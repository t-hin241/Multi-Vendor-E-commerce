import { describe, expect, it } from "vitest";

import { isSizeRelatedContext } from "./description-images";

describe("isSizeRelatedContext", () => {
  it("matches the literal English keyword", () => {
    expect(isSizeRelatedContext("Size chart below")).toBe(true);
  });

  it("matches Vietnamese size-related phrases", () => {
    expect(isSizeRelatedContext("Bảng kích thước sản phẩm")).toBe(true);
    expect(isSizeRelatedContext("Vui lòng xem kích cỡ trước khi đặt hàng")).toBe(true);
  });

  it("does not match unrelated marketing text", () => {
    expect(isSizeRelatedContext("Thiết kế thanh lịch, phối màu tinh tế")).toBe(false);
  });

  it("is case-insensitive", () => {
    expect(isSizeRelatedContext("SIZE CHART")).toBe(true);
  });

  it("handles empty text", () => {
    expect(isSizeRelatedContext("")).toBe(false);
  });
});
