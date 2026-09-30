import {
  Banknote,
  ClipboardList,
  CreditCard,
  FolderTree,
  MessageSquareWarning,
  PackagePlus,
  PackageSearch,
  Percent,
  ShieldCheck,
  Tags,
  Truck,
  Undo2,
  Users,
  Wallet,
} from "lucide-react";

export type AdminNavLink = {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
};
export type AdminNavGroup = { label: string; items: AdminNavLink[] };

export const ADMIN_NAV_GROUPS: AdminNavGroup[] = [
  {
    label: "Moderation",
    items: [
      { href: "/admin/vendors", label: "Vendor applications", icon: ShieldCheck },
      { href: "/admin/products", label: "Product moderation", icon: PackageSearch },
      { href: "/admin/requests", label: "Restock requests", icon: PackagePlus },
      { href: "/admin/reviews", label: "Reviews", icon: MessageSquareWarning },
      { href: "/admin/users", label: "Users", icon: Users },
    ],
  },
  {
    label: "Operations",
    items: [
      { href: "/admin/orders", label: "Orders", icon: ClipboardList },
      { href: "/admin/returns", label: "Returns", icon: Undo2 },
      { href: "/admin/refunds", label: "Refunds", icon: Banknote },
      { href: "/admin/payments", label: "Payment reconciliation", icon: CreditCard },
      { href: "/admin/payouts", label: "Vendor payouts", icon: Wallet },
    ],
  },
  {
    label: "Configuration",
    items: [
      { href: "/admin/categories", label: "Categories", icon: FolderTree },
      { href: "/admin/attributes", label: "Attributes", icon: Tags },
      { href: "/admin/commission", label: "Commission", icon: Percent },
      { href: "/admin/shipping", label: "Shipping", icon: Truck },
    ],
  },
];
export const ADMIN_NAV_LINKS: AdminNavLink[] = ADMIN_NAV_GROUPS.flatMap((group) => group.items);
