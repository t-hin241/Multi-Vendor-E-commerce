import type { Vendor } from "@/lib/api-client";

// A vendor user may own several shops; the "active" one is derived, not
// effect-driven state -- it falls back to an approved shop (or the first
// one) until the vendor explicitly picks a different one via the switcher.
// Extracted from the identical logic previously duplicated in
// vendor/layout.tsx's ShopSwitcher and vendor/page.tsx's dashboard.
export function resolveActiveVendor(
  vendors: Vendor[],
  selectedVendorId: string | null,
): Vendor | undefined {
  if (vendors.length === 0) return undefined;
  const fallback = vendors.find((v) => v.status === "approved") ?? vendors[0];
  if (!selectedVendorId) return fallback;
  return vendors.find((v) => v.id === selectedVendorId) ?? fallback;
}
