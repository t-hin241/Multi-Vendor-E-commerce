import { ApiError } from "@/lib/api-client";
import type {
  CheckoutPreview,
  OrderItem,
  OrderRefundStatus,
  PaymentRefundStatus,
  ReturnRequest,
  ReturnStatus,
} from "@/lib/api-client";

// Presentation helpers for Order's checkout, return and refund workflow.
// Order and Payment decide every state; nothing here recomputes money.

export type CheckoutAttempt = { fingerprint: string; key: string };

// checkoutAttempt keeps one Idempotency-Key per checkout input: a retry of
// the same address, cart version and confirmed total reuses the key (and
// gets the same order back), while any change starts a new attempt.
export function checkoutAttempt(
  previous: CheckoutAttempt | null,
  input: {
    addressId: string;
    cartVersion?: number;
    expectedTotalAmount?: number;
    acceptedPolicyVersions?: Record<string, number>;
  },
  newKey: () => string = newIdempotencyKey,
): CheckoutAttempt {
  const policies = Object.entries(input.acceptedPolicyVersions ?? {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([kind, version]) => `${kind}=${version}`)
    .join(",");
  // Without policies the fingerprint keeps its earlier shape, so an
  // attempt stored before policies existed is still recognized.
  const fingerprint = [
    input.addressId,
    input.cartVersion ?? "",
    input.expectedTotalAmount ?? "",
    ...(policies ? [policies] : []),
  ].join("|");
  if (previous && previous.fingerprint === fingerprint) return previous;
  return { fingerprint, key: newKey() };
}

export function newIdempotencyKey(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") {
    return crypto.randomUUID();
  }
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}

export type CheckoutErrorKind =
  | "cart_changed"
  | "total_changed"
  | "shipping_unavailable"
  | "in_progress"
  | "policy_changed"
  | "other";

export function checkoutErrorKind(err: unknown): CheckoutErrorKind {
  if (!(err instanceof ApiError)) return "other";
  switch (err.code) {
    case "cart_changed":
      return "cart_changed";
    case "checkout_total_changed":
      return "total_changed";
    case "shipping_unavailable":
      return "shipping_unavailable";
    case "checkout_in_progress":
      return "in_progress";
    case "policy_changed":
      return "policy_changed";
    default:
      return "other";
  }
}

// canPlaceOrder: the buyer may only confirm a total Order quoted for the
// cart version they are looking at, with every shop's shipping priced.
export function canPlaceOrder(
  preview: CheckoutPreview | undefined,
  cartVersion: number,
): preview is CheckoutPreview & { total_amount: number } {
  return Boolean(
    preview &&
    preview.ready &&
    preview.total_amount !== null &&
    preview.cart_version === cartVersion,
  );
}

export const returnStatusLabels: Record<ReturnStatus, string> = {
  requested: "Chờ người bán xác nhận",
  vendor_confirmed: "Chờ sàn duyệt",
  rejected: "Bị từ chối",
  approved: "Đã duyệt, chờ gửi hàng về",
  received: "Đã nhận hàng trả",
  refund_pending: "Đang hoàn tiền",
  refunded: "Đã hoàn tiền",
  refund_failed: "Hoàn tiền thất bại",
};

export function returnStatusLabel(status: string): string {
  return returnStatusLabels[status as ReturnStatus] ?? status;
}

export const orderRefundStatusLabels: Record<OrderRefundStatus, string> = {
  requested: "Đã yêu cầu",
  submitted: "Đang chờ cổng thanh toán",
  succeeded: "Đã hoàn tiền",
  failed: "Thất bại",
  rejected: "Bị từ chối",
};

export const paymentRefundStatusLabels: Record<PaymentRefundStatus, string> = {
  awaiting_provider_refund: "Chờ xác nhận hoàn tiền",
  pending: "Đang xử lý",
  succeeded: "Đã hoàn tiền",
  failed: "Thất bại",
};

// returnableQuantity is how many units of an item the buyer may still ask
// to return: purchased minus units in returns that were not rejected.
// Order re-checks this and the return window.
export function returnableQuantity(item: OrderItem, returns: ReturnRequest[] | undefined): number {
  const claimed = (returns ?? [])
    .filter((r) => r.order_item_id === item.id && r.status !== "rejected")
    .reduce((sum, r) => sum + r.quantity, 0);
  return Math.max(0, item.quantity - claimed);
}

// Next actions each role may take on a return, mirroring Order's rules.
export function vendorCanConfirm(r: ReturnRequest): boolean {
  return r.status === "requested";
}
export function canReceive(r: ReturnRequest): boolean {
  return r.status === "approved";
}
export function adminCanDecide(r: ReturnRequest): boolean {
  return r.status === "requested" || r.status === "vendor_confirmed";
}
export function adminCanRetryRefund(r: ReturnRequest): boolean {
  return r.status === "refund_failed";
}
