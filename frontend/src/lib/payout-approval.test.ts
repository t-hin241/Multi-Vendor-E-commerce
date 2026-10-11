import { describe, expect, it } from "vitest";

import type { PayoutBatch } from "@/lib/api-client";
import { approvalStatus, awaitingApproval, transfersAllowed } from "@/lib/payout-approval";

const base: PayoutBatch = {
  id: "b",
  idempotency_key: "k",
  currency: "VND",
  status: "pending",
  created_by: "maker",
  created_at: "2026-10-11T00:00:00Z",
  total_amount: 110000,
  approval_state: "awaiting_approval",
  required_approvals: 1,
  risk_reasons: [],
};

describe("payout batch approval", () => {
  it("blocks transfers until approved", () => {
    expect(transfersAllowed(base)).toBe(false);
    expect(awaitingApproval(base)).toBe(true);
    expect(transfersAllowed({ ...base, approval_state: "approved" })).toBe(true);
    expect(transfersAllowed({ ...base, approval_state: "not_required" })).toBe(true);
    expect(transfersAllowed({ ...base, approval_state: "rejected" })).toBe(false);
  });
  it("explains why a second approver is needed", () => {
    expect(approvalStatus(base)).toBe("Chờ một admin khác duyệt");
    expect(
      approvalStatus({
        ...base,
        approval_state: "awaiting_second_approval",
        required_approvals: 2,
        risk_reasons: ["new_destination", "batch_total_over_limit"],
      }),
    ).toBe(
      "Chờ duyệt cấp 2 (người khác người duyệt cấp 1) (tài khoản nhận lần đầu, tổng lô vượt hạn mức)",
    );
    expect(approvalStatus({ ...base, approval_state: "rejected", decision_reason: "Sai kỳ" })).toBe(
      "Bị từ chối: Sai kỳ",
    );
  });
});
