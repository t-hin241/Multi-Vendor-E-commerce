import type { PolicyKind, PolicySnapshot } from "@/lib/api-client";

// Presentation helpers for versioned policies (AF-02). Vendor owns the
// text and Order the rules; nothing here decides a deadline or a refund.

export const POLICY_KINDS: { kind: PolicyKind; label: string }[] = [
  { kind: "returns", label: "Chính sách đổi trả và hoàn tiền" },
  { kind: "shipping", label: "Chính sách vận chuyển" },
  { kind: "terms", label: "Điều khoản sử dụng" },
  { kind: "privacy", label: "Chính sách bảo mật" },
];

export function policyKindLabel(kind: string): string {
  return POLICY_KINDS.find((k) => k.kind === kind)?.label ?? kind;
}

export function isPolicyKind(kind: string): kind is PolicyKind {
  return POLICY_KINDS.some((k) => k.kind === kind);
}

// policyVersionHref links a specific version; it stays valid after a newer
// version is published.
export function policyVersionHref(kind: string, version: number): string {
  return `/policies/${kind}/v/${version}`;
}

// returnRulesSummary words the return rules an order is placed under, from
// Order's snapshot (not from the text).
export function returnRulesSummary(
  s: Pick<PolicySnapshot, "returns_window_days" | "return_shipping_refund">,
): string {
  const shipping =
    s.return_shipping_refund === "none"
      ? "phí vận chuyển không được hoàn khi trả hàng"
      : "phí vận chuyển hoàn theo chính sách";
  return `Yêu cầu trả hàng trong ${s.returns_window_days} ngày kể từ khi nhận hàng; ${shipping}.`;
}

export function formatEffectiveAt(iso: string): string {
  return new Date(iso).toLocaleString("vi-VN", { timeZone: "Asia/Saigon" });
}

// Admin: the rule references a returns policy must cite, with the values
// Order enforces today.
export const RETURN_SHIPPING_OPTIONS = [
  { value: "none", label: "Không hoàn phí vận chuyển (hiện hành)" },
];

export function returnsRuleRefs(windowDays: number, shipping: string): Record<string, string> {
  return {
    "order.returns_window": `window-${windowDays}d`,
    "order.return_shipping_refund": shipping,
  };
}
