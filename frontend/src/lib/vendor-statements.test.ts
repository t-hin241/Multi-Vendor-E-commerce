import { describe, expect, it } from "vitest";
import { statementPeriod } from "./vendor-statements";
import { canOpen } from "./shop-access";

describe("vendor statements", () => {
  it("uses half-open Vietnam days, including the last day", () => {
    expect(statementPeriod("2026-01-01", "2026-01-01")).toEqual({
      from: "2025-12-31T17:00:00.000Z",
      to: "2026-01-01T17:00:00.000Z",
      currency: "VND",
    });
    expect(statementPeriod("", "2026-01-01")).toBeNull();
    expect(statementPeriod("2026-02-30", "2026-03-01")).toBeNull();
    expect(statementPeriod("2026-01-02", "2026-01-01")).toBeNull();
    expect(statementPeriod("2024-01-01", "2025-01-01")).toBeNull();
  });
  it("allows finance-only staff without granting payout destination access", () => {
    const shop = { role: "staff" as const, capabilities: ["finance.read"] };
    expect(canOpen("/vendor/statements", shop, "buyer")).toBe(true);
    expect(canOpen("/vendor/payout-accounts", shop, "buyer")).toBe(false);
    expect(
      canOpen("/vendor/statements", { role: "staff", capabilities: ["orders.read"] }, "buyer"),
    ).toBe(false);
  });
});
