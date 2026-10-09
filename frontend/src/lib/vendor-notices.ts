import { ApiError, type VendorNoticeCategory } from "@/lib/api-client";

// AF-08: shop work notices. The server decides who is told (owner always,
// staff by permission and opt-in); this only labels and edits opt-ins.

export const VENDOR_NOTICE_CATEGORIES: {
  value: VendorNoticeCategory;
  label: string;
  hint: string;
}[] = [
  { value: "orders", label: "Đơn hàng", hint: "Đơn mới đã thanh toán, yêu cầu hủy gói hàng" },
  { value: "returns", label: "Trả hàng", hint: "Yêu cầu trả hàng mới, hàng trả đang gửi về" },
  { value: "finance", label: "Thanh toán cho shop", hint: "Kết quả chuyển khoản payout" },
  {
    value: "support",
    label: "Hỗ trợ khách hàng",
    hint: "Yêu cầu hỗ trợ mới, sàn chờ shop phản hồi",
  },
];

// toggleCategory adds or removes one category, keeping the server's order.
export function toggleCategory(
  current: VendorNoticeCategory[],
  category: VendorNoticeCategory,
  on: boolean,
): VendorNoticeCategory[] {
  const next = new Set(current);
  if (on) next.add(category);
  else next.delete(category);
  return VENDOR_NOTICE_CATEGORIES.map((c) => c.value).filter((c) => next.has(c));
}

// noticesDisabled: the feature flag is off (the card hides itself).
export function noticesDisabled(error: unknown): boolean {
  return error instanceof ApiError && error.code === "feature_disabled";
}

export function preferencesErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 409) return "Tùy chọn đã được thay đổi ở nơi khác. Tải lại rồi thử lại.";
    if (error.status === 422 || error.status === 400) return "Lựa chọn không hợp lệ.";
  }
  return "Chưa lưu được. Vui lòng thử lại.";
}

const ACTION_LABELS: Record<string, string> = {
  new_order: "New paid order",
  cancellation_requested: "Cancellation requested",
  return_requested: "Return requested",
  return_dispatched: "Return parcel sent",
  payout_succeeded: "Payout sent",
  payout_failed: "Payout failed",
  support_case_opened: "Support case opened",
  support_waiting_shop: "Waiting for the shop",
  delivery_goods_returned: "Failed delivery coming back",
  redelivery_accepted: "Redelivery accepted",
};

export function vendorActionLabel(kind: string): string {
  return ACTION_LABELS[kind] ?? kind.replace(/_/g, " ");
}

// needsReview: an admin follows the event up and may retry it.
export function needsReview(status: string): boolean {
  return status === "no_recipient" || status === "parked";
}
