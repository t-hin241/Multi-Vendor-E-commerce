import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api-client";
import {
  needsReview,
  noticesDisabled,
  preferencesErrorMessage,
  toggleCategory,
  vendorActionLabel,
} from "@/lib/vendor-notices";

describe("toggleCategory", () => {
  it("adds and removes once, in a stable order", () => {
    expect(toggleCategory([], "finance", true)).toEqual(["finance"]);
    expect(toggleCategory(["finance"], "orders", true)).toEqual(["orders", "finance"]);
    expect(toggleCategory(["orders", "finance"], "orders", true)).toEqual(["orders", "finance"]);
    expect(toggleCategory(["orders", "finance"], "orders", false)).toEqual(["finance"]);
  });
});

describe("errors and labels", () => {
  it("recognises the disabled feature", () => {
    expect(noticesDisabled(new ApiError(404, "feature_disabled", "off"))).toBe(true);
    expect(noticesDisabled(new ApiError(404, "not_found", "x"))).toBe(false);
    expect(noticesDisabled(new Error("x"))).toBe(false);
  });
  it("explains a conflict", () => {
    expect(preferencesErrorMessage(new ApiError(409, "conflict", "x"))).toContain("Tải lại");
  });
  it("labels actions and review states", () => {
    expect(vendorActionLabel("new_order")).toBe("New paid order");
    expect(vendorActionLabel("something_new")).toBe("something new");
    expect(needsReview("no_recipient")).toBe(true);
    expect(needsReview("parked")).toBe(true);
    expect(needsReview("resolved")).toBe(false);
  });
});
