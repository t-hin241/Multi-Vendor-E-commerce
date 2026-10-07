import { afterEach, describe, expect, it, vi } from "vitest";

import {
  ADMIN_PAGE_BUNDLES,
  canOpenAdminPage,
  orApprovalDraft,
  payoutDecisionRef,
  payoutDetailsRef,
} from "./admin-access";
import { ADMIN_NAV_LINKS } from "./admin-nav";
import { ApiError } from "./api-client";

const passthrough = <R>(fn: (token: string) => Promise<R>) => fn("synthetic-token");

afterEach(() => vi.unstubAllGlobals());

describe("admin access", () => {
  it("names a bundle for every admin page", () => {
    for (const link of ADMIN_NAV_LINKS) {
      expect(ADMIN_PAGE_BUNDLES[link.href], link.href).toBeDefined();
    }
  });

  it("opens only the pages the bundles allow", () => {
    expect(canOpenAdminPage("/admin/refunds", ["finance.read"])).toBe(true);
    expect(canOpenAdminPage("/admin/refunds", ["support.manage"])).toBe(false);
    expect(canOpenAdminPage("/admin/approvals", ["finance.approve"])).toBe(true);
    expect(canOpenAdminPage("/admin/access", ["audit.read"])).toBe(false);
    expect(canOpenAdminPage("/admin/access", undefined)).toBe(false);
  });

  it("binds payout proofs to the account version and action", () => {
    expect(payoutDecisionRef("acct", 3, true)).toBe("payout_account:acct:v3:verify");
    expect(payoutDecisionRef("acct", 3, false)).toBe("payout_account:acct:v3:reject");
    expect(payoutDetailsRef("acct", 3)).toBe("payout_account:acct:v3:details");
  });

  it("turns an action needing a second admin into a draft request", async () => {
    const fetchMock = vi.fn(async () =>
      Response.json({ data: { id: "12345678-aaaa-bbbb-cccc-000000000000" } }, { status: 201 }),
    );
    vi.stubGlobal("fetch", fetchMock);
    const direct = vi.fn(async () => {
      throw new ApiError(409, "approval_required", "needs a second admin");
    });
    await expect(
      orApprovalDraft(passthrough, direct, {
        operation_kind: "refund_resolution",
        target_id: "refund-1",
        payload: { outcome: "succeeded", evidence_reference: "FAKE-REF" },
        reason: "Bank confirmed",
      }),
    ).rejects.toThrow(/Draft request 12345678/);
    const [url, init] = fetchMock.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toContain("/api/payments/admin/approval-requests");
    expect(JSON.parse(String(init.body))).toMatchObject({
      operation_kind: "refund_resolution",
      target_id: "refund-1",
    });
  });

  it("passes other errors and results through", async () => {
    await expect(
      orApprovalDraft(passthrough, async () => "done", {
        operation_kind: "settlement_adjustment",
        target_id: "v",
        payload: {},
        reason: "r",
      }),
    ).resolves.toBe("done");
    await expect(
      orApprovalDraft(
        passthrough,
        async () => {
          throw new ApiError(403, "missing_permission", "no");
        },
        { operation_kind: "settlement_adjustment", target_id: "v", payload: {}, reason: "r" },
      ),
    ).rejects.toMatchObject({ code: "missing_permission" });
  });
});
