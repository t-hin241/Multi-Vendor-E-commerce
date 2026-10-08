import { ApiError } from "@/lib/api-client";
import type {
  CancellationRequest,
  CancellationStatus,
  Shipment,
  VendorOrder,
} from "@/lib/api-client";

// Cancelling a paid package before handover (AF-03). Order decides every
// step; these helpers word it and hide what the server would refuse.

export const BUYER_CANCEL_REASONS = [
  { value: "changed_mind", label: "Tôi đổi ý" },
  { value: "ordered_by_mistake", label: "Đặt nhầm" },
  { value: "delivery_too_slow", label: "Chờ giao quá lâu" },
  { value: "other", label: "Lý do khác" },
];

export const VENDOR_CANCEL_REASONS = [
  { value: "out_of_stock", label: "Hết hàng" },
  { value: "damaged_stock", label: "Hàng hỏng" },
  { value: "cannot_fulfil", label: "Không thể thực hiện" },
];

// What the buyer reads: never "refunded" before Payment confirmed.
const BUYER_LABELS: Record<CancellationStatus, string> = {
  preparing: "Đã tiếp nhận yêu cầu hủy",
  requested: "Đã tiếp nhận yêu cầu hủy, đang chờ sàn xem xét",
  stopping_fulfillment: "Sàn đã chấp nhận, đang dừng giao",
  approved: "Đã dừng giao",
  refund_pending: "Đã hủy giao, đang hoàn tiền",
  resolved: "Đã hủy và hoàn tiền",
  rejected: "Yêu cầu hủy không được chấp nhận",
  needs_review: "Sàn đang kiểm tra yêu cầu hủy",
};

export function buyerCancellationLabel(status: CancellationStatus): string {
  return BUYER_LABELS[status] ?? status;
}

export const ADMIN_CANCELLATION_LABELS: Record<CancellationStatus, string> = {
  preparing: "Preparing (payout hold)",
  requested: "Waiting for a decision",
  stopping_fulfillment: "Stopping the shipment",
  approved: "Stopped",
  refund_pending: "Refund pending",
  resolved: "Resolved",
  rejected: "Rejected",
  needs_review: "Needs review",
};

export function isOpenCancellation(c: Pick<CancellationRequest, "status">): boolean {
  return c.status !== "rejected" && c.status !== "resolved";
}

// canAskCancellation: a paid package not handed over and without an open
// request. The server checks again (409 already_shipped / request_exists).
export function canAskCancellation(
  vo: Pick<VendorOrder, "id" | "status">,
  requests: Pick<CancellationRequest, "vendor_order_id" | "status">[],
  shipment?: Pick<Shipment, "status">,
): boolean {
  if (vo.status !== "paid" && vo.status !== "processing") return false;
  if (shipment && shipment.status !== "pending" && shipment.status !== "ready_to_ship") {
    return false;
  }
  return !requests.some((r) => r.vendor_order_id === vo.id && isOpenCancellation(r));
}

export function cancellationErrorMessage(err: unknown, fallback: string): string {
  if (!(err instanceof ApiError)) return fallback;
  switch (err.code) {
    case "already_shipped":
      return "Gói hàng đã được giao cho vận chuyển. Bạn có thể yêu cầu trả hàng hoặc mở yêu cầu hỗ trợ.";
    case "request_exists":
      return "Gói hàng này đã có yêu cầu hủy đang được xử lý.";
    case "paid_cancellation_disabled":
      return "Chưa hỗ trợ hủy đơn đã thanh toán trực tuyến. Vui lòng mở yêu cầu hỗ trợ.";
  }
  if (err.status === 0 || err.status >= 500) return `${fallback} ${err.message}`;
  return err.message || fallback;
}
