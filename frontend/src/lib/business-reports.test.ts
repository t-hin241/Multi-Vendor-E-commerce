import { afterEach, describe, expect, it, vi } from "vitest";

import {
  formatRate,
  getDrilldown,
  getShopBusinessReport,
  reportPeriod,
  vietnamDate,
} from "./business-reports";
import { canOpen } from "./shop-access";

const period = { from: "2026-09-30T17:00:00.000Z", to: "2026-10-31T17:00:00.000Z" };
const shop = "00000001-0000-4000-8000-000000000001";
const asOf = "2026-10-14T01:00:00.123456Z";

afterEach(() => vi.unstubAllGlobals());

describe("business report periods", () => {
  it("converts inclusive Vietnam dates to half-open UTC boundaries", () => {
    expect(reportPeriod("2026-10-01", "2026-10-31")).toEqual(period);
    expect(reportPeriod("2024-02-29", "2024-02-29")).toEqual({
      from: "2024-02-28T17:00:00.000Z",
      to: "2024-02-29T17:00:00.000Z",
    });
    expect(reportPeriod("2024-01-01", "2024-12-31")).not.toBeNull();
    expect(vietnamDate(0, new Date("2026-09-30T16:59:59Z"))).toBe("2026-09-30");
    expect(vietnamDate(0, new Date("2026-09-30T17:00:00Z"))).toBe("2026-10-01");
  });
  it.each([
    ["2026-02-29", "2026-03-01"],
    ["2026-02-01", "2026-02-30"],
    ["2026-10-03", "2026-10-01"],
    ["2024-01-01", "2025-01-01"],
    ["2026-13-01", "2026-13-02"],
    ["", "2026-10-01"],
  ])("refuses invalid dates %s through %s", (from, to) => {
    expect(reportPeriod(from, to)).toBeNull();
  });
  it("distinguishes no denominator from a measured zero rate", () => {
    expect(formatRate({ numerator: 0, denominator: 0, rate: null })).toBe("—");
    expect(formatRate({ numerator: 0, denominator: 10, rate: 0 })).toBe("0%");
  });
});

describe("shop reports", () => {
  it("never requests money without finance permission and preserves owner failures", async () => {
    const fetcher = vi.fn(async (input: string) => {
      const url = new URL(input);
      expect(url.searchParams.get("vendor_id")).toBe(shop);
      if (url.pathname.includes("shipments"))
        return {
          ok: false,
          status: 503,
          headers: new Headers(),
          json: async () => ({ error: { code: "report_timeout", message: "Try again" } }),
        };
      return {
        ok: true,
        json: async () => ({
          data: {
            source: "order",
            definition_version: "business-report-v1",
            as_of: asOf,
            vendor_orders: 3,
          },
        }),
      };
    });
    vi.stubGlobal("fetch", fetcher);
    const result = await getShopBusinessReport("fake-token", shop, period, "day", false);
    expect(fetcher).toHaveBeenCalledTimes(2);
    expect(result.order).toMatchObject({ vendor_orders: 3 });
    expect(result.payment).toBeNull();
    expect(result.shipment).toBeNull();
    expect(result.sources).toEqual([
      { name: "order", status: "ok", generated_at: asOf },
      { name: "payment", status: "restricted", error: "permission_denied" },
      { name: "shipment", status: "unavailable", error: "report_timeout" },
    ]);
  });
  it("uses shop-scoped Payment routes and refuses mismatched definitions", async () => {
    const fetcher = vi.fn(async (input: string) => {
      const url = new URL(input);
      const payment = url.pathname.includes("payments");
      if (payment) expect(url.pathname).toBe(`/api/payments/vendor/${shop}/business-reports`);
      return {
        ok: true,
        json: async () => ({
          data: { as_of: asOf, definition_version: payment ? "v999" : "business-report-v1" },
        }),
      };
    });
    vi.stubGlobal("fetch", fetcher);
    const result = await getShopBusinessReport("fake-token", shop, period, "month", true);
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(result.payment).toBeNull();
    expect(result.sources.find((s) => s.name === "payment")).toMatchObject({
      status: "unavailable",
    });
  });
  it("keeps filter, cutoff precision and cursor when loading a drilldown", async () => {
    const fetcher = vi.fn(async (input: string) => {
      const url = new URL(input);
      expect(url.pathname).toBe(`/api/payments/vendor/${shop}/business-reports/drilldown`);
      expect(Object.fromEntries(url.searchParams)).toEqual({
        ...period,
        group: "month",
        vendor_id: shop,
        metric: "refunded",
        cutoff: asOf,
        cursor: "fake-cursor",
        limit: "20",
      });
      return { ok: true, json: async () => ({ data: { items: [] } }) };
    });
    vi.stubGlobal("fetch", fetcher);
    await getDrilldown("fake-token", {
      owner: "payment",
      scope: "shop",
      metric: "refunded",
      cutoff: asOf,
      cursor: "fake-cursor",
      range: { ...period, group: "month", vendor_id: shop },
    });
    expect(fetcher).toHaveBeenCalledTimes(1);
  });
  it("requires analytics access for the shop report page", () => {
    expect(
      canOpen("/vendor/reports", { role: "staff", capabilities: ["orders.read"] }, "buyer"),
    ).toBe(false);
    expect(
      canOpen("/vendor/reports", { role: "staff", capabilities: ["analytics.read"] }, "buyer"),
    ).toBe(true);
  });
});
