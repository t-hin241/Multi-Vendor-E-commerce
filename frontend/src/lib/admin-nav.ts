import { ClipboardList, FolderTree, PackagePlus, PackageSearch, Percent, ShieldCheck, Tags, Truck, Users } from "lucide-react";

export type AdminNavLink = {
  href: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
};

export type AdminNavGroup = { label: string; items: AdminNavLink[] };

// Single source of truth for the admin console's navigation, grouped by
// domain (spec: moderation / operations / configuration) -- used by both
// the console shell's sidebar/mobile sheet and the header's per-route
// title lookup, so the two never drift apart.
export const ADMIN_NAV_GROUPS: AdminNavGroup[] = [
  {
    label: "Moderation",
    items: [
      { href: "/admin/vendors", label: "Vendor applications", icon: ShieldCheck },
      { href: "/admin/products", label: "Product moderation", icon: PackageSearch },
      { href: "/admin/requests", label: "Restock requests", icon: PackagePlus },
      { href: "/admin/users", label: "Users", icon: Users },
    ],
  },
  {
    label: "Operations",
    items: [{ href: "/admin/orders", label: "Orders", icon: ClipboardList }],
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

export const ADMIN_NAV_LINKS: AdminNavLink[] = ADMIN_NAV_GROUPS.flatMap((g) => g.items);
