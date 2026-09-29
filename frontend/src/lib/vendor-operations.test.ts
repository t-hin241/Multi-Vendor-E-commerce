import { afterEach, describe, expect, it, vi } from "vitest";
import { listMyVendors } from "./api-client";
import { accountDetails, decideAccount, dashboard } from "./vendor-operations";
import type { PayoutAccount } from "./vendor-operations";

afterEach(() => vi.unstubAllGlobals());
const account: PayoutAccount = {
  id: "test-account",
  vendor_id: "shop-a",
  version: 7,
  bank_bin: "000000",
  last4: "1234",
  status: "pending",
  is_default: false,
  created_at: "2026-09-28T00:00:00Z",
};
describe("vendor contracts", () => {
  it("keeps account version and shop in every sensitive action", async () => {
    const fetcher = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ data: {} }) });
    vi.stubGlobal("fetch", fetcher);
    await decideAccount("synthetic-token", "shop-a", account, true, "Test evidence");
    await accountDetails("synthetic-token", "shop-a", account, "Test verification");
    for (const [path, options] of fetcher.mock.calls) {
      expect(path).toContain("/admin/shops/shop-a/payout-accounts/test-account/");
      expect(options.method).toBe("POST");
      expect(JSON.parse(options.body).version).toBe(7);
    }
  });
  it("loads every page for the multi-shop selector", async () => {
    const first = Array.from({ length: 100 }, (_, i) => ({ id: `shop-${i}` }));
    const fetcher = vi
      .fn()
      .mockResolvedValueOnce({ ok: true, json: async () => ({ data: first }) })
      .mockResolvedValueOnce({ ok: true, json: async () => ({ data: [{ id: "last-shop" }] }) });
    vi.stubGlobal("fetch", fetcher);
    expect(await listMyVendors("synthetic-token")).toHaveLength(101);
    expect(fetcher.mock.calls[1]?.[0]).toContain("limit=100&offset=100");
  });
  it("preserves unknown eligible money instead of converting it to zero", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue({
        ok: true,
        json: async () => ({
          data: {
            payments: {
              eligible: {
                available: false,
                value: null,
                reason: "settlement_eligibility_not_confirmed",
              },
            },
          },
        }),
      }),
    );
    const report = await dashboard(
      "synthetic-token",
      "shop-a",
      "2026-09-01T00:00:00Z",
      "2026-10-01T00:00:00Z",
    );
    expect(report.payments?.eligible.value).toBeNull();
    expect(report.payments?.eligible.available).toBe(false);
  });
});
