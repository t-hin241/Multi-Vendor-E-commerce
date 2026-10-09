import { ApiError, type ApprovalKind, createApprovalRequest } from "@/lib/api-client";

// Scoped admin permissions (AF-19). These helpers only hide what an admin
// cannot use; every admin route is checked again by its service.

export const BUNDLE_LABELS: Record<string, string> = {
  "support.manage": "Support and operations",
  "moderation.manage": "Moderation",
  "finance.read": "Finance (read)",
  "finance.prepare": "Finance (prepare)",
  "finance.approve": "Finance (approve)",
  "platform.configure": "Platform configuration",
  "analytics.read": "Analytics",
  "audit.read": "Audit (read only)",
  "access.manage": "Access management",
};

export function bundleLabel(name: string): string {
  return BUNDLE_LABELS[name] ?? name;
}

// The bundle each console page needs (one of them is enough).
export const ADMIN_PAGE_BUNDLES: Record<string, string[]> = {
  "/admin": ["analytics.read"],
  "/admin/audit": ["audit.read"],
  "/admin/vendors": ["moderation.manage"],
  "/admin/products": ["moderation.manage"],
  "/admin/requests": ["moderation.manage"],
  "/admin/reviews": ["moderation.manage"],
  "/admin/users": ["moderation.manage"],
  "/admin/access": ["access.manage"],
  "/admin/orders": ["support.manage"],
  "/admin/work-items": ["support.manage"],
  "/admin/support": ["support.manage"],
  "/admin/cancellations": ["support.manage", "finance.prepare"],
  "/admin/delivery-exceptions": ["support.manage", "finance.prepare"],
  "/admin/fulfillment": ["support.manage"],
  "/admin/returns": ["support.manage"],
  "/admin/refunds": ["finance.read"],
  "/admin/payments": ["finance.read"],
  "/admin/payouts": ["finance.read"],
  "/admin/approvals": ["finance.read", "finance.prepare", "finance.approve"],
  "/admin/notifications": ["support.manage"],
  "/admin/events": ["platform.configure"],
  "/admin/categories": ["platform.configure"],
  "/admin/attributes": ["platform.configure"],
  "/admin/commission": ["finance.read"],
  "/admin/policies": ["platform.configure"],
  "/admin/shipping": ["platform.configure"],
};

export function canOpenAdminPage(href: string, permissions: string[] | undefined): boolean {
  const needed = ADMIN_PAGE_BUNDLES[href];
  if (!needed) return true;
  return needed.some((b) => permissions?.includes(b));
}

// Reauthentication purposes of the Vendor payout destination actions and
// the operation each proof is bound to (same format as the Vendor service).
export const PAYOUT_DECIDE_PURPOSE = "vendor.payout.decide";
export const PAYOUT_DETAILS_PURPOSE = "vendor.payout.details";

export function payoutDecisionRef(accountId: string, version: number, verify: boolean): string {
  return `payout_account:${accountId}:v${version}:${verify ? "verify" : "reject"}`;
}

export function payoutDetailsRef(accountId: string, version: number): string {
  return `payout_account:${accountId}:v${version}:details`;
}

export const APPROVAL_SUBMIT_PURPOSE = "payment.approval.submit";
export const APPROVAL_DECIDE_PURPOSE = "payment.approval.decide";

export const APPROVAL_KIND_LABELS: Record<ApprovalKind, string> = {
  refund_resolution: "Refund result",
  payout_item_resolution: "Payout transfer result",
  settlement_adjustment: "Ledger adjustment",
};

export function isApprovalRequired(err: unknown): boolean {
  return err instanceof ApiError && err.code === "approval_required";
}

export function needsReauthentication(err: unknown): boolean {
  return err instanceof ApiError && err.code === "reauthentication_required";
}

// orApprovalDraft runs a direct money action; when the platform requires a
// second admin it prepares an approval draft with the same input instead
// and reports that as an error the dialog shows.
export async function orApprovalDraft<T>(
  callWithAuth: <R>(fn: (token: string) => Promise<R>) => Promise<R>,
  direct: (token: string) => Promise<T>,
  draft: { operation_kind: ApprovalKind; target_id: string; payload: unknown; reason: string },
): Promise<T> {
  try {
    return await callWithAuth(direct);
  } catch (err) {
    if (!isApprovalRequired(err)) throw err;
    try {
      const created = await callWithAuth((token) => createApprovalRequest(token, draft));
      throw new Error(
        `A second admin must approve this. Draft request ${created.id.slice(0, 8)} was prepared: confirm and submit it under Approvals.`,
      );
    } catch (inner) {
      if (inner instanceof ApiError && inner.code === "approval_open") {
        throw new Error("An approval request for this item is already open; see Approvals.");
      }
      throw inner;
    }
  }
}
