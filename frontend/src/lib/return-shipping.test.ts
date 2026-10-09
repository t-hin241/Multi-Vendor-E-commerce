import { describe, expect, it } from "vitest";

import { ApiError } from "@/lib/api-client";
import {
  buyerShippingLabel,
  canAuthorizeShipping,
  canDispatch,
  receiptError,
  returnShippingErrorMessage,
} from "@/lib/return-shipping";

describe("return shipping steps", () => {
  it("dispatches only an authorized, not yet sent return", () => {
    expect(canDispatch({ status: "approved", shipping_status: "awaiting_dispatch" })).toBe(true);
    expect(canDispatch({ status: "approved", shipping_status: "awaiting_verification" })).toBe(
      false,
    );
    expect(canDispatch({ status: "approved", shipping_status: "destination_missing" })).toBe(false);
    expect(canDispatch({ status: "received", shipping_status: "awaiting_dispatch" })).toBe(false);
  });
  it("authorizes (or corrects) only before dispatch", () => {
    expect(canAuthorizeShipping({ status: "approved", shipping_status: undefined })).toBe(true);
    expect(canAuthorizeShipping({ status: "approved", shipping_status: "awaiting_dispatch" })).toBe(
      true,
    );
    expect(
      canAuthorizeShipping({ status: "approved", shipping_status: "awaiting_verification" }),
    ).toBe(false);
  });
});

describe("receiptError", () => {
  it("needs every unit once, never more", () => {
    expect(receiptError(2, { sellable: 1, damaged: 1, missing: 0 })).toBeNull();
    expect(receiptError(2, { sellable: 3, damaged: 0, missing: 0 })).not.toBeNull();
    expect(receiptError(2, { sellable: 1, damaged: 0, missing: 0 })).not.toBeNull();
    expect(receiptError(2, { sellable: -1, damaged: 3, missing: 0 })).not.toBeNull();
  });
});

describe("wording", () => {
  it("never says refunded from the parcel alone", () => {
    expect(buyerShippingLabel("received")).not.toMatch(/hoàn tiền/);
  });
  it("explains a parcel already sent", () => {
    expect(returnShippingErrorMessage(new ApiError(409, "destination_changed", "x"), "f")).toMatch(
      /đã được gửi/,
    );
  });
});
