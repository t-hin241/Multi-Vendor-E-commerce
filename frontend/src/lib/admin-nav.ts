import {
  Ban,
  Banknote,
  ClipboardList,
  CreditCard,
  FolderTree,
  History,
  Inbox,
  KeyRound,
  LayoutDashboard,
  LifeBuoy,
  Mail,
  MessageSquareWarning,
  PackagePlus,
  PackageSearch,
  ScrollText,
  Stamp,
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
    label: "Overview",
    items: [
      { href: "/admin", label: "Dashboard", icon: LayoutDashboard },
      { href: "/admin/audit", label: "Audit log", icon: History },
    ],
  },
  {
    label: "Moderation",
    items: [
      { href: "/admin/vendors", label: "Vendor applications", icon: ShieldCheck },
      { href: "/admin/products", label: "Product moderation", icon: PackageSearch },
      { href: "/admin/requests", label: "Restock requests", icon: PackagePlus },
      { href: "/admin/reviews", label: "Reviews", icon: MessageSquareWarning },
      { href: "/admin/users", label: "Users", icon: Users },
      { href: "/admin/access", label: "Admin access", icon: KeyRound },
    ],
  },
  {
    label: "Operations",
    items: [
      { href: "/admin/orders", label: "Orders", icon: ClipboardList },
      { href: "/admin/work-items", label: "Case deadlines", icon: ClipboardList },
      { href: "/admin/support", label: "Support cases", icon: LifeBuoy },
      { href: "/admin/cancellations", label: "Paid cancellations", icon: Ban },
      { href: "/admin/fulfillment", label: "Fulfillment", icon: Truck },
      { href: "/admin/returns", label: "Returns", icon: Undo2 },
      { href: "/admin/refunds", label: "Refunds", icon: Banknote },
      { href: "/admin/payments", label: "Payment reconciliation", icon: CreditCard },
      { href: "/admin/payouts", label: "Vendor payouts", icon: Wallet },
      { href: "/admin/approvals", label: "Approvals", icon: Stamp },
      { href: "/admin/notifications", label: "Notifications", icon: Mail },
      { href: "/admin/events", label: "Parked events", icon: Inbox },
    ],
  },
  {
    label: "Configuration",
    items: [
      { href: "/admin/categories", label: "Categories", icon: FolderTree },
      { href: "/admin/attributes", label: "Attributes", icon: Tags },
      { href: "/admin/commission", label: "Commission", icon: Percent },
      { href: "/admin/policies", label: "Policies", icon: ScrollText },
      { href: "/admin/shipping", label: "Shipping", icon: Truck },
    ],
  },
];
export const ADMIN_NAV_LINKS: AdminNavLink[] = ADMIN_NAV_GROUPS.flatMap((group) => group.items);
