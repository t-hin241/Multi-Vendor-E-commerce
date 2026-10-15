import {
  BarChart3,
  CalendarOff,
  ClipboardList,
  FileSpreadsheet,
  LayoutDashboard,
  LifeBuoy,
  MessageSquare,
  Package,
  Store,
  Truck,
  Users,
} from "lucide-react";

export type VendorNavLink = {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
};

// Shared source for desktop navigation and the account menu.
export const VENDOR_NAV_LINKS: VendorNavLink[] = [
  { href: "/vendor/statements", label: "Sao kê tài chính", icon: ClipboardList },
  { href: "/vendor/inventory", label: "Tồn kho", icon: Package },
  { href: "/vendor/payout-accounts", label: "Tài khoản nhận tiền", icon: Store },
  { href: "/vendor", label: "Tổng quan", icon: LayoutDashboard },
  { href: "/vendor/reports", label: "Báo cáo kinh doanh", icon: BarChart3 },
  { href: "/vendor/products", label: "Sản phẩm", icon: Package },
  { href: "/vendor/bulk", label: "Nhập/xuất hàng loạt", icon: FileSpreadsheet },
  { href: "/vendor/orders", label: "Đơn hàng", icon: ClipboardList },
  { href: "/vendor/support", label: "Hỗ trợ đơn hàng", icon: LifeBuoy },
  { href: "/vendor/reviews", label: "Đánh giá", icon: MessageSquare },
  { href: "/vendor/shipping", label: "Vận chuyển", icon: Truck },
  { href: "/vendor/staff", label: "Nhân viên", icon: Users },
  { href: "/vendor/availability", label: "Tạm nghỉ bán", icon: CalendarOff },
  { href: "/vendor/shops", label: "Cửa hàng", icon: Store },
];
