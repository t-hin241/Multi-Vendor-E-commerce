import type { PayoutBatch } from "@/lib/api-client";

// PW-027 labels for a payout batch's approval.

const RISK_LABELS: Record<string, string> = {
  batch_total_over_limit: "tổng lô vượt hạn mức",
  item_over_limit: "có khoản vượt hạn mức",
  new_destination: "tài khoản nhận lần đầu",
  currency_without_limit: "loại tiền chưa có hạn mức",
};

export function riskLabel(reason: string): string {
  return RISK_LABELS[reason] ?? reason.replace(/_/g, " ");
}

// approvalStatus says where the batch is in its approval.
export function approvalStatus(b: PayoutBatch): string {
  const why = b.risk_reasons?.length ? ` (${b.risk_reasons.map(riskLabel).join(", ")})` : "";
  switch (b.approval_state) {
    case "awaiting_approval":
      return b.required_approvals > 1 ? `Chờ duyệt cấp 1 trên 2${why}` : "Chờ một admin khác duyệt";
    case "awaiting_second_approval":
      return `Chờ duyệt cấp 2 (người khác người duyệt cấp 1)${why}`;
    case "approved":
      return b.required_approvals > 1 ? "Đã duyệt đủ hai cấp" : "Đã duyệt";
    case "rejected":
      return `Bị từ chối${b.decision_reason ? `: ${b.decision_reason}` : ""}`;
    case "withdrawn":
      return `Người tạo đã rút${b.decision_reason ? `: ${b.decision_reason}` : ""}`;
    default:
      return "Lô trước khi có duyệt";
  }
}

// transfersAllowed: transfer results may be recorded for the batch.
export function transfersAllowed(b: PayoutBatch): boolean {
  return b.approval_state === "approved" || b.approval_state === "not_required";
}

export function awaitingApproval(b: PayoutBatch): boolean {
  return (
    b.approval_state === "awaiting_approval" || b.approval_state === "awaiting_second_approval"
  );
}
