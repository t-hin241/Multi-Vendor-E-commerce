import type { Order, OrderStatus } from "@/lib/api-client";

// After the buyer comes back from the payment provider, the order page
// learns the outcome only from Order (updated by Payment's verified
// webhook). The query string the provider adds (status=PAID, cancel=true,
// ...) is never read: it can be typed by anyone.

export type PaymentView = "confirming" | "paid" | "unpaid" | "cancelled" | "unknown";

const PAID: OrderStatus[] = ["paid", "processing", "shipped", "completed", "refunded"];

// paymentView says what to show for the order as Order reports it.
// returned: the buyer arrived from the provider's return (not cancel) page.
export function paymentView(
  order: Pick<Order, "status"> | undefined,
  returned: boolean,
  timedOut: boolean,
): PaymentView {
  if (!order) return timedOut ? "unknown" : "confirming";
  if (PAID.includes(order.status)) return "paid";
  if (order.status === "cancelled") return "cancelled";
  if (order.status !== "pending_payment") return "unknown"; // a status this page does not know
  if (!returned) return "unpaid";
  return timedOut ? "unknown" : "confirming";
}

// pollDelay is the wait before look number n (0-based): 1s, 2s, 3s, 5s,
// 8s, then every 10s; null once maxMs has passed since the first look.
export function pollDelay(n: number, elapsedMs: number, maxMs = 120_000): number | null {
  if (elapsedMs >= maxMs) return null;
  const steps = [1000, 2000, 3000, 5000, 8000];
  return steps[n] ?? 10_000;
}

const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

// orderIdFromQuery accepts only a well-formed order id; the order itself is
// then read with the buyer's session, so another buyer's id shows nothing.
export function orderIdFromQuery(raw: string | null): string | null {
  return raw && UUID.test(raw) ? raw.toLowerCase() : null;
}
