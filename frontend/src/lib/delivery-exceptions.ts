import { ApiError } from "@/lib/api-client";
import type {
  DeliveryException,
  DeliveryExceptionStatus,
  GoodsCondition,
  OrderItem,
} from "@/lib/api-client";

// Failed delivery and returned goods (AF-04). Order decides every step;
// these helpers word it and hide what the server would refuse.

// What the buyer reads: never "refunded" before Payment confirmed it, and
// never "the shop has your parcel" as if it were sellable.
const BUYER_LABELS: Record<DeliveryExceptionStatus, string> = {
  investigating: "Giao hàng chưa thành công, sàn đang kiểm tra với đơn vị vận chuyển",
  awaiting_goods: "Kiện hàng đang hoàn về người bán",
  awaiting_buyer: "Sàn đề nghị giao lại, đang chờ bạn xác nhận",
  redelivery_pending: "Đang giao lại kiện hàng",
  refund_pending: "Đang hoàn tiền cho kiện hàng",
  needs_review: "Sàn đang xem xét kiện hàng",
  resolved: "Sự cố giao hàng đã được xử lý",
};

export function buyerDeliveryLabel(status: DeliveryExceptionStatus): string {
  return BUYER_LABELS[status] ?? status;
}

const VENDOR_LABELS: Record<DeliveryExceptionStatus, string> = {
  investigating: "Giao thất bại, sàn đang kiểm tra",
  awaiting_goods: "Hàng hoàn về: ghi nhận tình trạng hàng",
  awaiting_buyer: "Chờ người mua xác nhận giao lại",
  redelivery_pending: "Giao lại (lần giao mới)",
  refund_pending: "Đang hoàn tiền cho người mua",
  needs_review: "Sàn đang xem xét",
  resolved: "Đã xử lý",
};

export function vendorDeliveryLabel(status: DeliveryExceptionStatus): string {
  return VENDOR_LABELS[status] ?? status;
}

export const ADMIN_DELIVERY_LABELS: Record<DeliveryExceptionStatus, string> = {
  investigating: "Investigating",
  awaiting_goods: "Awaiting goods / receipt",
  awaiting_buyer: "Waiting for buyer consent",
  redelivery_pending: "Redelivery in progress",
  refund_pending: "Refund pending",
  needs_review: "Needs review",
  resolved: "Resolved",
};

export const CONDITION_LABELS: Record<GoodsCondition, string> = {
  sellable: "Bán lại được",
  damaged: "Hỏng",
  missing: "Thiếu",
};

// needsReceipt: the package is back at the shop and nothing is recorded
// for this attempt yet.
export function needsReceipt(
  d: Pick<DeliveryException, "status" | "carrier_outcome" | "receipt">,
): boolean {
  return d.carrier_outcome === "returned" && !d.receipt && d.status !== "resolved";
}

// What an admin may decide now (the server checks again).
export function canRedeliver(d: DeliveryException): boolean {
  return (
    (d.status === "awaiting_goods" || d.status === "needs_review") &&
    d.carrier_outcome === "returned" &&
    d.resolution !== "refund" &&
    d.redelivery_count < d.redelivery_limit &&
    !!d.receipt &&
    d.receipt.lines.every((l) => l.condition === "sellable")
  );
}

export function canRefund(d: DeliveryException): boolean {
  return (
    !d.refund_id &&
    (d.carrier_outcome === "returned" || d.carrier_outcome === "lost") &&
    ["investigating", "awaiting_goods", "awaiting_buyer", "needs_review"].includes(d.status)
  );
}

export function canClose(d: DeliveryException): boolean {
  return (
    !!d.late_delivery_at &&
    d.carrier_outcome === "delivered" &&
    d.status !== "resolved" &&
    d.status !== "refund_pending"
  );
}

export function canRetryRefund(d: DeliveryException): boolean {
  return d.status === "needs_review" && !!d.refund_id && !d.late_delivery_at;
}

export type ReceiptDraft = Record<string, Partial<Record<GoodsCondition, number>>>;

// receiptLines turns the shop's counts into lines, refusing a draft that
// does not account for every unit of every item exactly once.
export function receiptLines(
  items: Pick<OrderItem, "id" | "quantity">[],
  draft: ReceiptDraft,
): { lines: { item_id: string; quantity: number; condition: GoodsCondition }[]; error?: string } {
  const lines: { item_id: string; quantity: number; condition: GoodsCondition }[] = [];
  for (const item of items) {
    const counts = draft[item.id] ?? {};
    let total = 0;
    for (const condition of ["sellable", "damaged", "missing"] as GoodsCondition[]) {
      const qty = Math.floor(Number(counts[condition] ?? 0));
      if (!Number.isFinite(qty) || qty < 0) return { lines: [], error: "Số lượng không hợp lệ." };
      if (qty > 0) {
        lines.push({ item_id: item.id, quantity: qty, condition });
        total += qty;
      }
    }
    if (total !== item.quantity) {
      return {
        lines: [],
        error: `Mỗi sản phẩm cần ghi đủ ${item.quantity} đơn vị (đang ${total}).`,
      };
    }
  }
  return { lines };
}

export function deliveryErrorMessage(err: unknown, fallback: string): string {
  if (!(err instanceof ApiError)) return fallback;
  switch (err.code) {
    case "version_conflict":
      return "Hồ sơ vừa thay đổi. Vui lòng tải lại và thử lại.";
    case "receipt_exists":
      return "Đã ghi nhận hàng hoàn về; liên hệ sàn nếu cần sửa.";
    case "resolution_locked":
      return "Kiện này đã chọn hoàn tiền nên không thể giao lại.";
    case "redelivery_disabled":
      return "Chưa hỗ trợ giao lại; hãy hoàn tiền cho người mua.";
    case "hold_unavailable":
      return "Đang chờ thanh toán xác nhận giữ tiền đối soát. Vui lòng thử lại sau.";
  }
  if (err.status === 0 || err.status >= 500) return `${fallback} ${err.message}`;
  return err.message || fallback;
}
