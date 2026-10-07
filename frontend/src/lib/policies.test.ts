import { afterEach, describe, expect, it, vi } from "vitest";

import { ApiError, checkout } from "./api-client";
import { checkoutAttempt, checkoutErrorKind } from "./order-workflow";
import { isPolicyKind, policyVersionHref, returnRulesSummary, returnsRuleRefs } from "./policies";

describe("policy presentation", () => {
  it("words the return rules from the order snapshot", () => {
    expect(returnRulesSummary({ returns_window_days: 14, return_shipping_refund: "none" })).toBe(
      "Yêu cầu trả hàng trong 14 ngày kể từ khi nhận hàng; phí vận chuyển không được hoàn khi trả hàng.",
    );
  });

  it("links a fixed version and knows the kinds", () => {
    expect(policyVersionHref("returns", 3)).toBe("/policies/returns/v/3");
    expect(isPolicyKind("terms")).toBe(true);
    expect(isPolicyKind("cookies")).toBe(false);
  });

  it("builds the rule references Order enforces", () => {
    expect(returnsRuleRefs(7, "none")).toEqual({
      "order.returns_window": "window-7d",
      "order.return_shipping_refund": "none",
    });
  });
});

describe("checkout with accepted policies", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("starts a new attempt when the accepted versions change", () => {
    let n = 0;
    const gen = () => `key-${++n}`;
    const input = {
      addressId: "a",
      cartVersion: 1,
      expectedTotalAmount: 100,
      acceptedPolicyVersions: { returns: 1 },
    };
    const first = checkoutAttempt(null, input, gen);
    expect(checkoutAttempt(first, { ...input, acceptedPolicyVersions: { returns: 1 } }, gen)).toBe(
      first,
    );
    expect(
      checkoutAttempt(first, { ...input, acceptedPolicyVersions: { returns: 2 } }, gen).key,
    ).not.toBe(first.key);
  });

  it("recognizes policy_changed", () => {
    expect(checkoutErrorKind(new ApiError(409, "policy_changed", "x"))).toBe("policy_changed");
  });

  it("sends the versions the buyer was shown", async () => {
    const fetcher = vi.fn().mockResolvedValue({
      ok: true,
      json: async () => ({ data: { id: "o-1" } }),
      headers: new Headers(),
    });
    vi.stubGlobal("fetch", fetcher);
    await checkout("t", {
      addressId: "a",
      idempotencyKey: "key-12345678",
      acceptedPolicyVersions: { returns: 2, terms: 1 },
    });
    const init = fetcher.mock.calls[0]?.[1] as { body: string };
    expect(JSON.parse(init.body).accepted_policy_versions).toEqual({ returns: 2, terms: 1 });
  });
});
