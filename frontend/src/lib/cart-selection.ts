import type { Cart } from "@/lib/api-client";

export function selectionKey(ids?: readonly string[]): string {
  return ids === undefined ? "all" : [...ids].sort().join(",");
}

export function restoreSelection(raw: string | null, cart: Cart): string[] {
  const current = cart.items.map((line) => line.line_id);
  if (raw === null) return current.sort();
  try {
    const saved: unknown = JSON.parse(raw);
    if (!Array.isArray(saved) || !saved.every((id) => typeof id === "string"))
      return current.sort();
    return current.filter((id) => saved.includes(id)).sort();
  } catch {
    return current.sort();
  }
}

// Display warnings for the selected lines only. Money still comes from Order.
export function selectedCartView(cart: Cart, ids?: string[]): Cart {
  if (ids === undefined) return cart;
  const items = cart.items.filter((line) => ids.includes(line.line_id));
  const unavailable = items.filter((line) => line.state !== "available").length;
  const changed = items.filter((line) => line.price_changed).length;
  const mixed = new Set(items.map((line) => line.currency).filter(Boolean)).size > 1;
  return {
    ...cart,
    items,
    subtotal: null,
    line_count: items.length,
    unavailable_lines: unavailable,
    price_changed_lines: changed,
    mixed_currency: mixed,
    over_line_limit: items.length > 50,
    degraded: { ...cart.degraded, catalog: items.some((line) => line.state === "unverified") },
    checkout_ready: items.length > 0 && items.length <= 50 && !unavailable && !changed && !mixed,
  };
}
