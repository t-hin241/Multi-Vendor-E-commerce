import { ApiError } from "@/lib/api-client";
import type { ReturnRequest, ReturnShippingStatus } from "@/lib/api-client";

// Return shipping (AF-05). Order decides every step; these helpers word it
// and hide what the server would refuse.

const BUYER_SHIPPING_LABELS: Record<ReturnShippingStatus, string> = {
  destination_missing: "Sàn đang chuẩn bị địa chỉ nhận trả",
  awaiting_dispatch: "Chờ bạn gửi hàng trả",
  awaiting_verification: "Đã gửi, chờ người bán nhận hàng",
  received: "Người bán đã nhận hàng trả",
  lost: "Kiện hàng trả thất lạc, sàn đang xử lý",
};

export function buyerShippingLabel(status?: ReturnShippingStatus): string {
  return status ? (BUYER_SHIPPING_LABELS[status] ?? status) : "";
}

export const ADMIN_SHIPPING_LABELS: Record<ReturnShippingStatus, string> = {
  destination_missing: "No verified destination",
  awaiting_dispatch: "Awaiting buyer dispatch",
  awaiting_verification: "In transit (buyer reported)",
  received: "Received",
  lost: "Lost on the way back",
};

// canDispatch: instructions issued, nothing reported yet. Late parcels are
// still accepted (the deadline only puts the return in review).
export function canDispatch(r: Pick<ReturnRequest, "status" | "shipping_status">): boolean {
  return r.status === "approved" && r.shipping_status === "awaiting_dispatch";
}

// canRecordReceipt: the shop records what came back for an approved return.
export function canRecordReceipt(r: Pick<ReturnRequest, "status">): boolean {
  return r.status === "approved";
}

export function canAuthorizeShipping(
  r: Pick<ReturnRequest, "status" | "shipping_status">,
): boolean {
  return (
    r.status === "approved" &&
    (r.shipping_status === undefined ||
      r.shipping_status === "destination_missing" ||
      r.shipping_status === "awaiting_dispatch")
  );
}

// receiptError: every returned unit is sellable, damaged or missing.
export function receiptError(
  approved: number,
  counts: { sellable: number; damaged: number; missing: number },
): string | null {
  const values = [counts.sellable, counts.damaged, counts.missing];
  if (values.some((v) => !Number.isInteger(v) || v < 0)) return "Số lượng không hợp lệ.";
  const total = values.reduce((a, b) => a + b, 0);
  if (total !== approved) return `Cần ghi đủ ${approved} sản phẩm (đang ${total}).`;
  return null;
}

export function returnShippingErrorMessage(err: unknown, fallback: string): string {
  if (!(err instanceof ApiError)) return fallback;
  switch (err.code) {
    case "version_conflict":
      return "Yêu cầu vừa thay đổi. Vui lòng tải lại và thử lại.";
    case "invalid_tracking":
      return "Mã vận đơn không hợp lệ (3-64 chữ, số, '.', '_' hoặc '-').";
    case "destination_changed":
      return "Kiện hàng đã được gửi; không thể đổi địa chỉ nhận.";
    case "return_not_approved":
      return "Yêu cầu trả hàng chưa có hướng dẫn gửi.";
    case "return_destination_missing":
      return "Người bán chưa có địa chỉ nhận trả đã xác minh.";
    case "idempotency_key_reused":
      return "Thông tin gửi hàng đã được ghi với nội dung khác. Tải lại trang.";
  }
  if (err.status === 0 || err.status >= 500) return `${fallback} ${err.message}`;
  return err.message || fallback;
}
