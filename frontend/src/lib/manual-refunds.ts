import type {
  BuyerRefundStage,
  ManualRefundStage,
  RefundAttempt,
  RefundAttemptStage,
} from "@/lib/api-client";

// Manual bank-transfer refunds (AF-06). Payment decides every step; these
// helpers only word the state and build the operation a password proof is
// bound to (same format as the Payment service).

// What the buyer reads: never that money is back before a reviewer
// confirmed the bank reference.
export const BUYER_REFUND_STAGE_LABELS: Record<BuyerRefundStage, string> = {
  awaiting_destination: "Cần thông tin tài khoản nhận tiền",
  destination_rejected: "Tài khoản nhận tiền chưa hợp lệ, vui lòng nhập lại",
  verifying: "Đang xác minh tài khoản nhận tiền",
  processing: "Đang hoàn tiền",
  refunded: "Đã hoàn tiền",
  failed: "Hoàn tiền không thành công",
};

export const BUYER_REFUND_EVENT_LABELS: Record<string, string> = {
  requested: "Yêu cầu hoàn tiền được ghi nhận",
  destination_submitted: "Đã gửi tài khoản nhận tiền",
  destination_verified: "Tài khoản nhận tiền đã được xác minh",
  destination_rejected: "Tài khoản nhận tiền bị từ chối",
  transfer_in_progress: "Đang chuyển khoản",
  succeeded: "Đã xác nhận hoàn tiền",
  failed: "Hoàn tiền không thành công",
};

export const MANUAL_STAGE_LABELS: Record<ManualRefundStage, string> = {
  awaiting_destination: "Waiting for the buyer's account",
  verifying: "Destination to verify",
  ready: "Ready to prepare a transfer",
  executing: "Transfer claimed",
  submitted: "Transfer to confirm",
  unknown: "Unknown: check the bank statement",
  confirmed: "Confirmed",
  failed: "Failed",
};

export const ATTEMPT_STAGE_LABELS: Record<RefundAttemptStage, string> = {
  ready: "Ready",
  executing: "Claimed",
  submitted: "Submitted",
  confirmed: "Confirmed",
  failed: "Failed",
  unknown: "Unknown",
  voided: "Voided",
};

export const DESTINATION_DECIDE_PURPOSE = "payment.refund_destination.decide";
export const DESTINATION_REVEAL_PURPOSE = "payment.refund_destination.reveal";
export const ATTEMPT_DECIDE_PURPOSE = "payment.refund_attempt.decide";

export function destinationDecisionRef(refundId: string, version: number, verify: boolean): string {
  return `refund_destination:${refundId}:v${version}:${verify ? "verify" : "reject"}`;
}

export function destinationRevealRef(refundId: string, version: number): string {
  return `refund_destination:${refundId}:v${version}:reveal`;
}

export function attemptDecisionRef(attemptId: string, version: number, confirm: boolean): string {
  return `refund_attempt:${attemptId}:v${version}:${confirm ? "confirm" : "fail"}`;
}

export function isActiveAttempt(stage: RefundAttemptStage): boolean {
  return stage === "ready" || stage === "executing" || stage === "submitted" || stage === "unknown";
}

// The buyer form only checks the shape; Payment validates again.
export function checkBeneficiary(input: {
  bank_code: string;
  account_number: string;
  account_name: string;
}): string | null {
  if (!/^[A-Za-z0-9]{2,20}$/.test(input.bank_code.trim()))
    return "Mã ngân hàng gồm 2-20 chữ hoặc số.";
  if (!/^[0-9]{6,20}$/.test(input.account_number.replace(/[\s.-]/g, "")))
    return "Số tài khoản gồm 6-20 chữ số.";
  const name = input.account_name.trim().replace(/\s+/g, " ");
  if (name.length < 2 || name.length > 100 || !/^[\p{L} ]+$/u.test(name))
    return "Tên chủ tài khoản chỉ gồm chữ cái, 2-100 ký tự.";
  return null;
}

// What an admin may do on an attempt; the service checks again.
export function attemptActions(
  attempt: RefundAttempt,
  me: string | undefined,
  now: Date = new Date(),
): { claim: boolean; cancel: boolean; submit: boolean; decide: boolean; confirm: boolean } {
  const mine = Boolean(me) && attempt.claimed_by === me;
  const leaseLive =
    attempt.lease_expires_at !== undefined && new Date(attempt.lease_expires_at) > now;
  const executor = mine || attempt.submitted_by === me;
  return {
    claim: attempt.stage === "ready",
    cancel: attempt.stage === "ready" || (attempt.stage === "executing" && mine && leaseLive),
    submit: (attempt.stage === "executing" && mine) || attempt.stage === "unknown",
    decide: (attempt.stage === "submitted" || attempt.stage === "unknown") && !executor,
    confirm: attempt.stage === "submitted" && !executor,
  };
}
