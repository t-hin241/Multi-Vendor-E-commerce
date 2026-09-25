import { ClipboardList, LayoutDashboard, Package, Store, Truck } from "lucide-react";

export type VendorNavLink = {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
};

// Single source of truth for the vendor console's navigation -- used by both
// the console shell's sidebar/mobile sheet and nav-bar.tsx's account dropdown
// for vendor role, so the two never silently drift apart again.
export const VENDOR_NAV_LINKS: VendorNavLink[] = [
  { href: "/vendor", label: "Tổng quan", icon: LayoutDashboard },
  { href: "/vendor/products", label: "Sản phẩm", icon: Package },
  { href: "/vendor/orders", label: "Đơn hàng", icon: ClipboardList },
  { href: "/vendor/shipping", label: "Vận chuyển", icon: Truck },
  { href: "/vendor/shops", label: "Cửa hàng", icon: Store },
];
