import type { AccessibleShop, PermissionDefinition } from "@/lib/api-client";

// Shop staff (AF-17): what the console shows for the active shop. These
// helpers only hide what the person cannot use; every service still checks
// the shop permission with Vendor on each request.

export const PERMISSION_LABELS: Record<string, string> = {
  "products.read": "Xem sản phẩm",
  "products.write": "Thêm/sửa sản phẩm",
  "inventory.read": "Xem tồn kho",
  "inventory.adjust": "Điều chỉnh tồn kho",
  "orders.read": "Xem đơn hàng",
  "orders.fulfill": "Xử lý và giao đơn",
  "returns.handle": "Xử lý trả hàng",
  "finance.read": "Xem tài chính",
  "staff.manage": "Quản lý nhân viên",
  "support.reply": "Trả lời hỗ trợ/đánh giá",
  "shop.availability.write": "Bật/tắt trạng thái bán",
  "analytics.read": "Xem tổng quan bán hàng",
  "marketing.manage": "Quản lý khuyến mãi",
  "live.host": "Dẫn livestream",
  "payout_destination.write": "Tài khoản nhận tiền (chủ shop)",
  "shop.settings.write": "Cài đặt cửa hàng (chủ shop)",
};

export function permissionLabel(name: string): string {
  return PERMISSION_LABELS[name] ?? name;
}

export function can(
  shop: Pick<AccessibleShop, "capabilities"> | undefined,
  permission: string,
): boolean {
  return Boolean(shop?.capabilities.includes(permission));
}

export function isOwner(shop: Pick<AccessibleShop, "role"> | undefined): boolean {
  return shop?.role === "owner";
}

// Which console pages a shop member may open. Pages not listed stay open
// to anyone in the console. "owner" means the shop owner only; "vendor"
// means a vendor account (registering shops is not a shop permission).
export type NavRequirement = { permission: string } | { owner: true } | { vendorAccount: true };

export const NAV_REQUIREMENTS: Record<string, NavRequirement> = {
  "/vendor/payout-accounts": { owner: true },
  "/vendor": { permission: "analytics.read" },
  "/vendor/products": { permission: "products.read" },
  "/vendor/orders": { permission: "orders.read" },
  "/vendor/support": { permission: "support.reply" },
  "/vendor/reviews": { permission: "products.read" },
  "/vendor/shipping": { permission: "orders.read" },
  "/vendor/staff": { permission: "staff.manage" },
  "/vendor/shops": { vendorAccount: true },
};

export function canOpen(
  href: string,
  shop: Pick<AccessibleShop, "role" | "capabilities"> | undefined,
  accountRole: string | undefined,
): boolean {
  const rule = NAV_REQUIREMENTS[href];
  if (!rule) return true;
  if ("vendorAccount" in rule) return accountRole === "vendor";
  if ("owner" in rule) return isOwner(shop);
  return can(shop, rule.permission);
}

// Permissions an actor may hand out: available, never owner-only, and held
// by the actor (an owner holds them all).
export function grantablePermissions(
  registry: PermissionDefinition[],
  actor: Pick<AccessibleShop, "capabilities"> | undefined,
): PermissionDefinition[] {
  return registry.filter((p) => p.available && !p.owner_only && can(actor, p.name));
}

// The invitation link carries its token in the fragment (#token=...), so it
// never reaches a server log. Returns null for anything else.
export function readInvitationToken(hash: string): string | null {
  const params = new URLSearchParams(hash.startsWith("#") ? hash.slice(1) : hash);
  const token = params.get("token");
  return token && /^[A-Za-z0-9_-]{43}$/.test(token) ? token : null;
}

const ACCEPT_ERRORS: Record<string, string> = {
  invitation_used: "Lời mời này đã được sử dụng.",
  invitation_revoked: "Lời mời đã bị thu hồi hoặc thay bằng lời mời mới. Hãy xin cửa hàng gửi lại.",
  invitation_expired: "Lời mời đã hết hạn. Hãy xin cửa hàng gửi lời mời mới.",
  already_member: "Bạn đã là thành viên của cửa hàng này.",
  permission_denied:
    "Lời mời được gửi tới một email khác. Hãy đăng nhập bằng đúng email nhận lời mời.",
  feature_disabled: "Tính năng nhân viên cửa hàng chưa được bật.",
  not_found: "Không tìm thấy lời mời hoặc lời mời không còn hiệu lực.",
};

export function acceptErrorMessage(code: string, fallback: string): string {
  return ACCEPT_ERRORS[code] ?? fallback;
}
