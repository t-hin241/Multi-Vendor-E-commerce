import { ClipboardList, LayoutDashboard, MessageSquare, Package, Store, Truck } from "lucide-react";

export type VendorNavLink = {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
};

// Shared source for desktop navigation and the account menu.
export const VENDOR_NAV_LINKS: VendorNavLink[] = [
  { href: "/vendor/payout-accounts", label: "Tài khoản nhận tiền", icon: Store },
  { href: "/vendor", label: "Tổng quan", icon: LayoutDashboard },
  { href: "/vendor/products", label: "Sản phẩm", icon: Package },
  { href: "/vendor/orders", label: "Đơn hàng", icon: ClipboardList },
  { href: "/vendor/reviews", label: "Đánh giá", icon: MessageSquare },
  { href: "/vendor/shipping", label: "Vận chuyển", icon: Truck },
  { href: "/vendor/shops", label: "Cửa hàng", icon: Store },
];
